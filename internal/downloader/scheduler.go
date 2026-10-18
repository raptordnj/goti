// This file implements the segment scheduler with endgame splitting (spec §6.1–§6.3).

package downloader

import (
	"crypto/sha256"
	"encoding"
	"errors"
	"fmt"
	"hash"
	"math"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/raptordnj/goti/internal/domain"
	"github.com/raptordnj/goti/internal/storage"
)

// splitAlign is the alignment of endgame split points (spec §6.2).
const splitAlign = 64 << 10

type segState uint8

const (
	segPending segState = iota
	segOwned
	segDone
)

// Segment is one half-open byte range [Start, End()) of a download.
//
// Owner contract (spec §6.2/§6.3): the worker that acquired a segment is its
// only writer. Before every write it reads End() and truncates the write to
// End()-Pos(); End() may shrink at any time through TrySplit, but never below
// Pos()+bufSize as observed by the splitter, so a buffer that was sized from a
// stale End() still never crosses the new End. After writing n bytes the owner
// calls Write (hash + advance). When
// Pos()==End() it calls Complete; if Complete reports false, End() moved and
// the owner keeps writing.
type Segment struct {
	// Start is the first byte of the segment; it never changes.
	Start int64

	end atomic.Int64
	pos atomic.Int64

	// hmu serialises hasher updates by the owner against checkpoint snapshots.
	// It is uncontended on the hot path except while Records runs.
	hmu    sync.Mutex
	hasher hash.Hash

	// Fields below are guarded by Scheduler.mu.
	state segState
	owner int
	sum   []byte
}

// End returns the current exclusive end of the segment (lock-free).
func (g *Segment) End() int64 { return g.end.Load() }

// Pos returns the next byte to write (lock-free).
func (g *Segment) Pos() int64 { return g.pos.Load() }

// Remaining returns End()-Pos().
func (g *Segment) Remaining() int64 { return g.End() - g.Pos() }

// advance moves Pos forward by n bytes without hashing. Owner only.
func (g *Segment) advance(n int64) { g.pos.Add(n) }

// Hasher returns the SHA-256 state covering [Start, Pos). Owner only; use
// Write instead when checkpoints may run concurrently.
func (g *Segment) Hasher() hash.Hash { return g.hasher }

// Write hashes p and advances Pos by len(p) atomically with respect to
// Records. Owner only. It never fails.
func (g *Segment) Write(p []byte) {
	g.hmu.Lock()
	_, _ = g.hasher.Write(p)
	g.pos.Add(int64(len(p)))
	g.hmu.Unlock()
}

// Scheduler owns all segments of one download under a single mutex.
type Scheduler struct {
	mu    sync.Mutex
	segs  []*Segment // sorted by Start, pairwise disjoint, union [0,total)
	total int64
	open  bool

	// splitHook, if set (tests only), runs between publishing a split End
	// and re-checking the victim's Pos.
	splitHook func(victim *Segment)
}

// NewScheduler splits [0,total) into fresh segments of chunk bytes (the last
// one may be shorter).
func NewScheduler(total, chunk int64) *Scheduler {
	if chunk <= 0 {
		chunk = total
	}
	s := &Scheduler{total: total}
	for start := int64(0); start < total; {
		end := total
		if chunk < total-start {
			end = start + chunk
		}
		s.segs = append(s.segs, newSegment(start, end, start))
		start = end
	}
	return s
}

// NewOpenScheduler creates a scheduler for a download of unknown size: one
// segment [0, math.MaxInt64) that is never split. Call Finish at EOF.
func NewOpenScheduler() *Scheduler {
	return &Scheduler{
		segs:  []*Segment{newSegment(0, math.MaxInt64, 0)},
		total: math.MaxInt64,
		open:  true,
	}
}

