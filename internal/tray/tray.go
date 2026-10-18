// Package tray is the SwiftFetch system tray (spec §9): Show, a status line,
// Pause all/Resume all and Quit.
package tray

import (
	"sync"
	"time"

	"fyne.io/systray"

	"github.com/raptordnj/swiftfetch/internal/app"
	"github.com/raptordnj/swiftfetch/internal/ui/state"
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
		systray.SetIcon(icon())
		systray.SetTooltip("SwiftFetch")
		go menuLoop(d, done)
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

// menuLoop builds the menu, forwards clicks and refreshes the status and
// toggle items once per second until done.
func menuLoop(d Deps, done <-chan struct{}) {
	snap := d.Snapshot()
	show := systray.AddMenuItem("Show SwiftFetch", "Open the SwiftFetch window")
	status := systray.AddMenuItem(StatusLine(snap), "")
	status.Disable()
	toggle := systray.AddMenuItem(ToggleLabel(snap), "")
	systray.AddSeparator()
	quit := systray.AddMenuItem("Quit", "Quit SwiftFetch")

	lastStatus, lastToggle := StatusLine(snap), ToggleLabel(snap)
	tick := time.NewTicker(refreshInterval)
	defer tick.Stop()
	for {
		select {
		case <-done:
			return
		case <-show.ClickedCh:
			if d.Show != nil {
				d.Show()
			}
		case <-toggle.ClickedCh:
			if d.Snapshot().QueuePaused {
				d.Send(app.ResumeAll{})
			} else {
				d.Send(app.PauseAll{})
			}
		case <-quit.ClickedCh:
			d.Send(app.Quit{})
		case <-tick.C:
			snap := d.Snapshot()
			if s := StatusLine(snap); s != lastStatus {
				status.SetTitle(s)
				lastStatus = s
			}
			if s := ToggleLabel(snap); s != lastToggle {
				toggle.SetTitle(s)
				lastToggle = s
			}
		}
	}
}
