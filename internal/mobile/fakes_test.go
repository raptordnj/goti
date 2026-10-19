package mobile

import (
	"time"

	"github.com/raptordnj/goti/internal/ui/state"
)

type fakeHost struct {
	calls   []string
	updates []Notification
	stopped []int
}

func (f *fakeHost) StartService() { f.calls = append(f.calls, "start") }
func (f *fakeHost) UpdateNotification(n Notification) {
	f.calls = append(f.calls, "update")
	f.updates = append(f.updates, n)
}
func (f *fakeHost) StopService(c int) {
	f.calls = append(f.calls, "stop")
	f.stopped = append(f.stopped, c)
}
func (f *fakeHost) PublishToDownloads(path, name, mime string) (string, error) {
	return "", nil
}
func (f *fakeHost) RequestNotificationPermission() {}

func (f *fakeHost) count(k string) int {
	n := 0
	for _, c := range f.calls {
		if c == k {
			n++
		}
	}
	return n
}

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func snap(active, queued, paused, completed int) *state.Snapshot {
	s := &state.Snapshot{ActiveCount: active, TotalSpeedBps: 1 << 20}
	s.Counts[state.SectionQueued] = queued
	s.Counts[state.SectionPaused] = paused
	s.Counts[state.SectionCompleted] = completed
	return s
}
