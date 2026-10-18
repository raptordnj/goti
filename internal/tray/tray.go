// Package tray is the Goti system tray (spec §9): Show, a status line,
// Pause all/Resume all and Quit.
package tray

import (
	"sync"
	"time"

	"github.com/raptordnj/goti/internal/app"
	"github.com/raptordnj/goti/internal/ui/state"
)

// readyTimeout bounds how long Start waits for the tray to come up.
const readyTimeout = 2 * time.Second

// refreshInterval is the menu refresh period (≤ 1 Hz, spec §9).
const refreshInterval = time.Second

// Deps are the tray's collaborators.
type Deps struct {
	Send     func(app.Command) bool
	Snapshot func() *state.Snapshot
	// Show opens (or focuses) the main window.
	Show func()
}

// Start brings up the tray. available is false when there is no tray host
// (e.g. no StatusNotifierWatcher on the D-Bus session bus) or the tray did not
// become ready within 2 s; then the window close should quit. stop removes
// the tray; it is safe to call more than once.
func Start(d Deps) (available bool, stop func()) {
	if !hostAvailable() {
		return false, func() {}
	}
	ready := make(chan struct{})
	done := make(chan struct{})
	var once sync.Once
	onReady := func() {
		select {
		case <-done:
			return
		default:
		}
		setup(d, done)
		close(ready)
	}
	end := launch(onReady)
	stop = func() {
		once.Do(func() {
			close(done)
			end()
		})
	}
	select {
	case <-ready:
		return true, stop
	case <-time.After(readyTimeout):
		stop()
		return false, func() {}
	}
}
