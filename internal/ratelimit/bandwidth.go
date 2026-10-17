// Package ratelimit provides shared bandwidth limiters and a connection semaphore.
package ratelimit

import (
	"context"

	"golang.org/x/time/rate"
)

const burst = 64 << 10

// Bandwidth is a shared token-bucket byte-rate limiter. A nil *Bandwidth is unlimited.
type Bandwidth struct {
	l *rate.Limiter
}

func toLimit(bytesPerSec int64) rate.Limit {
	if bytesPerSec <= 0 {
		return rate.Inf
	}
	return rate.Limit(bytesPerSec)
}

// NewBandwidth returns a limiter of bytesPerSec bytes per second (0 = unlimited).
func NewBandwidth(bytesPerSec int64) *Bandwidth {
	return &Bandwidth{l: rate.NewLimiter(toLimit(bytesPerSec), burst)}
}

// SetLimit changes the rate at runtime (0 = unlimited).
func (b *Bandwidth) SetLimit(bytesPerSec int64) {
	if b == nil {
		return
	}
	b.l.SetLimit(toLimit(bytesPerSec))
}

// WaitN blocks until n bytes may be transferred, in pieces no larger than the burst.
func (b *Bandwidth) WaitN(ctx context.Context, n int) error {
	if b == nil {
		return nil
	}
	for n > 0 {
		if b.l.Limit() == rate.Inf {
			return ctx.Err()
		}
		p := min(n, burst)
		if err := b.l.WaitN(ctx, p); err != nil {
			return err
		}
		n -= p
	}
	return nil
}

// WaitAll waits on every limiter in turn; the stricter one dominates.
func WaitAll(ctx context.Context, n int, ls ...*Bandwidth) error {
	for _, l := range ls {
		if err := l.WaitN(ctx, n); err != nil {
			return err
		}
	}
	return nil
}
