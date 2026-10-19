package components

import (
	"image"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/widget"

	"github.com/raptordnj/goti/internal/ui/icons"
	"github.com/raptordnj/goti/internal/ui/theme"
)

type harness struct {
	r   input.Router
	gtx layout.Context
}

func newHarness() *harness {
	hs := &harness{gtx: newGtx()}
	hs.gtx.Source = hs.r.Source()
	return hs
}

func (hs *harness) frame(f func(gtx layout.Context)) {
	hs.gtx.Ops.Reset()
	f(hs.gtx)
	hs.r.Frame(hs.gtx.Ops)
}

// activate focuses tag and presses Enter (keyboard activation).
func (hs *harness) activate(tag event.Tag, f func(gtx layout.Context)) {
	hs.gtx.Execute(key.FocusCmd{Tag: tag})
	hs.frame(f)
	hs.r.Queue(key.Event{Name: key.NameReturn, State: key.Press}, key.Event{Name: key.NameReturn, State: key.Release})
}

var lth = theme.New(theme.Light())

func TestSwitchToggles(t *testing.T) {
	hs := newHarness()
	var b widget.Bool
	f := func(gtx layout.Context) { Switch(lth, &b, "Resume on startup")(gtx) }
	hs.frame(f)
	hs.activate(&b, f)
	hs.frame(f)
	if !b.Value {
		t.Fatal("switch not toggled on")
	}
	hs.activate(&b, f)
	hs.frame(f)
	if b.Value {
		t.Fatal("switch not toggled off")
	}
}

func TestSegmentedSelects(t *testing.T) {
	hs := newHarness()
	e := widget.Enum{Value: "system"}
	opts := [][2]string{{"system", "System"}, {"light", "Light"}, {"dark", "Dark"}}
	var d layout.Dimensions
	f := func(gtx layout.Context) {
		gtx.Constraints.Min = image.Point{}
		d = Segmented(lth, &e, opts)(gtx)
	}
	hs.frame(f)
	hs.frame(f)                                            // Enum registers per-key filters from its second frame on
	at := f32.Pt(float32(d.Size.X-6), float32(d.Size.Y/2)) // inside the last segment
	hs.r.Queue(pointer.Event{Kind: pointer.Press, Position: at, Buttons: pointer.ButtonPrimary},
		pointer.Event{Kind: pointer.Release, Position: at, Buttons: pointer.ButtonPrimary})
	hs.frame(f)
	if e.Value != "dark" {
		t.Fatalf("Value = %q, want dark", e.Value)
	}
}

func TestStepperClamps(t *testing.T) {
	hs := newHarness()
	s := StepperState{Value: 9}
	f := func(gtx layout.Context) { Stepper(lth, &s, 1, 10)(gtx) }
	hs.frame(f)
	for range 3 {
		hs.activate(&s.Inc, f)
		hs.frame(f)
	}
	if s.Value != 10 {
		t.Fatalf("after inc Value = %d, want 10", s.Value)
	}
	for range 12 {
		hs.activate(&s.Dec, f)
		hs.frame(f)
	}
	if s.Value != 1 {
		t.Fatalf("after dec Value = %d, want 1", s.Value)
	}
	s.Value = 42
	hs.frame(f)
	if s.Value != 10 {
		t.Fatalf("out-of-range Value not clamped: %d", s.Value)
	}
}

func menuItems() []MenuItem {
	return []MenuItem{{Key: "open", Label: "Open file", Icon: icons.File}, {Key: "copy", Label: "Copy link"},
		{Key: "remove", Label: "Remove…", Icon: icons.Close, Danger: true}}
}

func TestMenuPick(t *testing.T) {
	hs := newHarness()
	m := &MenuState{Open: true, Anchor: image.Pt(100, 100), Items: menuItems()}
	var picked string
	f := func(gtx layout.Context) {
		if p := m.Layout(gtx, lth); p != "" {
			picked = p
		}
	}
	hs.frame(f)
	hs.activate(&m.clicks[1], f)
	hs.frame(f)
	if picked != "copy" {
		t.Fatalf("picked %q, want copy", picked)
	}
	if m.Open {
		t.Fatal("menu still open after pick")
	}
}

func TestMenuEscCloses(t *testing.T) {
	hs := newHarness()
	m := &MenuState{Open: true, Anchor: image.Pt(100, 100), Items: menuItems()}
	f := func(gtx layout.Context) { m.Layout(gtx, lth) }
	hs.frame(f)
	hs.r.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	hs.frame(f)
	if m.Open {
		t.Fatal("Esc did not close menu")
	}
}

func TestMenuClickOutsideCloses(t *testing.T) {
	hs := newHarness()
	m := &MenuState{Open: true, Anchor: image.Pt(100, 100), Items: menuItems()}
	f := func(gtx layout.Context) { m.Layout(gtx, lth) }
	hs.frame(f)
	hs.r.Queue(pointer.Event{Kind: pointer.Press, Position: f32.Pt(700, 550), Buttons: pointer.ButtonPrimary})
	hs.frame(f)
	if m.Open {
		t.Fatal("click outside did not close menu")
	}
}

func TestMenuClampsToWindow(t *testing.T) {
	hs := newHarness()
	m := &MenuState{Open: true, Anchor: image.Pt(795, 595), Items: menuItems()}
	hs.frame(func(gtx layout.Context) { m.Layout(gtx, lth) })
	win := image.Rect(0, 0, 800, 600)
	if m.rect.Empty() || !m.rect.In(win) {
		t.Fatalf("menu rect %v not inside %v", m.rect, win)
	}
}

func TestButtonDisabledDims(t *testing.T) {
	if got := disabledAlpha; got < 0.39 || got > 0.41 {
		t.Fatalf("disabledAlpha = %v, want 0.4", got)
	}
	hs := newHarness()
	var c widget.Clickable
	var en, dis float32
	hs.frame(func(gtx layout.Context) { Button(lth, &c, "Add", Primary)(gtx); en = lastButtonOpacity })
	hs.frame(func(gtx layout.Context) {
		gtx = gtx.Disabled()
		Button(lth, &c, "Add", Primary)(gtx)
		dis = lastButtonOpacity
	})
	if en != 1 || dis != disabledAlpha {
		t.Fatalf("opacity enabled=%v disabled=%v", en, dis)
	}
}

func TestMenuTouchItems(t *testing.T) {
	hs := newHarness()
	m := &MenuState{Open: true, Anchor: image.Pt(100, 100), Items: menuItems()}
	f := func(gtx layout.Context) { m.Layout(gtx, lth) }
	hs.frame(f)
	h := m.rect.Dy()
	m.Touch = true
	hs.frame(f)
	if got := m.rect.Dy() - h; got != 3*(44-30) {
		t.Fatalf("touch menu grew by %d px, want %d", got, 3*(44-30))
	}
}
