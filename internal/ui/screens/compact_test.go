package screens

import (
	"image"
	"slices"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"

	"github.com/raptordnj/goti/internal/domain"
	"github.com/raptordnj/goti/internal/settings"
	"github.com/raptordnj/goti/internal/ui/state"
)

func TestCompactBreakpoint(t *testing.T) {
	for _, c := range []struct {
		w    int
		want bool
	}{{599, true}, {600, false}, {360, true}, {1100, false}} {
		hs := newHarness(c.w, 600)
		if got := Compact(hs.gtx); got != c.want {
			t.Errorf("width %d: Compact = %v want %v", c.w, got, c.want)
		}
	}
}

func tap(hs *harness, p image.Point, at time.Duration) {
	pos := f32.Pt(float32(p.X), float32(p.Y))
	hs.r.Queue(
		pointer.Event{Kind: pointer.Press, Source: pointer.Touch, Position: pos, Time: at},
		pointer.Event{Kind: pointer.Release, Source: pointer.Touch, Position: pos, Time: at + 10*time.Millisecond},
	)
}

func centre(r image.Rectangle) image.Point { return r.Min.Add(r.Max).Div(2) }

func TestCompactHeaderHasNoAddButton(t *testing.T) {
	recordTexts(t)
	var m Main
	hs := newHarness(360, 780)
	hs.frame(func(gtx layout.Context) {
		m.Layout(gtx, th, snapOf(task("a", domain.StateQueued, state.SectionQueued)), now)
	})
	if !m.AddBounds.Empty() || hasText(t, "Add") {
		t.Errorf("compact header has an Add button: %v", m.AddBounds)
	}
	if m.FABBounds.Empty() {
		t.Fatal("no FAB")
	}
	b := m.FABBounds
	if b.Dx() != 56 || b.Dy() != 56 || b.Max.X != 360-16 || b.Max.Y != 780-16 {
		t.Errorf("FAB bounds %v", b)
	}
}

func TestCompactPillUnderHeader(t *testing.T) {
	var m Main
	s := snapOf(task("a", domain.StateDownloading, state.SectionActive))
	s.ActiveCount = 1
	hs := newHarness(360, 780)
	hs.frame(func(gtx layout.Context) { m.Layout(gtx, th, s, now) })
	if !m.PillShown || m.PillBounds.Min.Y < m.HeaderHeight {
		t.Errorf("pill %v shown=%v header %d", m.PillBounds, m.PillShown, m.HeaderHeight)
	}
}

func TestCompactFABSendsAdd(t *testing.T) {
	var m Main
	hs := newHarness(360, 780)
	s := &state.Snapshot{}
	lay := func(gtx layout.Context) { m.Layout(gtx, th, s, now) }
	hs.frame(lay)
	tap(hs, centre(m.FABBounds), 0)
	var got []Intent
	hs.frame(func(gtx layout.Context) { got = m.Layout(gtx, th, s, now) })
	if !slices.Contains(got, Intent(IntentOpenAdd{})) {
		t.Fatalf("intents %#v", got)
	}
}

func TestRowMinHeightCompact(t *testing.T) {
	var m Main
	hs := newHarness(360, 780)
	s := snapOf(task("a", domain.StateQueued, state.SectionQueued))
	hs.frame(func(gtx layout.Context) { m.Layout(gtx, th, s, now) })
	if len(m.itemH) != 2 || m.itemH[1] < 64 {
		t.Fatalf("item heights %v", m.itemH)
	}
	// Desktop rows keep their 56-dp height (+2 gap).
	var d Main
	hs = newHarness(800, 600)
	hs.frame(func(gtx layout.Context) { d.Layout(gtx, th, s, now) })
	if d.itemH[1] != 58 {
		t.Fatalf("desktop item heights %v", d.itemH)
	}
}

func TestCompactDetailsFullPage(t *testing.T) {
	var m Main
	m.Selected = "a"
	hs := newHarness(360, 780)
	s := snapOf(task("a", domain.StateDownloading, state.SectionActive))
	hs.frame(func(gtx layout.Context) { m.Layout(gtx, th, s, now) })
	if m.DetailsMode != DetailsPage {
		t.Fatalf("mode %v", m.DetailsMode)
	}
	hs.click(&m.details.close, func(gtx layout.Context) { m.Layout(gtx, th, s, now) })
	var got []Intent
	hs.frame(func(gtx layout.Context) { got = m.Layout(gtx, th, s, now) })
	if !slices.Contains(got, Intent(IntentSelect{})) || m.Selected != "" {
		t.Fatalf("back arrow: %#v sel %q", got, m.Selected)
	}
}

func TestLongPressOpensMenu(t *testing.T) {
	var m Main
	hs := newHarness(360, 780)
	s := snapOf(task("a", domain.StateQueued, state.SectionQueued))
	lay := func(gtx layout.Context) { m.Layout(gtx, th, s, now) }
	hs.frame(lay)
	y, ok := m.rowY(s, "a")
	if !ok {
		t.Fatal("row not shown")
	}
	pos := f32.Pt(100, float32(y+20))
	hs.r.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Touch, Position: pos})
	hs.frame(lay)
	hs.gtx.Now = now.Add(600 * time.Millisecond)
	hs.frame(lay)
	if !m.MenuOpen() {
		t.Fatal("long press did not open the menu")
	}
	hs.r.Queue(pointer.Event{Kind: pointer.Release, Source: pointer.Touch, Position: pos, Time: 600 * time.Millisecond})
	hs.frame(lay)
	if m.Selected != "" {
		t.Errorf("long press also selected %q", m.Selected)
	}
}

func TestSettingsHidesDesktopRowsOnMobile(t *testing.T) {
	for _, mobile := range []bool{false, true} {
		RecordTexts(true)
		st := Settings{Mobile: mobile}
		st.Open(settings.Default())
		hs := newHarness(360, 4000)
		hs.frame(func(gtx layout.Context) { st.Layout(gtx, th, &state.Snapshot{}, now) })
		for _, s := range []string{"Download folder", "Close to tray"} {
			if got := slices.Contains(RecordedTexts(), s); got == mobile {
				t.Errorf("mobile=%v: %q shown=%v", mobile, s, got)
			}
		}
		if !slices.Contains(RecordedTexts(), "Reduce motion") {
			t.Errorf("mobile=%v: Reduce motion missing", mobile)
		}
		RecordTexts(false)
	}
}
