package mobile

import (
	"fmt"
	"time"

	"github.com/raptordnj/goti/internal/platform"
	"github.com/raptordnj/goti/internal/ui/state"
)

// Notification is the foreground-service notification content.
type Notification struct {
	Title, Text string
	Percent     int  // 0..100, -1 = indeterminate
	Paused      bool // drives Pause all <-> Resume all action
}

// NotificationFor builds the notification for a snapshot.
func NotificationFor(s *state.Snapshot) Notification {
	if s.ActiveCount == 0 && s.Counts[state.SectionQueued] == 0 && s.Counts[state.SectionPaused] > 0 {
		return Notification{Title: "Goti", Text: fmt.Sprintf("Paused · %d downloads", s.Counts[state.SectionPaused]), Percent: -1, Paused: true}
	}
	n := Notification{Percent: -1}
	if s.ActiveCount > 0 {
		n.Title = fmt.Sprintf("Goti · %d downloading", s.ActiveCount)
	} else {
		n.Title = fmt.Sprintf("Goti · %d queued", s.Counts[state.SectionQueued])
	}
	n.Text = platform.FormatSpeed(s.TotalSpeedBps)
	var down, total int64
	known, any := true, false
	for _, t := range s.Tasks {
		if t.Section != state.SectionActive {
			continue
		}
		any = true
		if !t.TotalKnown || t.Total <= 0 {
			known = false
			continue
		}
		down += t.Downloaded
		total += t.Total
	}
	if any && known && total > 0 {
		n.Percent = int(down * 100 / total)
		if s.TotalSpeedBps > 0 {
			rem := float64(total - down)
			eta := time.Duration(rem / s.TotalSpeedBps * float64(time.Second))
			n.Text += " · " + platform.FormatETA(eta) + " left"
		}
	}
	return n
}
