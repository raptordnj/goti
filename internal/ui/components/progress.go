package components

import (
	"image"
	"image/color"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"

	"github.com/raptordnj/goti/internal/ui/anim"
	"github.com/raptordnj/goti/internal/ui/theme"
)

// indetLoop is the period of the indeterminate segment's slide.
const indetLoop = 1200 * time.Millisecond

// ProgressBar draws a 3-dp pill bar on a Hairline track. The fill eases via
// v (nil: no easing); frac < 0 draws a sliding 30 % segment that animates
// only while laid out and motion is not reduced.
func ProgressBar(th *theme.Theme, v *anim.Value, frac float32, col color.NRGBA) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		h := gtx.Dp(3)
		w := gtx.Constraints.Max.X
		if w <= 0 || w == int(^uint(0)>>1) {
			w = gtx.Dp(200)
		}
		sz := image.Pt(w, h)
		r := h / 2
		paint.FillShape(gtx.Ops, th.Hairline, clip.UniformRRect(image.Rectangle{Max: sz}, r).Op(gtx.Ops))
		if frac < 0 {
			seg := w * 3 / 10
			x := (w - seg) / 2
			if !anim.Reduce() {
				ph := float32(gtx.Now.UnixNano()%int64(indetLoop)) / float32(indetLoop)
				x = int(ph*float32(w+seg)) - seg
				gtx.Execute(op.InvalidateCmd{})
			}
			defer clip.UniformRRect(image.Rectangle{Max: sz}, r).Push(gtx.Ops).Pop()
			paint.FillShape(gtx.Ops, col, clip.UniformRRect(image.Rect(x, 0, x+seg, h), r).Op(gtx.Ops))
			return layout.Dimensions{Size: sz}
		}
		frac = min(frac, 1)
		if v != nil {
			v.Set(frac)
			frac = v.Get(gtx)
		}
		if fw := int(float32(w) * frac); fw > 0 {
			paint.FillShape(gtx.Ops, col, clip.UniformRRect(image.Rect(0, 0, max(fw, h), h), r).Op(gtx.Ops))
		}
		return layout.Dimensions{Size: sz}
	}
}
