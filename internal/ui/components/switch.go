package components

import (
	"image"
	"image/color"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget"

	"github.com/raptordnj/goti/internal/ui/theme"
)

// Switch is a full-width row: label on the left, a 32x18-dp toggle on the
// right whose knob slides (eased) between off and on.
func Switch(th *theme.Theme, b *widget.Bool, labelText string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		if !gtx.Enabled() {
			defer paint.PushOpacity(gtx.Ops, disabledAlpha).Pop()
		}
		return b.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			if gtx.Enabled() {
				pointer.CursorPointer.Add(gtx.Ops)
			}
			// Both children fill the 32-dp row and centre their content in it,
			// so the label lines up with the toggle.
			rowH := gtx.Dp(32)
			return layout.Flex{}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.Y = rowH
					return midLeft(gtx, styled(th, theme.StyleBody, th.Text, labelText).Layout)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.Y = rowH
					return layout.E.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return FocusRing(gtx, th, b, 9, func(gtx layout.Context) layout.Dimensions {
							return toggle(gtx, th, b)
						})
					})
				}),
			)
		})
	}
}

func toggle(gtx layout.Context, th *theme.Theme, b *widget.Bool) layout.Dimensions {
	t := eased(gtx, b, b2f(b.Value))
	w, h := gtx.Dp(32), gtx.Dp(18)
	track := theme.Mix(th.Hairline, th.Accent, t)
	if b.Hovered() {
		track = theme.Mix(track, th.Text, 0.06)
	}
	paint.FillShape(gtx.Ops, track, clip.UniformRRect(image.Rect(0, 0, w, h), h/2).Op(gtx.Ops))
	k, pad := gtx.Dp(14), gtx.Dp(2)
	x := pad + int(float32(w-k-2*pad)*t)
	knob := image.Rect(x, pad, x+k, pad+k)
	shadow := knob.Add(image.Pt(0, 1))
	paint.FillShape(gtx.Ops, color.NRGBA{A: 0x30}, clip.Ellipse(shadow).Op(gtx.Ops))
	paint.FillShape(gtx.Ops, color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}, clip.Ellipse(knob).Op(gtx.Ops))
	return layout.Dimensions{Size: image.Pt(w, h)}
}
