// Package services holds application services above the download engine.
package services

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/raptordnj/swiftfetch/internal/domain"
	"github.com/raptordnj/swiftfetch/internal/downloader"
	"github.com/raptordnj/swiftfetch/internal/events"
)

const (
	tickInterval  = 5 * time.Second
	lockedSkip    = 30 * time.Second
	pausePoll     = 2 * time.Millisecond
	lockedWarning = "running in another SwiftFetch process"
)

// QueueEngine is the engine surface the queue drives; *downloader.Engine satisfies it.
type QueueEngine interface {
	Run(ctx context.Context, id string) error
	Pause(id string) bool
	Hold(ctx context.Context, id string) error
	Requeue(ctx context.Context, id string) error
	Running(id string) bool
	Shutdown(ctx context.Context) error
}

// QueueDeps are the queue's collaborators.
type QueueDeps struct {
	Engine QueueEngine
	Store  interface {
		ListTasks(context.Context) ([]*domain.Task, error)
	}
	Bus             *events.Bus
	Clock           domain.Clock
	Logger          *slog.Logger
	MaxSimultaneous int
	ResumeOnStartup bool
}

// Queue starts queued tasks FIFO by CreatedAt, at most MaxSimultaneous at once.
type Queue struct {
	d    QueueDeps
	wake chan struct{}

	mu        sync.Mutex
	slots     int
	paused    bool
	stopped   bool
	running   map[string]chan struct{} // closed when the run returns
	wanted    map[string]bool          // interrupted tasks to run although not queued
	held      map[string]bool          // paused by the user; never picked until Resume
	skipUntil map[string]time.Time
	cancel    context.CancelFunc
	loopDone  chan struct{}
	runs      sync.WaitGroup
}

// Interrupted reports whether s is an active state, i.e. one a task is left
// in by a crash when no run is live.
func Interrupted(s domain.State) bool {
	switch s {
	case domain.StateInspecting, domain.StateDownloading, domain.StateRetrying, domain.StateVerifying:
		return true
	}
	return false
}

// NewQueue returns a queue; call Start to begin scheduling.
func NewQueue(d QueueDeps) *Queue {
	if d.Clock == nil {
		d.Clock = domain.RealClock{}
	}
	if d.Logger == nil {
		d.Logger = slog.New(slog.DiscardHandler)
	}
	return &Queue{
		d: d, wake: make(chan struct{}, 1), slots: max(d.MaxSimultaneous, 1),
		running: map[string]chan struct{}{}, wanted: map[string]bool{},
		held: map[string]bool{}, skipUntil: map[string]time.Time{},
		loopDone: make(chan struct{}),
	}
}

// Start restores tasks left by a previous session and spawns the scheduler,
// which runs until ctx is done or Shutdown. It returns immediately.
func (q *Queue) Start(ctx context.Context) {
	lctx, cancel := context.WithCancel(ctx)
	q.mu.Lock()
	q.cancel = cancel
	q.mu.Unlock()
	go func() {
		defer close(q.loopDone)
		q.restore(lctx)
		for {
			q.schedule(ctx, lctx)
			select {
			case <-lctx.Done():
				return
			case <-q.wake:
			case <-q.d.Clock.After(tickInterval):
			}
		}
	}()
}

func (q *Queue) restore(ctx context.Context) {
	tasks, err := q.d.Store.ListTasks(ctx)
	if err != nil {
		q.d.Logger.Warn("queue restore", "err", err)
		return
	}
	for _, t := range tasks {
		switch {
		case Interrupted(t.State) && q.d.ResumeOnStartup && !q.d.Engine.Running(t.ID):
			q.mu.Lock()
			q.wanted[t.ID] = true
			q.mu.Unlock()
		case t.State == domain.StateQueued && !q.d.ResumeOnStartup:
			if err := q.d.Engine.Hold(ctx, t.ID); err != nil {
				q.d.Logger.Warn("queue restore hold", "id", t.ID, "err", err)
			}
		}
	}
}

// schedule starts eligible tasks while slots are free. runCtx is the parent
// of every Run; lctx bounds the store read.
func (q *Queue) schedule(runCtx, lctx context.Context) {
	tasks, err := q.d.Store.ListTasks(lctx)
	if err != nil {
		if lctx.Err() == nil {
			q.d.Logger.Warn("queue list", "err", err)
		}
		return
	}
	sort.Slice(tasks, func(i, j int) bool {
		a, b := tasks[i], tasks[j]
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.Before(b.CreatedAt)
		}
		return a.ID < b.ID
	})
	now := q.d.Clock.Now()
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, t := range tasks {
		if q.paused || q.stopped || len(q.running) >= q.slots {
			return
		}
		id := t.ID
		eligible := t.State == domain.StateQueued || (q.wanted[id] && Interrupted(t.State))
		if !eligible || q.held[id] || q.running[id] != nil || now.Before(q.skipUntil[id]) || q.d.Engine.Running(id) {
			continue
		}
		delete(q.wanted, id)
		delete(q.skipUntil, id)
		done := make(chan struct{})
		q.running[id] = done
		q.runs.Add(1)
		go func() {
			defer q.runs.Done()
			err := q.d.Engine.Run(runCtx, id)
			q.finished(id, done, err)
		}()
	}
}

