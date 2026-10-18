// Package components holds SwiftFetch's reusable Gio widgets.
package components

import (
	"image"
	"image/color"
	"sync"

	"gioui.org/io/event"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/raptordnj/swiftfetch/internal/ui/anim"
	"github.com/raptordnj/swiftfetch/internal/ui/theme"
)

// FocusRing lays out w and, when tag is focused, draws a 2-dp Focus ring
// offset 2 dp outside it.
func FocusRing(gtx layout.Context, th *theme.Theme, tag event.Tag, radius unit.Dp, w layout.Widget) layout.Dimensions {
	dims := w(gtx)
	if tag != nil && gtx.Focused(tag) {
		ring(gtx, th.Focus, dims.Size, radius)
	}
	return dims
}

// ring strokes a 2-dp ring 2 dp outside a box of size sz.
func ring(gtx layout.Context, c color.NRGBA, sz image.Point, radius unit.Dp) {
	o := gtx.Dp(4)
	defer op.Offset(image.Pt(-o, -o)).Push(gtx.Ops).Pop()
	widget.Border{Color: c, CornerRadius: radius + 4, Width: 2}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Dimensions{Size: sz.Add(image.Pt(2*o, 2*o))}
	})
}

// motion holds eased per-widget state values keyed by widget pointer.
// Widgets live as long as the window, so the map stays small.
var motion = struct {
	sync.Mutex
	m map[any]*anim.Value
}{m: map[any]*anim.Value{}}

// eased returns the eased value for key heading toward target.
func eased(gtx layout.Context, key any, target float32) float32 {
	motion.Lock()
	v, ok := motion.m[key]
	if !ok {
		v = new(anim.Value)
		motion.m[key] = v
	}
	motion.Unlock()
	v.Set(target)
	return v.Get(gtx)
}

// fillRRect paints a rounded rectangle of size sz.
func fillRRect(gtx layout.Context, c color.NRGBA, sz image.Point, r unit.Dp) {
	paint.FillShape(gtx.Ops, c, clip.UniformRRect(image.Rectangle{Max: sz}, gtx.Dp(r)).Op(gtx.Ops))
}

// background lays out w over a rounded rect of color c.
func background(c color.NRGBA, r unit.Dp, w layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Background{}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			fillRRect(gtx, c, gtx.Constraints.Min, r)
			return layout.Dimensions{Size: gtx.Constraints.Min}
		}, w)
	}
}

// label returns a single-line text label.
func label(th *theme.Theme, size unit.Sp, c color.NRGBA, txt string) material.LabelStyle {
	return material.LabelStyle{Text: txt, Color: c, TextSize: size, Shaper: th.Shaper, MaxLines: 1}
}

// styled returns a single-line label in text style s.
func styled(th *theme.Theme, s theme.TextStyle, c color.NRGBA, txt string) material.LabelStyle {
	l := label(th, s.Size, c, txt)
	l.Font.Weight = s.Weight
	return l
}

// isDark reports whether th is a dark palette.
func isDark(th *theme.Theme) bool {
	b := th.Bg
	return int(b.R)+int(b.G)+int(b.B) < 3*0x80
}

// fixed constrains gtx to exactly w x h dp.
func fixed(gtx layout.Context, w, h unit.Dp) layout.Context {
	gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(w), gtx.Dp(h)))
	return gtx
}

// midLeft places w vertically centred at the left edge of the minimum
// constraints (unlike layout.W it also frees w's minimum height).
func midLeft(gtx layout.Context, w layout.Widget) layout.Dimensions {
	return layout.W.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.Y = 0
		return w(gtx)
	})
}
