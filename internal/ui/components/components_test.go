package components

import (
	"image"
	"reflect"
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
		nonZero(t, "Section", SectionHeader(th, "Active", 3)(newGtx()))
		var e widget.Editor
		nonZero(t, "Field", TextField(th, &e, "URL", "https://", "bad url")(newGtx()))
		nonZero(t, "Dialog", DialogShell(th, nil, "Add", Button(th, &c, "a", Primary), Button(th, &c, "b", Ghost))(newGtx()))
		toasts := []state.Toast{{Kind: state.ToastInfo, Text: "hi"}, {Kind: state.ToastSuccess, Text: "ok"}, {Kind: state.ToastError, Text: "no"}}
		nonZero(t, "Toasts", ToastStack(th, nil, toasts, make([]widget.Clickable, 3))(newGtx()))
		for _, v := range fixtures() {
			var w RowWidgets
			nonZero(t, "TaskRow "+string(v.State), TaskRow(th, v, &w, now)(newGtx()))
		}
	}
}

func TestTaskRowButtonsMatchCan(t *testing.T) {
	th := theme.New(theme.Light())
	for _, v := range fixtures() {
		var w RowWidgets
		TaskRow(th, v, &w, time.Unix(1000, 0))(newGtx())
		want := map[string]bool{}
		for name, ok := range map[string]bool{"pause": v.CanPause, "resume": v.CanResume, "cancel": v.CanCancel,
			"retry": v.CanRetry, "restart": v.CanRestart, "open": v.CanOpen} {
			if ok {
				want[name] = true
			}
		}
		got := map[string]bool{}
		for _, s := range w.Shown {
			if s != "menu" {
				got[s] = true
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s interrupted=%v: shown %v want %v", v.State, v.Interrupted, got, want)
		}
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
	TaskRow(th, v, &w, time.Unix(1000, 0))(gtx)
	if !strings.HasSuffix(w.ShownName, ".tar.gz") || len([]rune(w.ShownName)) >= len([]rune(v.FileName)) {
		t.Fatalf("ShownName = %q", w.ShownName)
	}
}