// Finish fixes the size of an open scheduler at n bytes (EOF reached).
func (s *Scheduler) Finish(n int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.open {
		return errors.New("scheduler: Finish on a scheduler of known size")
	}
	g := s.segs[0]
	if n < 0 || n < g.Pos() {
		return fmt.Errorf("scheduler: Finish(%d) below written position %d", n, g.Pos())
	}
	s.total = n
	s.open = false
	g.end.Store(n)
	return nil
}

// Total returns the download size (math.MaxInt64 while open).
func (s *Scheduler) Total() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.total
}

func newSegment(start, end, pos int64) *Segment {
	g := &Segment{Start: start, owner: -1}
	g.end.Store(end)
	g.pos.Store(pos)
	return g
}

// RestoreScheduler rebuilds a scheduler from checkpointed records. Records
// must cover [0,total) exactly with disjoint non-empty segments. In-progress
// records without a hasher state restart at Start.
func RestoreScheduler(total int64, recs []domain.SegmentRecord) (*Scheduler, error) {
	if total < 0 || total == math.MaxInt64 {
		return nil, fmt.Errorf("scheduler: invalid total %d", total)
	}
	sorted := append([]domain.SegmentRecord(nil), recs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start < sorted[j].Start })
	s := &Scheduler{total: total}
	var next int64
	for _, r := range sorted {
		switch {
		case r.Start < next:
			return nil, fmt.Errorf("scheduler: segment [%d,%d) overlaps previous ending at %d", r.Start, r.End, next)
		case r.Start > next:
			return nil, fmt.Errorf("scheduler: gap [%d,%d)", next, r.Start)
		case r.End <= r.Start:
			return nil, fmt.Errorf("scheduler: empty segment [%d,%d)", r.Start, r.End)
		case r.Pos < r.Start || r.Pos > r.End:
			return nil, fmt.Errorf("scheduler: pos %d outside [%d,%d)", r.Pos, r.Start, r.End)
		}
		next = r.End
		g := newSegment(r.Start, r.End, r.Pos)
		switch {
		case r.Done && len(r.Hash) != sha256.Size:
			return nil, fmt.Errorf("scheduler: done segment %d has %d-byte hash", r.Start, len(r.Hash))
		case r.Done:
			g.state = segDone
			g.pos.Store(r.End)
			g.sum = append([]byte(nil), r.Hash...)
		case r.Pos > r.Start && len(r.HasherState) > 0:
			h, err := storage.ResumeHasher(r.HasherState)
			if err != nil {
				return nil, fmt.Errorf("scheduler: segment %d: %w", r.Start, err)
			}
			g.hasher = h
		default:
			g.pos.Store(r.Start)
		}
		s.segs = append(s.segs, g)
	}
	if next != total {
		return nil, fmt.Errorf("scheduler: coverage ends at %d, want %d", next, total)
	}
	return s, nil
}

// Acquire hands the lowest-start pending segment to worker.
func (s *Scheduler) Acquire(worker int) (*Segment, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, g := range s.segs {
		if g.state == segPending {
			s.own(g, worker)
			return g, true
		}
	}
	return nil, false
}

func (s *Scheduler) own(g *Segment, worker int) {
	g.state = segOwned
	g.owner = worker
	if g.hasher == nil {
		g.hasher = sha256.New()
	}
}

// Release returns an owned segment to pending, keeping Pos and hasher state.
func (s *Scheduler) Release(g *Segment) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g.state == segOwned {
		g.state = segPending
		g.owner = -1
	}
}

// Complete marks an owned segment done and stores its final hash. It returns
// false (and changes nothing) unless Pos()==End().
func (s *Scheduler) Complete(g *Segment) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g.state != segOwned || g.Pos() != g.End() {
		return false
	}
	g.hmu.Lock()
	g.sum = g.hasher.Sum(nil)
	g.hmu.Unlock()
	g.state = segDone
	g.owner = -1
	return true
}

