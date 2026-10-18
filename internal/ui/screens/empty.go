package screens

import (
	"image"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/raptordnj/goti/internal/brand"
	"github.com/raptordnj/goti/internal/ui/components"
	"github.com/raptordnj/goti/internal/ui/icons"
	"github.com/raptordnj/goti/internal/ui/theme"
)

// Fixed copy for empty and offline states.
const (
	EmptyTitle  = "No downloads yet"
	EmptyHint   = "Paste a link with Ctrl+V or click Add"
	OfflineText = "You're offline — downloads resume automatically"
)

// emptyTile is the size of the empty state's icon tile.
const emptyTile unit.Dp = 48

// layoutEmpty draws the empty-state stack centred on the whole window (not
// just the area under the header) and returns its window bounds.
func layoutEmpty(gtx layout.Context, th *theme.Theme, add *widget.Clickable) image.Rectangle {
	full := gtx.Constraints.Max
	gtx.Constraints.Min = image.Point{}
	m := op.Record(gtx.Ops)
	d := emptyStack(th, add)(gtx)
	call := m.Stop()
	off := image.Pt((full.X-d.Size.X)/2, (full.Y-d.Size.Y)/2)
	st := op.Offset(off).Push(gtx.Ops)
	call.Add(gtx.Ops)
	st.Pop()
	return image.Rectangle{Min: off, Max: off.Add(d.Size)}
}

// emptyStack is one calm centred column: icon tile, brand, copy, action.
func emptyStack(th *theme.Theme, add *widget.Clickable) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				sz := image.Pt(gtx.Dp(emptyTile), gtx.Dp(emptyTile))
				paint.FillShape(gtx.Ops, th.AccentSoft, clip.UniformRRect(image.Rectangle{Max: sz}, gtx.Dp(theme.RadiusPanel)).Op(gtx.Ops))
				gtx.Constraints = layout.Exact(sz)
				layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return icons.ArrowDown(gtx, 24, th.Accent) })
				return layout.Dimensions{Size: sz}
			}),
			layout.Rigid(layout.Spacer{Height: theme.S4}.Layout),
			layout.Rigid(bold(th, theme.Display, th.Text, brand.Name)),
			layout.Rigid(Label(th, theme.Body, th.TextMuted, brand.Wordmark)),
			layout.Rigid(layout.Spacer{Height: theme.S1}.Layout),
			layout.Rigid(Label(th, theme.Body, th.TextMuted, brand.Tagline)),
			layout.Rigid(layout.Spacer{Height: theme.S6}.Layout),
			layout.Rigid(bold(th, theme.Title, th.Text, EmptyTitle)),
			layout.Rigid(layout.Spacer{Height: theme.S1}.Layout),
			layout.Rigid(Label(th, theme.Body, th.TextMuted, EmptyHint)),
			layout.Rigid(layout.Spacer{Height: theme.S4}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				Note("Add download")
				return components.Button(th, add, "Add download", components.Primary, components.WithIcon(icons.Plus))(gtx)
			}),
		)
	}
}

// offlineBanner is the slim Warning strip shown while Snapshot.Offline.
func offlineBanner(th *theme.Theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		Note(OfflineText)
		return components.Banner(th, icons.WifiOff, OfflineText, th.Warning)(gtx)
	}
}
