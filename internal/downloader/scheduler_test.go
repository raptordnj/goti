package downloader

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/raptordnj/swiftfetch/internal/domain"
)

func one(*Segment) float64 { return 1 }

// coverageErr reports violations of disjointness and exact coverage of [0,total).
func coverageErr(s *Scheduler, total int64) error {
	recs := s.Records()
	var next int64
	for _, r := range recs {
		if r.Start != next || r.End <= r.Start {
			return fmt.Errorf("bad coverage at %d: %+v", next, r)
		}
		next = r.End
	}
	if next != total {
		return fmt.Errorf("coverage ends at %d, want %d", next, total)
	}
	return nil
}

func checkCoverage(t *testing.T, s *Scheduler, total int64) {
	t.Helper()
	if err := coverageErr(s, total); err != nil {
		t.Fatal(err)
	}
	var done int64
	for _, r := range s.Records() {
		if r.Done {
			done += r.End - r.Start
		}
	}
	if got := s.DoneBytes(); got != done {
		t.Fatalf("DoneBytes=%d want %d", got, done)
	}
}

func TestGridCoversExactly(t *testing.T) {
	total := int64(5<<30 + 7)
	s := NewScheduler(total, 64<<20)
	checkCoverage(t, s, total)
	recs := s.Records()
	if last := recs[len(recs)-1]; last.End-last.Start != 7 {
		t.Fatalf("last segment len %d", last.End-last.Start)
	}
	if s.Pending() != len(recs) || s.Total() != total {
		t.Fatal("pending/total mismatch")
	}
}

func TestAcquireExclusive(t *testing.T) {
	s := NewScheduler(10<<20, 1<<20)
	seen := map[*Segment]bool{}
	for w := 0; ; w++ {
		g, ok := s.Acquire(w % 2)
		if !ok {
			break
		}
		if seen[g] {
			t.Fatal("segment acquired twice")
		}
		seen[g] = true
	}
	if len(seen) != 10 {
		t.Fatalf("acquired %d", len(seen))
	}
}

func TestReleaseKeepsPos(t *testing.T) {
	s := NewScheduler(4<<20, 4<<20)
	g, _ := s.Acquire(0)
	g.Write(make([]byte, 1000))
	s.Release(g)
	g2, ok := s.Acquire(1)
	if !ok || g2 != g || g2.Pos() != 1000 {
		t.Fatalf("pos after release: %d", g2.Pos())
	}
}

func TestCompleteRequiresFull(t *testing.T) {
	s := NewScheduler(100, 100)
	g, _ := s.Acquire(0)
	g.Write(make([]byte, 50))
	if s.Complete(g) {
		t.Fatal("completed partial segment")
	}
	g.Write(make([]byte, 50))
	if !s.Complete(g) || !s.AllDone() || s.DoneBytes() != 100 {
		t.Fatal("complete failed")
	}
	want := sha256.Sum256(make([]byte, 100))
	if !bytes.Equal(s.Records()[0].Hash, want[:]) {
		t.Fatal("hash mismatch")
	}
}

func TestSplitAboveWrittenPos(t *testing.T) {
	s := NewScheduler(4<<20, 4<<20)
	g, _ := s.Acquire(0)
	g.Advance(900 << 10)
	ng, ok := s.TrySplit(1, 1<<20, 64<<10, one)
	if !ok {
		t.Fatal("split refused")
	}
	sp := ng.Start
	if sp < g.Pos()+64<<10 || sp%(64<<10) != 0 || g.End() != sp || ng.End() != 4<<20 || ng.Pos() != sp {
		t.Fatalf("bad split S=%d owner end=%d new end=%d", sp, g.End(), ng.End())
	}
	if s.Pending() != 0 {
		t.Fatal("new segment should be owned")
	}
	checkCoverage(t, s, 4<<20)
}

func TestSplitRefusesSmall(t *testing.T) {
	s := NewScheduler(4<<20, 4<<20)
	g, _ := s.Acquire(0)
	g.Advance(2 << 20) // remaining 2 MiB == 2*minSplit
	if _, ok := s.TrySplit(1, 1<<20, 64<<10, one); ok {
		t.Fatal("split of small remainder")
	}
	s2 := NewScheduler(4<<20, 4<<20)
	s2.Acquire(0)
	// S = pos+buf = 3.5 MiB >= End-minSplit = 3 MiB.
	if _, ok := s2.TrySplit(1, 1<<20, 7<<19, one); ok {
		t.Fatal("split too close to end")
	}
	s3 := NewScheduler(8<<20, 4<<20) // pending segment present
	s3.Acquire(0)
	if _, ok := s3.TrySplit(1, 1<<20, 64<<10, one); ok {
		t.Fatal("split while pending exists")
	}
}

func TestRestoreRejectsGaps(t *testing.T) {
	_, err := RestoreScheduler(100, []domain.SegmentRecord{{Start: 0, End: 40, Pos: 0}, {Start: 50, End: 100, Pos: 50}})
	if err == nil {
		t.Fatal("gap accepted")
	}
	_, err = RestoreScheduler(100, []domain.SegmentRecord{{Start: 0, End: 90, Pos: 0}})
	if err == nil {
		t.Fatal("short coverage accepted")
	}
}

func TestRestoreRejectsOverlaps(t *testing.T) {
	_, err := RestoreScheduler(100, []domain.SegmentRecord{{Start: 0, End: 60, Pos: 0}, {Start: 50, End: 100, Pos: 50}})
	if err == nil {
		t.Fatal("overlap accepted")
	}
}

