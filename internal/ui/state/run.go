package state

import (
	"context"
	"time"

	"github.com/raptordnj/swiftfetch/internal/domain"
	"github.com/raptordnj/swiftfetch/internal/events"
	"github.com/raptordnj/swiftfetch/internal/settings"
)

// publishInterval is the minimum spacing between snapshot publications.
const publishInterval = 100 * time.Millisecond

// Deps are the inputs of the state goroutine.
type Deps struct {
	Bus         *events.Bus
	Store       TaskStore
	Running     func(id string) bool
	QueuePaused func() bool
	Settings    func() settings.Settings
	Clock       domain.Clock
	Invalidate  func()
}

// Run loads the initial state, then folds bus events into s until ctx is done.
// Publications are coalesced to at most one per 100 ms of clock time.
func Run(ctx context.Context, d Deps, s *Store) {
	if d.Clock == nil {
		d.Clock = domain.RealClock{}
	}
	sub := d.Bus.Subscribe()
	defer sub.Close()

	m := newModelState()
	m.running, m.queuePaused, m.settings = d.Running, d.QueuePaused, d.Settings

	evs := make(chan events.Event)
	go func() {
		defer close(evs)
		for {
			e, err := sub.Next(ctx)
			if err != nil {
				return
			}
			select {
			case evs <- e:
			case <-ctx.Done():
				return
			}
		}
	}()

	var (
		lastPub time.Time
		timer   <-chan time.Time
		timerAt time.Time
	)
	arm := func(at time.Time) {
		if timer != nil && !at.Before(timerAt) {
			return
		}
		now := d.Clock.Now()
		timer, timerAt = d.Clock.After(at.Sub(now)), at
	}
	publish := func() {
		now := d.Clock.Now()
		snap := build(m, now)
		s.publish(snap)
		lastPub = now
		if d.Invalidate != nil {
			d.Invalidate()
		}
		// Wake again to drop expired toasts or tick "Retrying in N s".
		_, exp := m.liveToasts(now)
		for _, v := range snap.Tasks {
			if v.State == domain.StateRetrying && !v.retryAt.IsZero() && v.retryAt.After(now) {
				if t := now.Add(time.Second); exp.IsZero() || t.Before(exp) {
					exp = t
				}
				break
			}
		}
		if !exp.IsZero() {
			arm(exp)
		}
	}
	maybePublish := func() {
		now := d.Clock.Now()
		if lastPub.IsZero() || now.Sub(lastPub) >= publishInterval {
			publish()
			return
		}
		arm(lastPub.Add(publishInterval))
	}

	loadAll := func() {
		if err := reloadAll(ctx, m, d.Store); err != nil {
			if ctx.Err() == nil {
				m.err = "Could not load downloads: " + err.Error()
			}
			return
		}
		m.err = ""
	}
	loadAll()
	publish()

	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-evs:
			if !ok {
				return
			}
			r := apply(m, e, d.Clock.Now())
			switch {
			case r.full:
				loadAll()
			case r.reload != "":
				if err := reloadOne(ctx, m, d.Store, r.reload); err != nil && ctx.Err() == nil {
					loadAll()
				}
			}
			if r.dirty {
				maybePublish()
			}
		case <-timer:
			timer = nil
			now := d.Clock.Now()
			if now.Sub(lastPub) < publishInterval {
				arm(lastPub.Add(publishInterval))
				continue
			}
			publish()
		}
	}
}
