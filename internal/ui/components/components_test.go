package components

import (
	"image"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/raptordnj/goti/internal/domain"
	"github.com/raptordnj/goti/internal/ui/anim"
	"github.com/raptordnj/goti/internal/ui/icons"
	"github.com/raptordnj/goti/internal/ui/state"
	"github.com/raptordnj/goti/internal/ui/theme"
)

func newGtx() layout.Context {
	return layout.Context{
		Ops:         new(op.Ops),
		Constraints: layout.Exact(image.Pt(800, 600)),
		Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Now:         time.Unix(1000, 0),
	}
}

func fixtures() []state.TaskView {
	eta := 30 * time.Second
	base := func(st domain.State) state.TaskView {
		return state.TaskView{ID: string(st), FileName: "a-very-long-file-name-for-testing-ellipsis.iso", State: st, Percent: -1}
	}
	var out []state.TaskView
	for _, st := range domain.AllStates {
		v := base(st)
		switch st {
		case domain.StateDownloading:
			v.Downloaded, v.Total, v.TotalKnown, v.Percent, v.SpeedBps, v.ETA = 50, 100, true, 50, 1024, &eta
			v.CanPause, v.CanCancel = true, true
		case domain.StateInspecting, domain.StateVerifying, domain.StateQueued:
			v.CanCancel = true
		case domain.StateRetrying:
			v.CanPause, v.CanCancel = true, true
		case domain.StatePaused:
			v.CanResume, v.CanCancel = true, true
		case domain.StateCompleted:
			v.Downloaded, v.Total, v.TotalKnown, v.Percent = 100, 100, true, 100
			v.CanOpen, v.CanRestart = true, true
		case domain.StateFailed, domain.StateCancelled:
			v.LastError = "boom"
			v.CanRetry, v.CanRestart = true, true
		}
		out = append(out, v)
	}
	iv := base(domain.StatePaused)
	iv.Interrupted, iv.CanResume, iv.CanRestart = true, true, true
	return append(out, iv)
}

func nonZero(t *testing.T, name string, d layout.Dimensions) {
	t.Helper()
	if d.Size.X <= 0 || d.Size.Y <= 0 {
		t.Errorf("%s: zero dims %v", name, d.Size)
	}
}

func TestComponentsLayout(t *testing.T) {
	now := time.Unix(1000, 0)
	for _, pal := range []theme.Palette{theme.Light(), theme.Dark()} {
		th := theme.New(pal)
		var c widget.Clickable
		for _, k := range []ButtonKind{Primary, Secondary, Danger, Ghost} {
			nonZero(t, "Button", Button(th, &c, "Add", k)(newGtx()))
		}
		nonZero(t, "IconButton", IconButton(th, &c, icons.Close, "Close")(newGtx()))
		nonZero(t, "Progress", ProgressBar(th, nil, 0.4, th.Accent)(newGtx()))
		nonZero(t, "ProgressIndet", ProgressBar(th, new(anim.Value), -1, th.Accent)(newGtx()))
		nonZero(t, "ButtonIconSmall", Button(th, &c, "Add", Primary, WithIcon(icons.Plus), Small())(newGtx()))
		var b widget.Bool
		nonZero(t, "Switch", Switch(th, &b, "Reduce motion")(newGtx()))
		var en widget.Enum
		nonZero(t, "Segmented", Segmented(th, &en, [][2]string{{"a", "A"}, {"b", "B"}})(newGtx()))
		nonZero(t, "Stepper", Stepper(th, &StepperState{Value: 3}, 1, 10)(newGtx()))
		nonZero(t, "Banner", Banner(th, icons.WifiOff, "Offline", th.Warning)(newGtx()))
		nonZero(t, "Section", SectionHeader(th, "Active", 3, nil)(newGtx()))
		nonZero(t, "SectionCollapsible", SectionHeader(th, "Completed", 3, new(widget.Bool))(newGtx()))
		var e widget.Editor
		nonZero(t, "Field", TextField(th, &e, "URL", "https://", "bad url")(newGtx()))
		nonZero(t, "Dialog", DialogShell(th, nil, "Add", Button(th, &c, "a", Primary), Button(th, &c, "b", Ghost))(newGtx()))
		toasts := []state.Toast{{Kind: state.ToastInfo, Text: "hi"}, {Kind: state.ToastSuccess, Text: "ok"}, {Kind: state.ToastError, Text: "no"}}
		nonZero(t, "Toasts", ToastStack(th, nil, toasts, make([]widget.Clickable, 3))(newGtx()))
		for _, v := range fixtures() {
			var w RowWidgets
			nonZero(t, "TaskRow "+string(v.State), TaskRow(th, v, &w, now, false)(newGtx()))
		}
	}
}

