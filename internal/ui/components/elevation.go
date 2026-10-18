package components

import (
	"image"
	"image/color"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"

	"github.com/raptordnj/goti/internal/ui/theme"
)

type shadowLayer struct {
	spread, dy unit.Dp
	alpha      uint8
}

var (
	raisedShadow  = []shadowLayer{{3, 2, 0x08}, {2, 1, 0x0C}, {1, 0, 0x10}}
	overlayShadow = []shadowLayer{{16, 10, 0x08}, {8, 5, 0x0E}, {2, 1, 0x14}}
)

// Raised lays out w with a small shadow (menus, toasts).
func Raised(gtx layout.Context, th *theme.Theme, radius unit.Dp, w layout.Widget) layout.Dimensions {
	return elevate(gtx, th, radius, raisedShadow, w)
}

// Overlay lays out w with a deep shadow (dialogs).
func Overlay(gtx layout.Context, th *theme.Theme, radius unit.Dp, w layout.Widget) layout.Dimensions {
	return elevate(gtx, th, radius, overlayShadow, w)
}

// elevate draws stacked translucent rrects behind w's bounds.
func elevate(gtx layout.Context, th *theme.Theme, radius unit.Dp, layers []shadowLayer, w layout.Widget) layout.Dimensions {
	m := op.Record(gtx.Ops)
	dims := w(gtx)
	call := m.Stop()
	k := 1
	if isDark(th) {
		k = 3 // shadows need more weight on dark backgrounds
	}
	for _, l := range layers {
		s, dy := gtx.Dp(l.spread), gtx.Dp(l.dy)
		r := image.Rect(-s, -s+dy, dims.Size.X+s, dims.Size.Y+s+dy)
		a := min(int(l.alpha)*k, 0xFF)
		paint.FillShape(gtx.Ops, color.NRGBA{A: uint8(a)}, clip.UniformRRect(r, gtx.Dp(radius)+s).Op(gtx.Ops))
	}
	call.Add(gtx.Ops)
	return dims
}
