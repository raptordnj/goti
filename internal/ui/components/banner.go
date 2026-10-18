package components

import (
	"image/color"

	"gioui.org/layout"

	"github.com/raptordnj/swiftfetch/internal/ui/icons"
	"github.com/raptordnj/swiftfetch/internal/ui/theme"
)

// Banner is a slim 32-dp full-width bar tinted with col, with a leading icon.
func Banner(th *theme.Theme, icon icons.Icon, text string, col color.NRGBA) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		gtx.Constraints.Min.Y = gtx.Dp(32)
		return layout.Background{}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			sz := gtx.Constraints.Min
			fillRRect(gtx, theme.WithAlpha(col, 0x1F), sz, 0)
			return layout.Dimensions{Size: sz}
		}, func(gtx layout.Context) layout.Dimensions {
			return midLeft(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: theme.S4, Right: theme.S4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if icon == nil {
								return layout.Dimensions{}
							}
							return icon(gtx, 14, col)
						}),
						layout.Rigid(layout.Spacer{Width: theme.S2}.Layout),
						layout.Rigid(styled(th, theme.StyleBody, th.Text, text).Layout),
					)
				})
			})
		})
	}
}
