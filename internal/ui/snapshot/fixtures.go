package snapshot

import (
	"time"

	"github.com/raptordnj/goti/internal/domain"
	"github.com/raptordnj/goti/internal/settings"
	"github.com/raptordnj/goti/internal/ui/state"
)

// Now is the fixed clock used by all fixtures.
var Now = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

func base() *state.Snapshot {
	s := settings.Default()
	s.DownloadDir = "/home/user/Downloads"
	return &state.Snapshot{Settings: s}
}

// Empty has no tasks.
func Empty() *state.Snapshot { return base() }

func eta(d time.Duration) *time.Duration { return &d }

// Busy has eight tasks spanning every section.
func Busy() *state.Snapshot {
	s := base()
	at := Now.Add(-time.Hour)
	mk := func(id, name string, st domain.State, sec state.Section) state.TaskView {
		return state.TaskView{ID: id, FileName: name, DestPath: "/home/user/Downloads/" + name,
			URL: "https://example.com/files/" + name, State: st, Section: sec,
			Percent: -1, CreatedAt: at, Conns: 1, CanOpen: false}
	}
	dl := mk("1", "ubuntu-24.04.1-desktop-amd64.iso", domain.StateDownloading, state.SectionActive)
	dl.Total, dl.TotalKnown, dl.Downloaded, dl.Percent = 6<<30, true, 2<<30, 100.0/3
	dl.SpeedBps, dl.ETA, dl.Conns, dl.CanPause, dl.CanCancel = 12.4*1024*1024, eta(5*time.Minute+30*time.Second), 8, true, true
	dl.Strategy, dl.RangesSupported, dl.Resumable, dl.ChunksDone, dl.ChunksTotal = "multipart", true, true, 21, 64

	long := mk("2", "an-extremely-long-file-name-for-the-quarterly-financial-report-final-v2-revised-reviewed-approved-FINAL.tar.gz",
		domain.StateDownloading, state.SectionActive)
	long.Total, long.TotalKnown, long.Downloaded, long.Percent = 800<<20, true, 600<<20, 75
	long.SpeedBps, long.ETA, long.CanPause, long.CanCancel = 3.2*1024*1024, eta(62*time.Second), true, true

	unk := mk("3", "live-stream-capture.ts", domain.StateDownloading, state.SectionActive)
	unk.Downloaded, unk.SpeedBps, unk.CanPause, unk.CanCancel = 148<<20, 1.1*1024*1024, true, true

	retry := mk("4", "dataset-part-07.zip", domain.StateRetrying, state.SectionActive)
	retry.Total, retry.TotalKnown, retry.Downloaded, retry.Percent = 1<<30, true, 300<<20, 29
	retry.CanCancel = true
	retry.LastError = "connection reset by peer"
	retry.Retries = []state.AttemptView{
		{At: Now.Add(-90 * time.Second), Attempt: 1, Kind: "network", Message: "connection reset by peer", Delay: 2 * time.Second},
		{At: Now.Add(-30 * time.Second), Attempt: 2, Kind: "network", Message: "connection reset by peer", Delay: 8 * time.Second},
	}

	q := mk("5", "installer.exe", domain.StateQueued, state.SectionQueued)
	q.CanCancel = true

	intr := mk("6", "movie-4k.mkv", domain.StatePaused, state.SectionPaused)
	intr.Interrupted, intr.CanResume, intr.CanCancel = true, true, true
	intr.Total, intr.TotalKnown, intr.Downloaded, intr.Percent = 9<<30, true, 4<<30, 44.4

	done := mk("7", "report.pdf", domain.StateCompleted, state.SectionCompleted)
	done.Total, done.TotalKnown, done.Downloaded, done.Percent, done.CanOpen = 2<<20, true, 2<<20, 100, true
	done.VerifyResult = "sha256 verified"

	fail := mk("8", "archive-2019.7z", domain.StateFailed, state.SectionFailed)
	fail.LastError, fail.LastErrorAction = "server returned 404 Not Found", "Check the link and retry"
	fail.CanRetry, fail.CanRestart = true, true

	s.Tasks = []state.TaskView{dl, long, unk, retry, q, intr, done, fail}
	s.ActiveCount, s.TotalSpeedBps = 4, dl.SpeedBps+long.SpeedBps+unk.SpeedBps
	s.Counts = [state.NumSections]int{4, 1, 1, 1, 1}
	return s
}

// Offline is Busy with the offline banner.
func Offline() *state.Snapshot {
	s := Busy()
	s.Offline = true
	return s
}

// WithToasts is Busy with one toast of each kind.
func WithToasts() *state.Snapshot {
	s := Busy()
	s.Toasts = []state.Toast{
		{Kind: state.ToastInfo, Text: "Download added to queue", At: Now},
		{Kind: state.ToastSuccess, Text: "report.pdf finished", At: Now},
		{Kind: state.ToastError, Text: "archive-2019.7z failed: 404", At: Now},
	}
	return s
}