// TrySplit implements spec §6.2: when nothing is pending, it splits the
// owned segment (not owned by worker) with the longest estimated remaining
// time, provided its remaining bytes exceed 2*minSplit and its time exceeds
// twice the median of the other owned segments. rate returns bytes/s; a rate
// <= 0 counts as infinitely slow. The new segment [S, oldEnd) is returned
// already owned by worker with a fresh hasher.
func (s *Scheduler) TrySplit(worker int, minSplit, bufSize int64, rate func(*Segment) float64) (*Segment, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.open {
		return nil, false
	}
	var owned []*Segment
	for _, g := range s.segs {
		switch g.state {
		case segPending:
			return nil, false
		case segOwned:
			if g.owner != worker {
				owned = append(owned, g)
			}
		}
	}
	if len(owned) == 0 {
		return nil, false
	}
	eta := func(g *Segment) float64 {
		r := rate(g)
		if r <= 0 {
			return math.Inf(1)
		}
		return float64(g.Remaining()) / r
	}
	best, bestT := -1, -1.0
	for i, g := range owned {
		if g.Remaining() <= 2*minSplit {
			continue
		}
		if t := eta(g); t > bestT {
			best, bestT = i, t
		}
	}
	if best < 0 {
		return nil, false
	}
	if len(owned) > 1 {
		others := make([]float64, 0, len(owned)-1)
		for i, g := range owned {
			if i != best {
				others = append(others, eta(g))
			}
		}
		sort.Float64s(others)
		med := others[len(others)/2]
		if len(others)%2 == 0 {
			med = (others[len(others)/2-1] + med) / 2
		}
		if !(bestT > 2*med) {
			return nil, false
		}
	}
	victim := owned[best]
	oldEnd := victim.End()
	p := victim.Pos()
	sp := max(p+bufSize, victim.Start+(oldEnd-victim.Start)/2)
	sp = (sp + splitAlign - 1) / splitAlign * splitAlign
	if sp >= oldEnd-minSplit {
		return nil, false
	}
	victim.end.Store(sp)
	if s.splitHook != nil {
		s.splitHook(victim)
	}
	// An owner write in flight began at the current (unadvanced) Pos and
	// spans at most bufSize bytes. If the owner advanced between our read and
	// the store, that write might cross sp: undo the split.
	if victim.Pos()+bufSize > sp {
		victim.end.Store(oldEnd)
		return nil, false
	}
	ng := newSegment(sp, oldEnd, sp)
	s.own(ng, worker)
	i := sort.Search(len(s.segs), func(i int) bool { return s.segs[i].Start > victim.Start })
	s.segs = append(s.segs, nil)
	copy(s.segs[i+1:], s.segs[i:])
	s.segs[i] = ng
	return ng, true
}

// Records returns a snapshot of all segments sorted by Start, with marshaled
// hasher states for in-progress segments.
func (s *Scheduler) Records() []domain.SegmentRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]domain.SegmentRecord, 0, len(s.segs))
	for _, g := range s.segs {
		r := domain.SegmentRecord{Start: g.Start, End: g.End()}
		if g.state == segDone {
			r.Pos, r.Done, r.Hash = r.End, true, append([]byte(nil), g.sum...)
		} else {
			g.hmu.Lock()
			r.Pos = g.Pos()
			if g.hasher != nil && r.Pos > g.Start {
				if m, ok := g.hasher.(encoding.BinaryMarshaler); ok {
					if st, err := m.MarshalBinary(); err == nil {
						r.HasherState = st
					}
				}
			}
			g.hmu.Unlock()
			if r.HasherState == nil {
				r.Pos = g.Start
			}
		}
		out = append(out, r)
	}
	return out
}

// AllDone reports whether every segment is done.
func (s *Scheduler) AllDone() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, g := range s.segs {
		if g.state != segDone {
			return false
		}
	}
	return !s.open
}

// DoneBytes returns the total length of done segments.
func (s *Scheduler) DoneBytes() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for _, g := range s.segs {
		if g.state == segDone {
			n += g.End() - g.Start
		}
	}
	return n
}

// Pending returns the number of pending segments.
func (s *Scheduler) Pending() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, g := range s.segs {
		if g.state == segPending {
			n++
		}
	}
	return n
}
