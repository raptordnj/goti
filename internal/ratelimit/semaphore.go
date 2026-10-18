package ratelimit

import "context"

// Semaphore bounds the number of concurrent connections.
type Semaphore struct {
	ch chan struct{}
}

// NewSemaphore returns a semaphore with capacity n (minimum 1).
func NewSemaphore(n int) *Semaphore {
	return &Semaphore{ch: make(chan struct{}, max(n, 1))}
}

// Acquire blocks until a slot is free or ctx is done.
func (s *Semaphore) Acquire(ctx context.Context) error {
	select {
	case s.ch <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TryAcquire takes a slot without blocking.
func (s *Semaphore) TryAcquire() bool {
	select {
	case s.ch <- struct{}{}:
		return true
	default:
		return false
	}
}

// Release frees a slot; releasing without a matching acquire is a no-op.
func (s *Semaphore) Release() {
	select {
	case <-s.ch:
	default:
	}
}

// InUse returns the number of held slots.
func (s *Semaphore) InUse() int { return len(s.ch) }
