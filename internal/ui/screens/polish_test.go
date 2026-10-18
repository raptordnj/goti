package screens

import (
	"testing"

	"gioui.org/layout"

	"github.com/raptordnj/goti/internal/domain"
	"github.com/raptordnj/goti/internal/ui/state"
)

func TestEmptyStateCentered(t *testing.T) {
	for _, sz := range [][2]int{{800, 600}, {1100, 720}, {720, 560}} {
		var m Main
		hs := newHarness(sz[0], sz[1])
		hs.frame(func(gtx layout.Context) { m.Layout(gtx, th, &state.Snapshot{}, now) })
		b := m.EmptyBounds
		if b.Empty() {
			t.Fatalf("%v: empty state not laid out", sz)
		}
		cx, cy := (b.Min.X+b.Max.X)/2, (b.Min.Y+b.Max.Y)/2
		if abs(cx-sz[0]/2) > 2 || abs(cy-sz[1]/2) > 2 {
			t.Errorf("%v: centre (%d,%d) want (%d,%d)", sz, cx, cy, sz[0]/2, sz[1]/2)
		}
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func TestEmptyStateCopy(t *testing.T) {
	recordTexts(t)
	var m Main
	hs := newHarness(800, 600)
	hs.frame(func(gtx layout.Context) { m.Layout(gtx, th, &state.Snapshot{}, now) })
	for _, s := range []string{"No downloads yet", "Paste a link with Ctrl+V or click Add", "Add download"} {
		if !hasText(t, s) {
			t.Errorf("missing %q in %v", s, RecordedTexts())
		}
	}
}

func TestHeaderIdleHidesPill(t *testing.T) {
	recordTexts(t)
	var m Main
	hs := newHarness(800, 600)
	idle := snapOf(task("a", domain.StateQueued, state.SectionQueued))
	hs.frame(func(gtx layout.Context) { m.Layout(gtx, th, idle, now) })
	if m.PillShown {
		t.Error("pill shown while idle")
	}
	if !hasText(t, "Goti") || !hasText(t, "Add") {
		t.Errorf("header texts %v", RecordedTexts())
	}
	busy := snapOf(task("a", domain.StateDownloading, state.SectionActive))
	busy.ActiveCount, busy.TotalSpeedBps = 1, 8_400_000
	hs.frame(func(gtx layout.Context) { m.Layout(gtx, th, busy, now) })
	if !m.PillShown {
		t.Error("pill hidden while active")
	}
	if m.HeaderHeight != 52 {
		t.Errorf("header height %d want 52", m.HeaderHeight)
	}
}

func TestOfflineBanner(t *testing.T) {
	if OfflineText != "You're offline — downloads resume automatically" {
		t.Fatalf("offline copy %q", OfflineText)
	}
	recordTexts(t)
	var m Main
	hs := newHarness(800, 600)
	s := snapOf(task("a", domain.StateRetrying, state.SectionActive))
	s.Offline = true
	hs.frame(func(gtx layout.Context) { m.Layout(gtx, th, s, now) })
	if !hasText(t, OfflineText) {
		t.Errorf("no offline banner: %v", RecordedTexts())
	}
}

func TestDetailsGroups(t *testing.T) {
	recordTexts(t)
	v := task("a", domain.StateFailed, state.SectionFailed)
	v.VerifyResult = "SHA-256 ok"
	v.Retries = []state.AttemptView{{Attempt: 1, Kind: "timeout", Message: "slow"}}
	d := Details{ID: "a"}
	hs := newHarness(340, 1400)
	hs.frame(func(gtx layout.Context) { d.Layout(gtx, th, snapOf(v), now) })
	for _, s := range []string{"Transfer", "Source", "Integrity", "History", "Size", "URL", "Destination", "SHA-256 ok"} {
		if !hasText(t, s) {
			t.Errorf("missing %q in %v", s, RecordedTexts())
		}
	}
}
