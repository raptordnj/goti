// Package clocktest provides a deterministic domain.Clock for tests.
package clocktest

import (
	"context"
	"sync"
	"time"

	"github.com/raptordnj/goti/internal/domain"
)

var _ domain.Clock = (*FakeClock)(nil)

type waiter struct {
	at time.Time
	ch chan time.Time
}

// FakeClock is a manually advanced clock; it is safe for concurrent use.
type FakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters map[*waiter]struct{}
}

// New returns a FakeClock starting at start.
func New(start time.Time) *FakeClock {
	return &FakeClock{now: start, waiters: make(map[*waiter]struct{})}
}

// Now returns the fake current time.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *FakeClock) add(d time.Duration) *waiter {
	c.mu.Lock()
	defer c.mu.Unlock()
	w := &waiter{at: c.now.Add(d), ch: make(chan time.Time, 1)}
	if d <= 0 {
		w.ch <- c.now
		return w
	}
	c.waiters[w] = struct{}{}
	return w
}

func (c *FakeClock) remove(w *waiter) {
	c.mu.Lock()
	delete(c.waiters, w)
	c.mu.Unlock()
}

// After returns a channel that receives once the clock advances by d.
func (c *FakeClock) After(d time.Duration) <-chan time.Time {
	return c.add(d).ch
}

// Sleep blocks until the clock advances by d or ctx is done.
func (c *FakeClock) Sleep(ctx context.Context, d time.Duration) error {
	w := c.add(d)
	select {
	case <-w.ch:
		return nil
	case <-ctx.Done():
		c.remove(w)
		return ctx.Err()
	}
}

// Advance moves the clock forward by d and fires every due timer.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	for w := range c.waiters {
		if !w.at.After(c.now) {
			w.ch <- c.now
			delete(c.waiters, w)
		}
	}
}

// Waiters returns the number of pending After/Sleep timers.
func (c *FakeClock) Waiters() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.waiters)
}
