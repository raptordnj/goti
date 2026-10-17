package events

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/raptordnj/swiftfetch/internal/domain"
)

const queueCap = 256

// ErrClosed is returned by Next after the subscription is closed.
var ErrClosed = errors.New("events: subscription closed")

// Bus fans events out to subscribers. Publish never blocks.
type Bus struct {
	interval time.Duration
	clock    domain.Clock

	mu   sync.Mutex
	subs map[*Subscription]struct{}
}

// NewBus returns a Bus emitting progress at most progressHz times per second per task.
func NewBus(progressHz int, clock domain.Clock) *Bus {
	if progressHz < 1 {
		progressHz = 1
	}
	return &Bus{
		interval: time.Second / time.Duration(progressHz),
		clock:    clock,
		subs:     make(map[*Subscription]struct{}),
	}
}

// Subscribe registers a new subscriber with its own bounded queue.
func (b *Bus) Subscribe() *Subscription {
	s := &Subscription{
		bus:     b,
		notify:  make(chan struct{}, 1),
		closed:  make(chan struct{}),
		pending: make(map[string]Progress),
		last:    make(map[string]time.Time),
	}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	return s
}

// Publish delivers e to every subscriber without blocking.
func (b *Bus) Publish(e Event) {
	b.mu.Lock()
	subs := make([]*Subscription, 0, len(b.subs))
	for s := range b.subs {
		subs = append(subs, s)
	}
	b.mu.Unlock()
	for _, s := range subs {
		s.push(e)
	}
}

// Subscription is one consumer's view of the bus. Next is intended for a single consumer goroutine.
type Subscription struct {
	bus    *Bus
	notify chan struct{}
	closed chan struct{}

	mu        sync.Mutex
	isClosed  bool
	queue     []Event
	pending   map[string]Progress // latest undelivered progress per task
	last      map[string]time.Time
	timer     <-chan time.Time // at most one outstanding clock timer, reused across Next calls
	timerFire time.Time
}

func (s *Subscription) push(e Event) {
	s.mu.Lock()
	if s.isClosed {
		s.mu.Unlock()
		return
	}
	if p, ok := e.(Progress); ok {
		s.pending[p.ID] = p
	} else {
		if len(s.queue) >= queueCap {
			clear(s.pending)
			s.queue = append(s.queue[:0], Resync{})
		} else {
			s.queue = append(s.queue, e)
		}
	}
	s.mu.Unlock()
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

// take returns a ready event, or the time at which the earliest pending progress is due.
func (s *Subscription) take(now time.Time) (e Event, due time.Time, have bool) {
	if len(s.queue) > 0 {
		e = s.queue[0]
		s.queue[0] = nil
		s.queue = s.queue[1:]
		if len(s.queue) == 0 {
			s.queue = nil
		}
		return e, time.Time{}, false
	}
	var bestID string
	for id := range s.pending {
		d := s.last[id].Add(s.bus.interval) // zero last => long past => due now
		if !have || d.Before(due) {
			bestID, due, have = id, d, true
		}
	}
	if have && !due.After(now) {
		p := s.pending[bestID]
		delete(s.pending, bestID)
		s.last[bestID] = now
		return p, time.Time{}, false
	}
	return nil, due, have
}

// Next returns the next event, blocking until one is available, ctx is done or the subscription is closed.
// Non-progress events come first in FIFO order; progress is rate-limited per task.
func (s *Subscription) Next(ctx context.Context) (Event, error) {
	for {
		s.mu.Lock()
		if s.isClosed {
			s.mu.Unlock()
			return nil, ErrClosed
		}
		now := s.bus.clock.Now()
		e, due, have := s.take(now)
		if e != nil {
			s.mu.Unlock()
			return e, nil
		}
		var timer <-chan time.Time
		if have {
			// Reuse the outstanding timer if it fires no later than needed, so
			// repeated wakeups never pile up abandoned clock timers.
			if s.timer == nil || due.Before(s.timerFire) {
				s.timer = s.bus.clock.After(due.Sub(now))
				s.timerFire = due
			}
			timer = s.timer
		}
		s.mu.Unlock()

		select {
		case <-s.notify:
		case <-timer:
			s.mu.Lock()
			s.timer = nil
			s.mu.Unlock()
		case <-s.closed:
			return nil, ErrClosed
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// Close unsubscribes and unblocks any pending Next.
func (s *Subscription) Close() {
	s.mu.Lock()
	if s.isClosed {
		s.mu.Unlock()
		return
	}
	s.isClosed = true
	s.queue, s.timer = nil, nil
	clear(s.pending)
	clear(s.last)
	s.mu.Unlock()
	close(s.closed)
	s.bus.mu.Lock()
	delete(s.bus.subs, s)
	s.bus.mu.Unlock()
}
