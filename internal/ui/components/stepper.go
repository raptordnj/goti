package components

import (
	"image"
	"image/color"
	"strconv"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/raptordnj/swiftfetch/internal/ui/icons"
	"github.com/raptordnj/swiftfetch/internal/ui/theme"
)

// StepperState is the state of a numeric stepper.
type StepperState struct {
	Dec, Inc widget.Clickable
	Value    int
}

// Update applies pending clicks and clamps Value to [lo, hi]; it reports
// whether Value changed.
func (s *StepperState) Update(gtx layout.Context, lo, hi int) bool {
	old := s.Value
	for s.Dec.Clicked(gtx) {
		s.Value--
	}
	for s.Inc.Clicked(gtx) {
		s.Value++
	}
	s.Value = min(max(s.Value, lo), hi)
	return s.Value != old
}

// Stepper is a "− value +" control clamped to [lo, hi].
func Stepper(th *theme.Theme, s *StepperState, lo, hi int) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		s.Update(gtx, lo, hi)
		return background(th.SurfaceSunken, theme.RadiusControl, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(stepBtn(th, &s.Dec, s.Value > lo, minus, "Decrease")),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min = image.Pt(gtx.Dp(36), gtx.Dp(28))
					return layout.Center.Layout(gtx, styled(th, theme.StyleBodyStrong, th.Text, strconv.Itoa(s.Value)).Layout)
				}),
				layout.Rigid(stepBtn(th, &s.Inc, s.Value < hi, icons.Plus, "Increase")),
			)
		})(gtx)
	}
}

func stepBtn(th *theme.Theme, c *widget.Clickable, enabled bool, ic icons.Icon, desc string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		if !enabled {
			gtx = gtx.Disabled()
		}
		return IconButton(th, c, ic, desc)(gtx)
	}
}

// minus is a horizontal stroke icon matching icons.Plus.
func minus(gtx layout.Context, size unit.Dp, c color.NRGBA) layout.Dimensions {
	px := float32(gtx.Dp(size))
	t := max(px*1.5/16, 1)
	r := image.Rect(int(px*3/16), int((px-t)/2), int(px*13/16+0.5), int((px+t)/2+0.5))
	paint.FillShape(gtx.Ops, c, clip.UniformRRect(r, int(t/2)).Op(gtx.Ops))
	return layout.Dimensions{Size: image.Pt(int(px), int(px))}
}