func TestTaskRowButtonsMatchCan(t *testing.T) {
	th := theme.New(theme.Light())
	canKey := map[string]string{"open": "open", "cancel": "cancel", "restart": "restart"}
	for _, v := range fixtures() {
		var w RowWidgets
		TaskRow(th, v, &w, time.Unix(1000, 0), true)(newGtx())
		want := map[string]bool{}
		for name, ok := range map[string]bool{"pause": v.CanPause, "resume": v.CanResume, "cancel": v.CanCancel,
			"retry": v.CanRetry, "restart": v.CanRestart, "open": v.CanOpen} {
			if ok {
				want[name] = true
			}
		}
		got := map[string]bool{}
		primaries := 0
		for _, s := range w.Shown {
			if s != "menu" {
				got[s] = true
				primaries++
			}
		}
		if primaries > 1 {
			t.Errorf("%s: more than one primary action %v", v.State, w.Shown)
		}
		if !slices.Contains(w.Shown, "menu") {
			t.Errorf("%s: selected row hides the more button", v.State)
		}
		for _, it := range RowMenuItems(v) {
			if c, ok := canKey[it.Key]; ok {
				got[c] = true
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s interrupted=%v: shown+menu %v want %v", v.State, v.Interrupted, got, want)
		}
	}
}

func TestRowMoreHiddenUntilHot(t *testing.T) {
	th := theme.New(theme.Light())
	v := fixtures()[0]
	var w RowWidgets
	TaskRow(th, v, &w, time.Unix(1000, 0), false)(newGtx())
	if slices.Contains(w.Shown, "menu") {
		t.Fatalf("more shown on idle row: %v", w.Shown)
	}
}

func TestRowMenuItems(t *testing.T) {
	keys := func(v state.TaskView) []string {
		var out []string
		for _, it := range RowMenuItems(v) {
			out = append(out, it.Key)
		}
		return out
	}
	v := state.TaskView{State: domain.StateDownloading, CanPause: true, CanCancel: true}
	if got, want := keys(v), []string{"folder", "copy-url", "copy-path", "cancel", "remove"}; !reflect.DeepEqual(got, want) {
		t.Errorf("downloading: %v want %v", got, want)
	}
	v = state.TaskView{State: domain.StateCompleted, CanOpen: true, CanRestart: true}
	if got, want := keys(v), []string{"open", "folder", "copy-url", "copy-path", "restart", "remove"}; !reflect.DeepEqual(got, want) {
		t.Errorf("completed: %v want %v", got, want)
	}
}

func TestRowEdgeFixtures(t *testing.T) {
	th := theme.New(theme.Light())
	eta := 5 * time.Minute
	base := state.TaskView{ID: "x", FileName: "file.iso", State: domain.StateDownloading, Percent: -1, CanPause: true, CanCancel: true}
	long := base
	long.FileName = strings.Repeat("very-long-name-", 20) + ".iso"
	zero := base
	zero.Total, zero.TotalKnown = 0, true
	unknown := base
	unknown.Downloaded, unknown.SpeedBps = 123456, 2048
	full := base
	full.Downloaded, full.Total, full.TotalKnown, full.Percent, full.SpeedBps, full.ETA = 100, 100, true, 100, 5, &eta
	neg := base
	neg.SpeedBps, neg.Downloaded = -100, -5
	widths := map[int]bool{}
	for i, v := range []state.TaskView{base, long, zero, unknown, full, neg} {
		var w RowWidgets
		gtx := newGtx()
		d := TaskRow(th, v, &w, time.Unix(1000, 0), false)(gtx)
		if d.Size.Y != 56 {
			t.Errorf("fixture %d: height %d want 56", i, d.Size.Y)
		}
		widths[w.NameWidth] = true
	}
	if len(widths) != 1 {
		t.Errorf("name column width varies with values: %v", widths)
	}
}

func TestRowStaticBarWhenIdle(t *testing.T) {
	cases := []struct {
		v    state.TaskView
		want float32
	}{
		{state.TaskView{State: domain.StateQueued, Percent: -1}, 0},
		{state.TaskView{State: domain.StatePaused, Percent: -1, Downloaded: 10}, 0},
		{state.TaskView{State: domain.StatePaused, Interrupted: true, Percent: 40}, 0.4},
		{state.TaskView{State: domain.StateDownloading, Percent: -1, Downloaded: 10}, -1},
		{state.TaskView{State: domain.StateDownloading, Percent: 50}, 0.5},
	}
	for i, c := range cases {
		if got := rowFrac(c.v); got != c.want {
			t.Errorf("case %d (%s): frac %v want %v", i, c.v.State, got, c.want)
		}
	}
}

func TestSectionHeaderToggles(t *testing.T) {
	th := theme.New(theme.Light())
	var b widget.Bool
	d := SectionHeader(th, "Completed", 2, &b)(newGtx())
	if d.Size.Y != 28 {
		t.Errorf("height %d want 28", d.Size.Y)
	}
}

func TestGlyphsCovered(t *testing.T) {
	for _, s := range append(usedGlyphs(), state.TrayNotice) {
		for _, r := range s {
			if !theme.Covers(r) {
				t.Errorf("glyph %q (in %q) missing from Inter", r, s)
			}
		}
	}
}

func TestTaskRowNameFitsWidth(t *testing.T) {
	th := theme.New(theme.Light())
	v := fixtures()[0]
	v.FileName = "an-extremely-long-archive-name-that-will-never-fit-in-a-narrow-row-1.2.3.tar.gz"
	var w RowWidgets
	gtx := newGtx()
	gtx.Constraints = layout.Exact(image.Pt(300, 600))
	TaskRow(th, v, &w, time.Unix(1000, 0), false)(gtx)
	if !strings.HasSuffix(w.ShownName, ".tar.gz") || len([]rune(w.ShownName)) >= len([]rune(v.FileName)) {
		t.Fatalf("ShownName = %q", w.ShownName)
	}
}

func TestEasedSweepsStale(t *testing.T) {
	gtx := newGtx()
	for i := 0; i < 50; i++ {
		eased(gtx, new(int), 1)
	}
	before := motionLen()
	gtx.Now = gtx.Now.Add(10 * time.Second)
	eased(gtx, new(int), 1)
	if after := motionLen(); after >= before || after > 2 {
		t.Fatalf("motion entries %d -> %d, stale not swept", before, after)
	}
}

func TestRowCancelOnlyPrimary(t *testing.T) {
	v := state.TaskView{ID: "r", FileName: "x.zip", State: domain.StateRetrying, Percent: 29, CanCancel: true}
	var w RowWidgets
	TaskRow(theme.New(theme.Light()), v, &w, time.Unix(1000, 0), false)(newGtx())
	if !slices.Contains(w.Shown, "cancel") {
		t.Fatalf("shown %v, want cancel primary", w.Shown)
	}
}

func TestRowOptimisticIcon(t *testing.T) {
	v := fixtures()[0] // downloading, CanPause
	w := RowWidgets{Optimistic: "resume"}
	TaskRow(theme.New(theme.Light()), v, &w, time.Unix(1000, 0), false)(newGtx())
	if !slices.Contains(w.Shown, "resume") || slices.Contains(w.Shown, "pause") {
		t.Fatalf("shown %v", w.Shown)
	}
}

func TestInterruptedBarColor(t *testing.T) {
	p := theme.Light()
	if got := theme.BarColor(p, domain.StatePaused, true); got == p.Warning {
		t.Fatalf("light interrupted bar uses the text Warning %v", got)
	}
}

func TestRowMenuShareOnMobile(t *testing.T) {
	v := state.TaskView{ID: "a"}
	label := func(items []MenuItem) string {
		for _, it := range items {
			if it.Key == "folder" {
				return it.Label
			}
		}
		return ""
	}
	if got := label(rowMenuItems(v, false)); got != "Show in folder" {
		t.Errorf("desktop %q", got)
	}
	if got := label(rowMenuItems(v, true)); got != "Share" {
		t.Errorf("mobile %q", got)
	}
}
