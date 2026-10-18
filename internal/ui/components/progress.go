package components

import (
	"image"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"

	"github.com/raptordnj/swiftfetch/internal/ui/theme"
)

// ProgressBar draws a horizontal bar; frac < 0 draws a static striped
// (indeterminate) bar.
func ProgressBar(th *theme.Theme, frac float32) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		h := gtx.Dp(theme.S1 + 2)
		w := gtx.Constraints.Max.X
		if w <= 0 || w == int(^uint(0)>>1) {
			w = gtx.Dp(200)
		}
		sz := image.Pt(w, h)
		r := h / 2
		paint.FillShape(gtx.Ops, th.SurfaceAlt, clip.UniformRRect(image.Rectangle{Max: sz}, r).Op(gtx.Ops))
		if frac < 0 {
			defer clip.UniformRRect(image.Rectangle{Max: sz}, r).Push(gtx.Ops).Pop()
			stripe, gap := gtx.Dp(theme.S4), gtx.Dp(theme.S2)
			for x := 0; x < w; x += stripe + gap {
				rect := image.Rect(x, 0, min(x+stripe, w), h)
				paint.FillShape(gtx.Ops, theme.WithAlpha(th.Accent, 0x99), clip.Rect(rect).Op())
			}
		} else {
			frac = min(frac, 1)
			if fw := int(float32(w) * frac); fw > 0 {
				paint.FillShape(gtx.Ops, th.Accent, clip.UniformRRect(image.Rect(0, 0, fw, h), r).Op(gtx.Ops))
			}
		}
		return layout.Dimensions{Size: sz}
	}
}
