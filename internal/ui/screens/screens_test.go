package screens

import (
	"reflect"
	"slices"
	"testing"

	"gioui.org/layout"

	"github.com/raptordnj/goti/internal/app"
	"github.com/raptordnj/goti/internal/domain"
	"github.com/raptordnj/goti/internal/settings"
	"github.com/raptordnj/goti/internal/ui/state"
)

func TestMainEmptyState(t *testing.T) {
	recordTexts(t)
	var m Main
	hs := newHarness(800, 600)
	hs.frame(func(gtx layout.Context) { m.Layout(gtx, th, &state.Snapshot{}, now) })
	for _, s := range []string{"No downloads yet", EmptyHint} {
		if !hasText(t, s) {
			t.Errorf("missing %q in %v", s, RecordedTexts())
		}
	}
}

func TestMainOfflineBanner(t *testing.T) {
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

func TestMainSectionsHidden(t *testing.T) {
	var m Main
	hs := newHarness(800, 600)
	s := snapOf(task("a", domain.StateDownloading, state.SectionActive), task("b", domain.StateCompleted, state.SectionCompleted))
	hs.frame(func(gtx layout.Context) { m.Layout(gtx, th, s, now) })
	want := []state.Section{state.SectionActive, state.SectionCompleted}
	if !reflect.DeepEqual(m.ShownSections, want) {
		t.Fatalf("shown %v want %v", m.ShownSections, want)
	}
}

func TestMainClickPause(t *testing.T) {
	var m Main
	hs := newHarness(800, 600)
	v := task("a", domain.StateDownloading, state.SectionActive)
	v.CanPause, v.CanCancel = true, true
	s := snapOf(v)
	lay := func(gtx layout.Context) { m.Layout(gtx, th, s, now) }
	hs.frame(lay)
	hs.click(&m.Row("a").Pause, lay)
	var got []Intent
	hs.frame(func(gtx layout.Context) { got = m.Layout(gtx, th, s, now) })
	if !slices.Contains(got, Intent(IntentCommand{Cmd: app.PauseTask{ID: "a"}})) {
		t.Fatalf("intents %#v", got)
	}
}

func TestMainCancelAsksConfirm(t *testing.T) {
	var m Main
	hs := newHarness(800, 600)
	v := task("a", domain.StateDownloading, state.SectionActive)
	v.CanPause, v.CanCancel = true, true
	s := snapOf(v)
	lay := func(gtx layout.Context) { m.Layout(gtx, th, s, now) }
	hs.frame(lay)
	hs.click(&m.Row("a").More, lay)
	hs.frame(lay)
	hs.click(m.Row("a").Menu.Item("cancel"), lay)
	var got []Intent
	hs.frame(func(gtx layout.Context) { got = m.Layout(gtx, th, s, now) })
	if !slices.Contains(got, Intent(IntentConfirm{Kind: ConfirmCancel, ID: "a"})) {
		t.Fatalf("intents %#v", got)
	}
}

func TestMainHeaderAdd(t *testing.T) {
	var m Main
	hs := newHarness(800, 600)
	s := &state.Snapshot{}
	lay := func(gtx layout.Context) { m.Layout(gtx, th, s, now) }
	hs.frame(lay)
	hs.click(&m.add, lay)
	var got []Intent
	hs.frame(func(gtx layout.Context) { got = m.Layout(gtx, th, s, now) })
	if !slices.Contains(got, Intent(IntentOpenAdd{})) {
		t.Fatalf("intents %#v", got)
	}
}

func TestDetailsWideVsNarrow(t *testing.T) {
	s := snapOf(task("a", domain.StateDownloading, state.SectionActive))
	for _, c := range []struct {
		w    int
		mode DetailsMode
	}{{1200, DetailsPane}, {800, DetailsSheet}} {
		var m Main
		m.Selected = "a"
		hs := newHarness(c.w, 600)
		hs.frame(func(gtx layout.Context) { m.Layout(gtx, th, s, now) })
		if m.DetailsMode != c.mode {
			t.Errorf("width %d: mode %v want %v", c.w, m.DetailsMode, c.mode)
		}
	}
	var m Main
	hs := newHarness(1200, 600)
	hs.frame(func(gtx layout.Context) { m.Layout(gtx, th, s, now) })
	if m.DetailsMode != DetailsNone {
		t.Errorf("no selection: mode %v", m.DetailsMode)
	}
}

func TestDetailsShowsFields(t *testing.T) {
	recordTexts(t)
	v := task("a", domain.StateFailed, state.SectionFailed)
	v.LastError, v.LastErrorAction = "boom", "Retry the download."
	d := Details{ID: "a"}
	hs := newHarness(500, 800)
	hs.frame(func(gtx layout.Context) { d.Layout(gtx, th, snapOf(v), now) })
	for _, s := range []string{"https://ex.com/a", "/tmp/a.iso", "boom", "Retry the download."} {
		if !hasText(t, s) {
			t.Errorf("missing %q", s)
		}
	}
}

func TestSettingsValidation(t *testing.T) {
	recordTexts(t)
	var st Settings
	st.Open(settings.Default())
	st.globalLimit.SetText("-5")
	hs := newHarness(800, 900)
	s := &state.Snapshot{}
	lay := func(gtx layout.Context) { st.Layout(gtx, th, s, now) }
	hs.frame(lay)
	hs.click(&st.save, lay)
	var got []Intent
	hs.frame(func(gtx layout.Context) { got = st.Layout(gtx, th, s, now) })
	if len(got) != 0 {
		t.Fatalf("expected no intents, got %#v", got)
	}
	if st.errs["GlobalRateLimit"] == "" {
		t.Fatalf("no field error: %v", st.errs)
	}
	hs.frame(lay)
	if !hasText(t, st.errs["GlobalRateLimit"]) {
		t.Errorf("error text not rendered")
	}

	st.globalLimit.SetText("0")
	st.maxSim.Value = 4
	hs.click(&st.save, lay)
	hs.frame(func(gtx layout.Context) { got = st.Layout(gtx, th, s, now) })
	want := settings.Default()
	want.MaxSimultaneous = 4
	if !slices.Contains(got, Intent(IntentCommand{Cmd: app.SaveSettings{S: want}})) || !slices.Contains(got, Intent(IntentCloseSettings{})) {
		t.Fatalf("intents %#v", got)
	}
}

func TestRenderedGlyphsCovered(t *testing.T) {
	recordTexts(t)
	v := task("a", domain.StateFailed, state.SectionFailed)
	v.LastError, v.Strategy, v.DecisionSummary = "boom", "multi", "ranges ok"
	v.Retries = []state.AttemptView{{Attempt: 1, Kind: "timeout", Message: "slow"}}
	s := snapOf(v)
	s.Offline = true
	m := Main{Selected: "a"}
	var st Settings
	st.Open(settings.Default())
	hs := newHarness(1200, 900)
	hs.frame(func(gtx layout.Context) { m.Layout(gtx, th, s, now); st.Layout(gtx, th, s, now) })
	hs.frame(func(gtx layout.Context) { m.Layout(gtx, th, &state.Snapshot{}, now) })
	assertGlyphs(t, RecordedTexts())
}

func TestDetailsRangeChunksResume(t *testing.T) {
	recordTexts(t)
	v := task("a", domain.StatePaused, state.SectionPaused)
	v.RangesSupported, v.Resumable, v.ChunksDone, v.ChunksTotal = true, true, 3, 8
	w := task("b", domain.StateQueued, state.SectionQueued)
	d := Details{ID: "a"}
	hs := newHarness(500, 1200)
	hs.frame(func(gtx layout.Context) { d.Layout(gtx, th, snapOf(v, w), now) })
	for _, s := range []string{"Yes", "3 of 8 done", "Supported"} {
		if !hasText(t, s) {
			t.Errorf("a: missing %q in %v", s, RecordedTexts())
		}
	}
	RecordTexts(true)
	d.ID = "b"
	hs.frame(func(gtx layout.Context) { d.Layout(gtx, th, snapOf(v, w), now) })
	if !hasText(t, "No") || !hasText(t, "Not supported") || hasText(t, "Chunks") {
		t.Errorf("b: %v", RecordedTexts())
	}
}

func TestSectionCollapse(t *testing.T) {
	var m Main
	hs := newHarness(800, 600)
	s := snapOf(task("a", domain.StateDownloading, state.SectionActive), task("b", domain.StateCompleted, state.SectionCompleted),
		task("c", domain.StateFailed, state.SectionFailed))
	lay := func(gtx layout.Context) { m.Layout(gtx, th, s, now) }
	hs.frame(lay)
	if !reflect.DeepEqual(m.ShownTasks, []string{"a", "b", "c"}) {
		t.Fatalf("expanded: %v", m.ShownTasks)
	}
	if m.Collapsible(state.SectionActive) != nil || m.Collapsible(state.SectionCompleted) == nil || m.Collapsible(state.SectionFailed) == nil {
		t.Fatal("only Completed and Failed are collapsible")
	}
	hs.click(m.Collapsible(state.SectionCompleted), lay)
	hs.frame(lay)
	hs.frame(lay)
	if !reflect.DeepEqual(m.ShownTasks, []string{"a", "c"}) {
		t.Fatalf("collapsed: %v", m.ShownTasks)
	}
	want := []state.Section{state.SectionActive, state.SectionCompleted, state.SectionFailed}
	if !reflect.DeepEqual(m.ShownSections, want) {
		t.Fatalf("headers %v want %v", m.ShownSections, want)
	}
}

func TestRowMenuIntents(t *testing.T) {
	v := task("a", domain.StateCompleted, state.SectionCompleted)
	v.CanOpen, v.CanRestart = true, true
	cases := map[string]Intent{
		"open":      IntentOpen{Path: v.DestPath},
		"folder":    IntentOpen{Path: v.DestPath, Reveal: true},
		"copy-url":  IntentCopy{Text: v.URL},
		"copy-path": IntentCopy{Text: v.DestPath},
		"restart":   IntentCommand{Cmd: app.RestartTask{ID: "a"}},
		"remove":    IntentConfirm{Kind: ConfirmRemove, ID: "a"},
	}
	for key, want := range cases {
		var m Main
		hs := newHarness(800, 600)
		s := snapOf(v)
		lay := func(gtx layout.Context) { m.Layout(gtx, th, s, now) }
		hs.frame(lay)
		hs.click(&m.Row("a").More, lay)
		hs.frame(lay)
		if !m.MenuOpen() {
			t.Fatalf("%s: menu not open", key)
		}
		item := m.Row("a").Menu.Item(key)
		if item == nil {
			t.Fatalf("%s: no menu item", key)
		}
		hs.click(item, lay)
		var got []Intent
		hs.frame(func(gtx layout.Context) { got = m.Layout(gtx, th, s, now) })
		if !slices.Contains(got, want) {
			t.Errorf("%s: intents %#v want %#v", key, got, want)
		}
		if m.MenuOpen() {
			t.Errorf("%s: menu still open after pick", key)
		}
	}
}

func TestRowMenuCancelGated(t *testing.T) {
	v := task("a", domain.StateDownloading, state.SectionActive)
	v.CanPause, v.CanCancel = true, true
	var m Main
	hs := newHarness(800, 600)
	s := snapOf(v)
	lay := func(gtx layout.Context) { m.Layout(gtx, th, s, now) }
	hs.frame(lay)
	hs.click(&m.Row("a").More, lay)
	hs.frame(lay)
	item := m.Row("a").Menu.Item("cancel")
	if item == nil {
		t.Fatal("no cancel item")
	}
	hs.click(item, lay)
	s.Tasks[0].CanCancel = false // state changed before the pick lands
	var got []Intent
	hs.frame(func(gtx layout.Context) { got = m.Layout(gtx, th, s, now) })
	for _, in := range got {
		if _, ok := in.(IntentConfirm); ok {
			t.Fatalf("cancel emitted although CanCancel is false: %#v", got)
		}
	}
}

func TestSettingsSteppersClamp(t *testing.T) {
	var st Settings
	st.Open(settings.Default())
	st.maxSim.Value, st.maxConn.Value = 50, 0
	hs := newHarness(800, 900)
	s := &state.Snapshot{}
	lay := func(gtx layout.Context) { st.Layout(gtx, th, s, now) }
	hs.frame(lay)
	if st.maxSim.Value != 10 || st.maxConn.Value != 1 {
		t.Fatalf("not clamped: sim=%d conn=%d", st.maxSim.Value, st.maxConn.Value)
	}
	st.maxConn.Value = 16
	hs.click(&st.maxConn.Inc, lay)
	hs.frame(lay)
	if st.maxConn.Value != 16 {
		t.Fatalf("conn=%d, want 16", st.maxConn.Value)
	}
	hs.click(&st.maxSim.Dec, lay)
	hs.frame(lay)
	if st.maxSim.Value != 9 {
		t.Fatalf("sim=%d, want 9", st.maxSim.Value)
	}
}

func TestSettingsReduceMotionSaved(t *testing.T) {
	var st Settings
	st.Open(settings.Default())
	hs := newHarness(800, 900)
	s := &state.Snapshot{}
	lay := func(gtx layout.Context) { st.Layout(gtx, th, s, now) }
	hs.frame(lay)
	hs.click(&st.reduceMotion, lay)
	hs.frame(lay)
	hs.click(&st.save, lay)
	var got []Intent
	hs.frame(func(gtx layout.Context) { got = st.Layout(gtx, th, s, now) })
	want := settings.Default()
	want.ReduceMotion = true
	if !slices.Contains(got, Intent(IntentCommand{Cmd: app.SaveSettings{S: want}})) {
		t.Fatalf("intents %#v", got)
	}
}

func TestSettingsLimitsKB(t *testing.T) {
	var st Settings
	d := settings.Default()
	d.GlobalRateLimit = 2048
	st.Open(d)
	if st.globalLimit.Text() != "2" {
		t.Fatalf("shown %q, want 2 (KB/s)", st.globalLimit.Text())
	}
	st.perLimit.SetText("5")
	got, errs := st.collect()
	if len(errs) != 0 || got.PerDownloadLimit != 5*1024 || got.GlobalRateLimit != 2048 {
		t.Fatalf("got %+v errs %v", got, errs)
	}
}
