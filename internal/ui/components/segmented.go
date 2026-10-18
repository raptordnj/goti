package components

import (
	"image"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/widget"

	"github.com/raptordnj/goti/internal/ui/theme"
)

// Segmented is a compact segmented control; options are {key, label}
// pairs and e.Value holds the selected key.
func Segmented(th *theme.Theme, e *widget.Enum, options [][2]string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		e.Update(gtx)
		hovered, isHover := e.Hovered()
		children := make([]layout.FlexChild, len(options))
		for i, o := range options {
			key, txt := o[0], o[1]
			children[i] = layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return e.Layout(gtx, key, func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					sel := e.Value == key
					fg, st := th.TextMuted, theme.StyleBody
					if sel {
						fg, st = th.Text, theme.StyleBodyStrong
					} else if isHover && hovered == key {
						fg = th.Text
					}
					m := styled(th, st, fg, txt)
					return layout.Background{}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						sz := gtx.Constraints.Min
						if sel {
							Raised(gtx, th, theme.RadiusControl-2, func(gtx layout.Context) layout.Dimensions {
								fillRRect(gtx, th.Surface, sz, theme.RadiusControl-2)
								return layout.Dimensions{Size: sz}
							})
						}
						if f, ok := e.Focused(); ok && f == key {
							ring(gtx, th.Focus, sz, theme.RadiusControl-2)
						}
						return layout.Dimensions{Size: sz}
					}, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min = image.Pt(gtx.Dp(56), gtx.Dp(24))
						return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Left: theme.S3, Right: theme.S3}.Layout(gtx, m.Layout)
						})
					})
				})
			})
		}
		return background(th.SurfaceSunken, theme.RadiusControl, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(theme.S0).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{}.Layout(gtx, children...)
			})
		})(gtx)
	}
}
