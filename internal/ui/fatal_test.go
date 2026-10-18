package ui

import (
	"image"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/raptordnj/goti/internal/ui/theme"
)

func TestFatalLayoutQuit(t *testing.T) {
	th := theme.New(theme.Light())
	var quit widget.Clickable
	var r input.Router
	frame := func() bool {
		gtx := layout.Context{Ops: new(op.Ops), Constraints: layout.Exact(image.Pt(480, 240)),
			Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Now: time.Unix(1000, 0), Source: r.Source()}
		clicked := fatalLayout(gtx, th, &quit, "Goti is already running.")
		r.Frame(gtx.Ops)
		return clicked
	}
	if frame() {
		t.Fatal("clicked before any input")
	}
	// Click every point on a grid until the Quit button reports a click.
	for y := 0; y < 240; y += 8 {
		for x := 0; x < 480; x += 8 {
			p := image.Pt(x, y)
			r.Queue(pointer.Event{Kind: pointer.Press, Position: pt(p), Buttons: pointer.ButtonPrimary, Source: pointer.Mouse},
				pointer.Event{Kind: pointer.Release, Position: pt(p), Source: pointer.Mouse})
			if frame() {
				return
			}
		}
	}
	t.Fatal("Quit button never clicked")
}

func pt(p image.Point) f32.Point { return f32.Point{X: float32(p.X), Y: float32(p.Y)} }
