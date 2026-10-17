// This file implements the write path: streaming a response body into one
// segment of the temp file with rate limiting, hashing and stall detection
// (spec §6.3, §8.2).

package downloader

import (
	"context"
	"errors"
	"io"
	"math"
	"sync/atomic"
	"time"

	"github.com/raptordnj/swiftfetch/internal/domain"
	"github.com/raptordnj/swiftfetch/internal/ratelimit"
	"github.com/raptordnj/swiftfetch/internal/transport"
)

const (
	// gapWindow is the number of recent inter-read gaps the stall detector
	// keeps for its median.
	gapWindow = 32
	// stallGapFactor multiplies the median gap (spec §8.2).
	stallGapFactor = 10
	// defaultStallTimeout applies when cfg.StallTimeout is unset.
	defaultStallTimeout = 30 * time.Second
)

// errStalled is the cause given to a request cancelled by the stall watchdog.
var errStalled = errors.New("stalled")

func newStallError() error {
	return domain.NewError(domain.KindTransient, "stalled", "", errStalled)
}

// requestBody is a response body together with the cancel function of its
// per-request context. copySegment's stall watchdog calls cancel to abort a
// read that is blocked on a silent connection.
type requestBody struct {
	io.Reader
	cancel context.CancelCauseFunc
}

// gapTracker keeps the last gapWindow read gaps in a fixed ring so the hot
// path never allocates.
type gapTracker struct {
	ring [gapWindow]time.Duration
	n    int
}

func (g *gapTracker) add(d time.Duration) {
	g.ring[g.n%gapWindow] = d
	g.n++
}

func (g *gapTracker) median() time.Duration {
	k := min(g.n, gapWindow)
	if k == 0 {
		return 0
	}
	var tmp [gapWindow]time.Duration
	copy(tmp[:k], g.ring[:k])
	for i := 1; i < k; i++ {
		for j := i; j > 0 && tmp[j] < tmp[j-1]; j-- {
			tmp[j], tmp[j-1] = tmp[j-1], tmp[j]
		}
	}
	return tmp[k/2]
}

// copySegment streams body into seg until Pos()==End() (nil), the body ends
// early (io.ErrUnexpectedEOF, or io.EOF for an open-ended segment whose size
// is unknown), no bytes arrive for the adaptive stall timeout (KindTransient
// "stalled"), or a read, rate-limit wait or write fails. Bytes are written,
// hashed and accounted only after a successful write; each write is at most
// bufSize and truncated to End()-Pos() read immediately before it.
func copySegment(ctx context.Context, env *runEnv, seg *Segment, body io.Reader) error {
	bp, _ := env.bufPool.Get().(*[]byte)
	defer env.bufPool.Put(bp)
	buf := (*bp)[:bufSize]

	var abort context.CancelCauseFunc
	if rb, ok := body.(*requestBody); ok {
		abort = rb.cancel
	}
	base := env.cfg.StallTimeout
	if base <= 0 {
		base = defaultStallTimeout
	}
	var stalled atomic.Bool
	watchdog := time.AfterFunc(math.MaxInt64, func() {
		stalled.Store(true)
		if abort != nil {
			abort(errStalled)
		}
	})
	defer watchdog.Stop()
	watchdog.Stop()

	var gaps gapTracker
	for {
		rem := seg.End() - seg.Pos()
		if rem <= 0 {
			return nil
		}
		watchdog.Reset(max(base, stallGapFactor*gaps.median()))
		t0 := env.clock.Now()
		n, rerr := body.Read(buf[:min(int64(len(buf)), rem)])
		watchdog.Stop()
		if stalled.Load() {
			return newStallError()
		}
		if n > 0 {
			gaps.add(env.clock.Now().Sub(t0))
			if err := ratelimit.WaitAll(ctx, n, env.limits...); err != nil {
				return err
			}
			// End may have shrunk (endgame split) while we read.
			p := buf[:min(int64(n), seg.End()-seg.Pos())]
			if len(p) > 0 {
				w, err := env.write(p, seg.Pos())
				if err == nil && w != len(p) {
					err = io.ErrShortWrite
				}
				if err != nil {
					return err
				}
				seg.Write(p)
				env.addBytes(int64(len(p)))
				if env.onCheckpoint != nil {
					if err := env.onCheckpoint(ctx, int64(len(p))); err != nil {
						return err
					}
				}
			}
		}
		if rerr == nil {
			continue
		}
		if seg.End()-seg.Pos() <= 0 {
			return nil
		}
		if errors.Is(rerr, io.EOF) {
			if seg.End() == math.MaxInt64 {
				return io.EOF
			}
			return io.ErrUnexpectedEOF
		}
		return transport.RedactError(rerr)
	}
}
