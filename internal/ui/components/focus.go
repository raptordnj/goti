// Package components holds SwiftFetch's reusable Gio widgets.
package components

import (
	"image"
	"image/color"

	"gioui.org/io/event"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/raptordnj/swiftfetch/internal/ui/theme"
)

// FocusRing lays out w and draws a focus ring around it when tag is focused.
func FocusRing(gtx layout.Context, th *theme.Theme, tag event.Tag, radius unit.Dp, w layout.Widget) layout.Dimensions {
	dims := w(gtx)
	if tag != nil && gtx.Focused(tag) {
		widget.Border{Color: th.Focus, CornerRadius: radius, Width: 2}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Dimensions{Size: dims.Size}
		})
	}
	return dims
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