func (q *Queue) finished(id string, done chan struct{}, err error) {
	locked := errors.Is(err, downloader.ErrLockedElsewhere)
	q.mu.Lock()
	delete(q.running, id)
	close(done)
	if locked {
		q.skipUntil[id] = q.d.Clock.Now().Add(lockedSkip)
	}
	q.mu.Unlock()
	if locked && q.d.Bus != nil {
		q.d.Bus.Publish(events.Warning{ID: id, Msg: lockedWarning})
	}
	q.signal()
}

func (q *Queue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// Enqueue wakes the scheduler, e.g. after a task was added.
func (q *Queue) Enqueue(string) { q.signal() }

// Pause stops a running task or holds a queued idle one. It returns once
// the task can no longer be started or continue running from this queue.
func (q *Queue) Pause(ctx context.Context, id string) error {
	return q.pause(ctx, id, true)
}

func (q *Queue) pause(ctx context.Context, id string, hold bool) error {
	q.mu.Lock()
	if hold {
		q.held[id] = true
	}
	delete(q.wanted, id)
	done := q.running[id]
	q.mu.Unlock()
	if done != nil {
		if err := q.stopUntil(ctx, id, func() bool {
			select {
			case <-done:
				return true
			default:
				return false
			}
		}); err != nil {
			return err
		}
		// The run may have ended without pausing (e.g. locked elsewhere).
		if err := q.d.Engine.Hold(ctx, id); err != nil && !errors.Is(err, domain.ErrInvalidTransition) {
			return err
		}
		return nil
	}
	err := q.d.Engine.Hold(ctx, id)
	if errors.Is(err, downloader.ErrAlreadyRunning) {
		return q.stopUntil(ctx, id, func() bool { return !q.d.Engine.Running(id) })
	}
	return err
}

// stopUntil keeps asking the engine to pause id until stopped holds, since
// a run may be between being started and registering with the engine.
func (q *Queue) stopUntil(ctx context.Context, id string, stopped func() bool) error {
	for !stopped() {
		q.d.Engine.Pause(id)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pausePoll):
		}
	}
	return nil
}

// Resume requeues a paused, failed or interrupted task and wakes the scheduler.
func (q *Queue) Resume(ctx context.Context, id string) error {
	q.mu.Lock()
	delete(q.held, id)
	q.mu.Unlock()
	if err := q.d.Engine.Requeue(ctx, id); err != nil {
		return err
	}
	q.mu.Lock()
	q.wanted[id] = true // only used if the task is in an interrupted state
	q.mu.Unlock()
	q.signal()
	return nil
}

// PauseAll stops scheduling, holds every idle queued task and pauses every running one.
func (q *Queue) PauseAll(ctx context.Context) error {
	q.mu.Lock()
	q.paused = true
	ids := make([]string, 0, len(q.running))
	for id := range q.running {
		ids = append(ids, id)
	}
	q.mu.Unlock()
	tasks, err := q.d.Store.ListTasks(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, t := range tasks {
		if t.State == domain.StateQueued {
			if err := q.pause(ctx, t.ID, false); err != nil {
				errs = append(errs, err)
			}
		}
	}
	for _, id := range ids {
		if err := q.pause(ctx, id, false); err != nil && !errors.Is(err, domain.ErrInvalidTransition) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// ResumeAll re-enables scheduling and requeues every paused task.
func (q *Queue) ResumeAll(ctx context.Context) error {
	q.mu.Lock()
	q.paused = false
	clear(q.held)
	q.mu.Unlock()
	defer q.signal()
	tasks, err := q.d.Store.ListTasks(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, t := range tasks {
		if t.State == domain.StatePaused {
			if err := q.d.Engine.Requeue(ctx, t.ID); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// Paused reports whether PauseAll is in effect.
func (q *Queue) Paused() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.paused
}

// SetMaxSimultaneous changes the slot count; running tasks above it keep running.
func (q *Queue) SetMaxSimultaneous(n int) {
	q.mu.Lock()
	q.slots = max(n, 1)
	q.mu.Unlock()
	q.signal()
}

// Shutdown stops scheduling, shuts the engine down (pausing running tasks)
// and waits for the scheduler and every run it started to return.
func (q *Queue) Shutdown(ctx context.Context) error {
	q.mu.Lock()
	first := !q.stopped
	q.stopped = true
	cancel := q.cancel
	q.mu.Unlock()
	var err error
	if first {
		err = q.d.Engine.Shutdown(ctx)
	}
	if cancel != nil {
		cancel()
		<-q.loopDone
	}
	q.runs.Wait()
	return err
}
