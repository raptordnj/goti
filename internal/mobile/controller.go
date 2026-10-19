package mobile

import (
	"time"

	"github.com/raptordnj/goti/internal/ui/state"
)

const (
	updateEvery   = time.Second
	pausedTimeout = 10 * time.Minute
)

// Controller drives the Host service lifecycle from snapshots.
type Controller struct {
	host            Host
	running         bool
	completedAtBase int
	lastUpdate      time.Time
	last            Notification
	pausedSince     time.Time
}

// NewController returns a Controller for h.
func NewController(h Host) *Controller { return &Controller{host: h} }

// Tick applies one snapshot observed at now.
func (c *Controller) Tick(s *state.Snapshot, now time.Time) {
	pending := s.ActiveCount + s.Counts[state.SectionQueued]
	paused := s.Counts[state.SectionPaused]
	completed := s.Counts[state.SectionCompleted]
	if !c.running {
		if pending == 0 {
			return
		}
		c.running = true
		c.completedAtBase = completed
		c.pausedSince = time.Time{}
		c.host.StartService()
		c.update(NotificationFor(s), now, true)
		return
	}
	switch {
	case pending > 0:
		c.pausedSince = time.Time{}
		c.update(NotificationFor(s), now, false)
	case paused > 0:
		if c.pausedSince.IsZero() {
			c.pausedSince = now
		}
		c.update(NotificationFor(s), now, false)
		if now.Sub(c.pausedSince) >= pausedTimeout {
			c.stop(completed)
		}
	default:
		c.stop(completed)
	}
}

func (c *Controller) update(n Notification, now time.Time, force bool) {
	if !force && now.Sub(c.lastUpdate) < updateEvery && n.Paused == c.last.Paused && n.Title == c.last.Title {
		return
	}
	c.lastUpdate, c.last = now, n
	c.host.UpdateNotification(n)
}

func (c *Controller) stop(completed int) {
	c.running = false
	c.pausedSince = time.Time{}
	c.last = Notification{}
	c.host.StopService(max(0, completed-c.completedAtBase))
}
