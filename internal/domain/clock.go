package domain

import (
	"context"
	"time"
)

// Clock abstracts time so backoff and controller logic can be tested.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
	// Sleep blocks for d or until ctx is done, returning ctx.Err() in the latter case.
	Sleep(ctx context.Context, d time.Duration) error
}

// RealClock is the Clock backed by the time package.
type RealClock struct{}

// Now returns the current time.
func (RealClock) Now() time.Time { return time.Now() }

// After waits for d and then sends the current time.
func (RealClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Sleep blocks for d or until ctx is done.
func (RealClock) Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
