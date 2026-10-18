package components

import (
	"image/color"

	"gioui.org/font"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/widget"

	"github.com/raptordnj/swiftfetch/internal/ui/theme"
)

// ButtonKind is the visual variant of a button.
type ButtonKind int

// Button kinds.
const (
	Primary ButtonKind = iota
	Secondary
	Danger
	Ghost
)

func buttonColors(th *theme.Theme, k ButtonKind) (bg, fg color.NRGBA) {
	switch k {
	case Primary:
		return th.Accent, th.OnAccent
	case Danger:
		return th.Surface, th.Error
	case Ghost:
		return theme.WithAlpha(th.Surface, 0), th.Text
	default:
		return th.SurfaceSunken, th.Text
	}
}

// stateColor derives hover/pressed/disabled backgrounds from tokens.
func stateColor(gtx layout.Context, th *theme.Theme, c *widget.Clickable, bg color.NRGBA) color.NRGBA {
	switch {
	case !gtx.Enabled():
		return theme.WithAlpha(bg, bg.A/2)
	case c.Pressed():
		return theme.Mix(bg, th.Text, 0.18)
	case c.Hovered():
		if bg.A == 0 {
			return theme.WithAlpha(th.Text, 0x14)
		}
		return theme.Mix(bg, th.Text, 0.08)
	}
	return bg
}

// Button is a text button.
func Button(th *theme.Theme, c *widget.Clickable, txt string, kind ButtonKind) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		bg, fg := buttonColors(th, kind)
		return FocusRing(gtx, th, c, theme.RadiusControl, func(gtx layout.Context) layout.Dimensions {
			return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return background(stateColor(gtx, th, c, bg), theme.RadiusControl, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: theme.S2, Bottom: theme.S2, Left: theme.S4, Right: theme.S4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						l := label(th, theme.Body, fg, txt)
						l.Font.Weight = font.SemiBold
						return l.Layout(gtx)
					})
				})(gtx)
			})
		})
	}
}

// IconButton is a compact glyph button; desc is its accessible description.
func IconButton(th *theme.Theme, c *widget.Clickable, glyph, desc string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return FocusRing(gtx, th, c, theme.RadiusControl, func(gtx layout.Context) layout.Dimensions {
			return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				semantic.DescriptionOp(desc).Add(gtx.Ops)
				bg := stateColor(gtx, th, c, theme.WithAlpha(th.Surface, 0))
				return background(bg, theme.RadiusControl, func(gtx layout.Context) layout.Dimensions {
					return layout.UniformInset(theme.S2).Layout(gtx, label(th, theme.Body, th.Text, glyph).Layout)
				})(gtx)
			})
		})
	}
}
