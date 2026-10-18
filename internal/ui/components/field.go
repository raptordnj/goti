package components

import (
	"image/color"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/widget"

	"github.com/raptordnj/swiftfetch/internal/ui/theme"
)

// TextField is a labelled single editor with hint and optional error text.
func TextField(th *theme.Theme, e *widget.Editor, labelText, hint, errText string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		border := th.Border
		if errText != "" {
			border = th.Error
		}
		if gtx.Focused(e) {
			border = th.Focus
		}
		children := []layout.FlexChild{
			layout.Rigid(label(th, theme.TextSm, th.TextMuted, labelText).Layout),
			layout.Rigid(layout.Spacer{Height: theme.S1}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return widget.Border{Color: border, CornerRadius: theme.RadiusSm, Width: 1}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return background(th.Surface, theme.RadiusSm, func(gtx layout.Context) layout.Dimensions {
						return layout.UniformInset(theme.S2).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							gtx.Constraints.Min.X = gtx.Constraints.Max.X
							return editor(gtx, th, e, hint)
						})
					})(gtx)
				})
			}),
		}
		if errText != "" {
			children = append(children,
				layout.Rigid(layout.Spacer{Height: theme.S1}.Layout),
				layout.Rigid(label(th, theme.TextSm, th.Error, errText).Layout))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	}
}

func editor(gtx layout.Context, th *theme.Theme, e *widget.Editor, hint string) layout.Dimensions {
	if e.Len() == 0 {
		macro := op.Record(gtx.Ops)
		label(th, theme.TextMd, th.TextMuted, hint).Layout(gtx)
		hintCall := macro.Stop()
		defer hintCall.Add(gtx.Ops)
	}
	textMat := colorMacro(gtx, th.Text)
	selMat := colorMacro(gtx, theme.WithAlpha(th.Accent, 0x60))
	return e.Layout(gtx, th.Shaper, font.Font{}, theme.TextMd, textMat, selMat)
}

func colorMacro(gtx layout.Context, c color.NRGBA) op.CallOp {
	m := op.Record(gtx.Ops)
	paint.ColorOp{Color: c}.Add(gtx.Ops)
	return m.Stop()
}
