package components

import (
	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/widget"

	"github.com/raptordnj/swiftfetch/internal/ui/theme"
)

// DialogShell renders a modal card with a title, body and right-aligned actions.
func DialogShell(th *theme.Theme, title string, body, actions layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return widget.Border{Color: th.Hairline, CornerRadius: theme.RadiusRow, Width: 1}.Layout(gtx, background(th.Surface, theme.RadiusRow, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(theme.S6).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						l := label(th, theme.Display, th.Text, title)
						l.Font.Weight = font.SemiBold
						return l.Layout(gtx)
					}),
					layout.Rigid(layout.Spacer{Height: theme.S4}.Layout),
					layout.Rigid(body),
					layout.Rigid(layout.Spacer{Height: theme.S6}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.E.Layout(gtx, actions)
					}),
				)
			})
		}))
	}
}
