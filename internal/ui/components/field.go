package components

import (
	"image/color"

	"gioui.org/font"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/widget"

	"github.com/raptordnj/goti/internal/platform"
	"github.com/raptordnj/goti/internal/ui/icons"
	"github.com/raptordnj/goti/internal/ui/theme"
)

// TextField is a labelled 32-dp single-line editor on a sunken fill with a
// hairline border (Accent when focused, Error on error) and inline error text.
func TextField(th *theme.Theme, e *widget.Editor, labelText, hint, errText string) layout.Widget {
	return textField(th, e, labelText, hint, errText, "", errText != "")
}

// UnitField is an unlabelled TextField with a muted unit suffix (e.g.
// "KB/s") drawn inside the field's trailing edge.
// invalid draws the error border; the caller shows the message.
func UnitField(th *theme.Theme, e *widget.Editor, suffix, hint string, invalid bool) layout.Widget {
	return textField(th, e, "", hint, "", suffix, invalid)
}

// PathLabel draws path on one line, middle-ellipsized to the available
// width so both the root and the leaf folder stay visible.
func PathLabel(th *theme.Theme, st theme.TextStyle, c color.NRGBA, path string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		maxW := gtx.Constraints.Max.X
		fits := func(s string) bool { return measure(gtx, styled(th, st, c, s)) <= maxW }
		txt := path
		if !fits(path) {
			lo, hi := 4, len([]rune(path))-1
			txt = platform.MiddleEllipsis(path, lo)
			for lo <= hi {
				mid := (lo + hi) / 2
				if s := platform.MiddleEllipsis(path, mid); fits(s) {
					txt, lo = s, mid+1
				} else {
					hi = mid - 1
				}
			}
		}
		l := styled(th, st, c, txt)
		l.MaxLines = 1
		return l.Layout(gtx)
	}
}

func textField(th *theme.Theme, e *widget.Editor, labelText, hint, errText, suffix string, invalid bool) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		border := th.Hairline
		if invalid {
			border = th.Error
		}
		if gtx.Focused(e) {
			border = th.Accent
		}
		children := []layout.FlexChild{}
		if labelText != "" {
			children = append(children,
				layout.Rigid(styled(th, theme.StyleCaption, th.TextMuted, labelText).Layout),
				layout.Rigid(layout.Spacer{Height: theme.S1 + 2}.Layout))
		}
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.Y = gtx.Dp(32)
			return layout.Background{}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				sz := gtx.Constraints.Min
				fillRRect(gtx, th.SurfaceSunken, sz, theme.RadiusRow)
				widget.Border{Color: border, CornerRadius: theme.RadiusRow, Width: 1}.Layout(gtx, func(layout.Context) layout.Dimensions {
					return layout.Dimensions{Size: sz}
				})
				return layout.Dimensions{Size: sz}
			}, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return midLeft(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: theme.S3 - 2, Right: theme.S3 - 2}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = gtx.Constraints.Max.X
						ed := func(gtx layout.Context) layout.Dimensions {
							gtx.Constraints.Min.X = gtx.Constraints.Max.X
							pointer.CursorText.Add(gtx.Ops)
							return editor(gtx, th, e, hint)
						}
						if suffix == "" {
							return ed(gtx)
						}
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Flexed(1, ed),
							layout.Rigid(layout.Spacer{Width: theme.S2}.Layout),
							layout.Rigid(styled(th, theme.StyleCaption, th.TextMuted, suffix).Layout),
						)
					})
				})
			})
		}))
		if errText != "" {
			children = append(children,
				layout.Rigid(layout.Spacer{Height: theme.S1 + 2}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return icons.Alert(gtx, 13, th.Error) }),
						layout.Rigid(layout.Spacer{Width: theme.S1 + 1}.Layout),
						layout.Rigid(styled(th, theme.StyleCaption, th.Error, errText).Layout),
					)
				}))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	}
}

func editor(gtx layout.Context, th *theme.Theme, e *widget.Editor, hint string) layout.Dimensions {
	if e.Len() == 0 {
		macro := op.Record(gtx.Ops)
		label(th, theme.Body, th.TextFaint, hint).Layout(gtx)
		hintCall := macro.Stop()
		defer hintCall.Add(gtx.Ops)
	}
	textMat := colorMacro(gtx, th.Text)
	selMat := colorMacro(gtx, theme.WithAlpha(th.Accent, 0x50))
	return e.Layout(gtx, th.Shaper, font.Font{}, theme.Body, textMat, selMat)
}

func colorMacro(gtx layout.Context, c color.NRGBA) op.CallOp {
	m := op.Record(gtx.Ops)
	paint.ColorOp{Color: c}.Add(gtx.Ops)
	return m.Stop()
}
