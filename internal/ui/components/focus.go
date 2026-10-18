// Package components holds Goti's reusable Gio widgets.
package components

import (
	"image"
	"image/color"
	"sync"
	"time"

	"gioui.org/io/event"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/raptordnj/goti/internal/ui/anim"
	"github.com/raptordnj/goti/internal/ui/theme"
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
// Entries not used for motionTTL are swept so widgets of vanished rows,
// dialogs and so on do not accumulate.
var motion = struct {
	sync.Mutex
	m         map[any]*motionEntry
	lastSweep time.Time
}{m: map[any]*motionEntry{}}

type motionEntry struct {
	v    anim.Value
	used time.Time
}

const motionTTL = 5 * time.Second

// eased returns the eased value for key heading toward target.
func eased(gtx layout.Context, key any, target float32) float32 {
	motion.Lock()
	e, ok := motion.m[key]
	if !ok {
		e = new(motionEntry)
		motion.m[key] = e
	}
	e.used = gtx.Now
	if gtx.Now.Sub(motion.lastSweep) > time.Second || gtx.Now.Before(motion.lastSweep) {
		motion.lastSweep = gtx.Now
		for k, o := range motion.m {
			if d := gtx.Now.Sub(o.used); d > motionTTL || d < 0 {
				delete(motion.m, k)
			}
		}
	}
	motion.Unlock()
	e.v.Set(target)
	return e.v.Get(gtx)
}

// motionLen reports the number of tracked eased values (tests).
func motionLen() int {
	motion.Lock()
	defer motion.Unlock()
	return len(motion.m)
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

// RoundedFill paints an sz rounded rect of radius r and returns sz.
func RoundedFill(gtx layout.Context, c color.NRGBA, sz image.Point, r unit.Dp) layout.Dimensions {
	fillRRect(gtx, c, sz, r)
	return layout.Dimensions{Size: sz}
}