func TestOpenSchedulerFinish(t *testing.T) {
	s := NewOpenScheduler()
	g, _ := s.Acquire(0)
	g.Write(make([]byte, 1234))
	if s.AllDone() {
		t.Fatal("open scheduler done early")
	}
	if _, ok := s.TrySplit(1, 1<<20, 64<<10, one); ok {
		t.Fatal("split of open segment")
	}
	s.Finish(1234)
	if !s.Complete(g) || !s.AllDone() || s.Total() != 1234 {
		t.Fatal("finish/complete failed")
	}
}

func TestRecordsRoundTrip(t *testing.T) {
	data := make([]byte, 3000)
	for i := range data {
		data[i] = byte(i * 7)
	}
	s := NewScheduler(3000, 1000)
	a, _ := s.Acquire(0)
	a.Write(data[:1000])
	s.Complete(a)
	b, _ := s.Acquire(0)
	b.Write(data[1000:1400])
	recs := s.Records()

	r, err := RestoreScheduler(3000, recs)
	if err != nil {
		t.Fatal(err)
	}
	checkCoverage(t, r, 3000)
	if r.DoneBytes() != 1000 || r.Pending() != 2 {
		t.Fatalf("done=%d pending=%d", r.DoneBytes(), r.Pending())
	}
	g, _ := r.Acquire(1)
	if g.Start != 1000 || g.Pos() != 1400 {
		t.Fatalf("resumed at %d/%d", g.Start, g.Pos())
	}
	g.Write(data[1400:2000])
	if !r.Complete(g) {
		t.Fatal("complete")
	}
	want := sha256.Sum256(data[1000:2000])
	if !bytes.Equal(r.Records()[1].Hash, want[:]) {
		t.Fatal("restored hasher diverged")
	}
}

func TestSchedulerRandomOps(t *testing.T) {
	const workers = 8
	rng := rand.New(rand.NewSource(42)) //nolint:gosec // deterministic test seed
	buf := make([]byte, 64<<10)
	for it := 0; it < 500; it++ {
		total := int64(rng.Intn(64<<20) + 1)
		s := NewScheduler(total, int64(rng.Intn(8<<20)+1))
		held := make([]*Segment, workers)
		for op := 0; op < 200; op++ {
			w := rng.Intn(workers)
			g := held[w]
			switch k := rng.Intn(5); {
			case g == nil && k < 4:
				held[w], _ = s.Acquire(w)
			case g == nil:
				held[w], _ = s.TrySplit(w, 1<<20, 64<<10, one)
			case k == 0 || k == 1:
				n := min(int64(rng.Intn(len(buf))+1), g.Remaining())
				g.Write(buf[:n])
			case k == 2:
				s.Release(g)
				held[w] = nil
			case k == 3:
				if s.Complete(g) {
					held[w] = nil
				}
			default:
				s.Release(g)
				held[w], _ = s.TrySplit(w, 1<<20, 64<<10, one)
			}
			checkCoverage(t, s, total)
		}
		for _, g := range held {
			if g != nil {
				s.Release(g)
			}
		}
		for {
			g, ok := s.Acquire(0)
			if !ok {
				break
			}
			for g.Remaining() > 0 {
				g.Write(buf[:min(int64(len(buf)), g.Remaining())])
			}
			if !s.Complete(g) {
				t.Fatal("complete failed")
			}
		}
		if !s.AllDone() || s.DoneBytes() != total {
			t.Fatalf("iteration %d not done", it)
		}
	}
}

// TestSchedulerConcurrent runs owners as goroutines that follow the owner
// contract while idle workers split, and a checkpointer snapshots Records.
func TestSchedulerConcurrent(t *testing.T) {
	var splits atomic.Int64
	for it := 0; it < 20; it++ {
		total := int64(32<<20 + it*4097)
		s := NewScheduler(total, 8<<20)
		data := make([]byte, total)
		_, _ = rand.New(rand.NewSource(int64(it))).Read(data) //nolint:gosec // deterministic test data
		stop := make(chan struct{})
		var ck sync.WaitGroup
		ck.Add(1)
		go func() {
			defer ck.Done()
			for {
				select {
				case <-stop:
					return
				default:
					if err := coverageErr(s, total); err != nil {
						t.Error(err)
						return
					}
				}
			}
		}()
		var wg sync.WaitGroup
		for w := 0; w < 8; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				for {
					g, ok := s.Acquire(w)
					if !ok {
						g, ok = s.TrySplit(w, 1<<20, 64<<10, one)
						if ok {
							splits.Add(1)
						}
					}
					if !ok {
						if s.AllDone() {
							return
						}
						continue
					}
					for {
						for p := g.Pos(); p < g.End(); p = g.Pos() {
							n := min(int64(16<<10), g.End()-p)
							g.Write(data[p : p+n])
						}
						if s.Complete(g) {
							break
						}
					}
				}
			}(w)
		}
		wg.Wait()
		close(stop)
		ck.Wait()
		var next int64
		for _, r := range s.Records() {
			want := sha256.Sum256(data[r.Start:r.End])
			if !r.Done || r.Start != next || !bytes.Equal(r.Hash, want[:]) {
				t.Fatalf("segment %+v bad", r.Start)
			}
			next = r.End
		}
	}
	if splits.Load() == 0 {
		t.Fatal("no endgame split exercised")
	}
}
