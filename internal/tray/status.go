package tray

import (
	"strconv"

	"github.com/raptordnj/swiftfetch/internal/platform"
	"github.com/raptordnj/swiftfetch/internal/ui/state"
)

// StatusLine is the tray's disabled status item: "Idle", "Paused" or
// "3 active · 2.4 MiB/s".
func StatusLine(s *state.Snapshot) string {
	switch {
	case s == nil || (!s.QueuePaused && s.ActiveCount == 0):
		return "Idle"
	case s.QueuePaused:
		return "Paused"
	}
	return strconv.Itoa(s.ActiveCount) + " active · " + platform.FormatSpeed(s.TotalSpeedBps)
}

// ToggleLabel is the pause/resume item's label.
func ToggleLabel(s *state.Snapshot) string {
	if s != nil && s.QueuePaused {
		return "Resume all"
	}
	return "Pause all"
}
