package components

import (
	"strconv"

	"gioui.org/font"
	"gioui.org/layout"

	"github.com/raptordnj/swiftfetch/internal/ui/theme"
)

// SectionHeader shows a list section title with its task count.
func SectionHeader(th *theme.Theme, title string, count int) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: theme.S4, Bottom: theme.S2, Left: theme.S4, Right: theme.S4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					l := label(th, theme.TextSm, th.TextMuted, title)
					l.Font.Weight = font.Bold
					return l.Layout(gtx)
				}),
				layout.Rigid(layout.Spacer{Width: theme.S2}.Layout),
				layout.Rigid(label(th, theme.TextSm, th.TextMuted, strconv.Itoa(count)).Layout),
			)
		})
	}
}
