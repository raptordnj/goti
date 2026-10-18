package screens

import (
	"gioui.org/layout"
	"gioui.org/widget"

	"github.com/raptordnj/swiftfetch/internal/ui/components"
	"github.com/raptordnj/swiftfetch/internal/ui/theme"
)

// Fixed copy for empty and offline states.
const (
	EmptyTitle  = "No downloads yet"
	EmptyHint   = "Ctrl+V to paste a link"
	OfflineText = "You're offline. Downloads resume when the network is back."
)

// emptyState is the centered "No downloads yet" block with an Add button.
func emptyState(th *theme.Theme, add *widget.Clickable) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(Label(th, theme.Title, th.Text, EmptyTitle)),
				layout.Rigid(layout.Spacer{Height: theme.S3}.Layout),
				layout.Rigid(components.Button(th, add, "Add download", components.Primary)),
				layout.Rigid(layout.Spacer{Height: theme.S2}.Layout),
				layout.Rigid(Label(th, theme.Caption, th.TextMuted, EmptyHint)),
			)
		})
	}
}

// offlineBanner is the strip shown while Snapshot.Offline.
func offlineBanner(th *theme.Theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Background{}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return fill(gtx, theme.WithAlpha(th.Warning, 0x22))
		}, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: theme.S2, Bottom: theme.S2, Left: theme.S4, Right: theme.S4}.Layout(gtx,
				Text(th, theme.Body, th.Text, OfflineText, 2))
		})
	}
}
