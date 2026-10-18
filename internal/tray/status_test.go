package tray

import (
	"testing"

	"github.com/raptordnj/swiftfetch/internal/ui/state"
)

func TestStatusLine(t *testing.T) {
	for _, tc := range []struct {
		s    state.Snapshot
		want string
	}{
		{state.Snapshot{}, "Idle"},
		{state.Snapshot{ActiveCount: 3, TotalSpeedBps: 2.4 * 1024 * 1024}, "3 active · 2.4 MiB/s"},
		{state.Snapshot{ActiveCount: 1, TotalSpeedBps: 0}, "1 active · 0 B/s"},
		{state.Snapshot{QueuePaused: true}, "Paused"},
		{state.Snapshot{QueuePaused: true, ActiveCount: 2, TotalSpeedBps: 100}, "Paused"},
	} {
		if got := StatusLine(&tc.s); got != tc.want {
			t.Errorf("StatusLine(%+v) = %q, want %q", tc.s, got, tc.want)
		}
	}
	if got := StatusLine(nil); got != "Idle" {
		t.Errorf("nil = %q", got)
	}
}

func TestToggleLabel(t *testing.T) {
	if got := ToggleLabel(&state.Snapshot{}); got != "Pause all" {
		t.Errorf("running = %q", got)
	}
	if got := ToggleLabel(&state.Snapshot{QueuePaused: true}); got != "Resume all" {
		t.Errorf("paused = %q", got)
	}
}
