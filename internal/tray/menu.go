//go:build !darwin

package tray

import (
	"time"

	"fyne.io/systray"

	"github.com/raptordnj/goti/internal/app"
	"github.com/raptordnj/goti/internal/brand"
)

// setup sets the icon and tooltip and starts the menu loop.
func setup(d Deps, done <-chan struct{}) {
	systray.SetIcon(icon())
	systray.SetTooltip(Tooltip)
	go menuLoop(d, done)
}

// menuLoop builds the menu, forwards clicks and refreshes the status and
// toggle items once per second until done.
func menuLoop(d Deps, done <-chan struct{}) {
	snap := d.Snapshot()
	show := systray.AddMenuItem(ShowLabel, "Open the "+brand.Name+" window")
	status := systray.AddMenuItem(StatusLine(snap), "")
	status.Disable()
	toggle := systray.AddMenuItem(ToggleLabel(snap), "")
	systray.AddSeparator()
	quit := systray.AddMenuItem("Quit", "Quit "+brand.Name)

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
