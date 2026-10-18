package components

import (
	"image"
	"strconv"
	"strings"

	"gioui.org/font"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/raptordnj/goti/internal/ui/icons"
	"github.com/raptordnj/goti/internal/ui/theme"
)

const sectionHeight unit.Dp = 28

// SectionHeader is a 28-dp list section header: uppercase muted caption and
// a count pill. A non-nil collapsed makes it a toggle with a chevron
// (Value true = collapsed).
func SectionHeader(th *theme.Theme, title string, count int, collapsed *widget.Bool) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X, gtx.Dp(sectionHeight)))
		body := func(gtx layout.Context) layout.Dimensions {
			return midLeft(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: theme.S1}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return sectionRow(gtx, th, title, count, collapsed)
				})
			})
		}
		if collapsed == nil {
			body(gtx)
			return layout.Dimensions{Size: gtx.Constraints.Min}
		}
		return FocusRing(gtx, th, collapsed, theme.RadiusControl, func(gtx layout.Context) layout.Dimensions {
			return collapsed.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				pointer.CursorPointer.Add(gtx.Ops)
				body(gtx)
				return layout.Dimensions{Size: gtx.Constraints.Min}
			})
		})
	}
}

func sectionRow(gtx layout.Context, th *theme.Theme, title string, count int, collapsed *widget.Bool) layout.Dimensions {
	var children []layout.FlexChild
	if collapsed != nil {
		ic := icons.ChevronDown
		if collapsed.Value {
			ic = icons.ChevronRight
		}
		children = append(children,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return ic(gtx, 12, th.TextMuted) }),
			layout.Rigid(layout.Spacer{Width: theme.S1 + 2}.Layout))
	}
	l := styled(th, theme.StyleCaption, th.TextMuted, strings.ToUpper(title))
	l.Font.Weight = font.SemiBold // Inter ships no Bold; SemiBold is the heaviest face
	children = append(children,
		layout.Rigid(l.Layout),
		layout.Rigid(layout.Spacer{Width: theme.S2}.Layout),
		layout.Rigid(background(th.SurfaceSunken, 8, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Left: 6, Right: 6, Top: 1, Bottom: 1}.Layout(gtx,
				styled(th, theme.StyleCaption, th.TextMuted, strconv.Itoa(count)).Layout)
		})),
	)
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
}
