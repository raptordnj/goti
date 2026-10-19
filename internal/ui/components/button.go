package components

import (
	"image"
	"image/color"

	"gioui.org/io/pointer"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/raptordnj/goti/internal/ui/icons"
	"github.com/raptordnj/goti/internal/ui/theme"
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

// disabledAlpha is the opacity of a disabled control (fg and bg).
const disabledAlpha float32 = 0.4

// lastButtonOpacity records the opacity of the last laid-out button (tests).
var lastButtonOpacity float32

// ButtonOpt customises a Button.
type ButtonOpt func(*buttonCfg)

type buttonCfg struct {
	icon  icons.Icon
	small bool
}

// WithIcon adds a leading icon.
func WithIcon(i icons.Icon) ButtonOpt { return func(c *buttonCfg) { c.icon = i } }

// Small selects the 28-dp size.
func Small() ButtonOpt { return func(c *buttonCfg) { c.small = true } }

func buttonColors(th *theme.Theme, k ButtonKind) (bg, fg, border color.NRGBA) {
	switch k {
	case Primary:
		return th.Accent, th.OnAccent, color.NRGBA{}
	case Danger:
		return th.Error, th.OnAccent, color.NRGBA{}
	case Ghost:
		return color.NRGBA{}, th.Text, color.NRGBA{}
	default:
		return th.Surface, th.Text, th.Hairline
	}
}

// overlay returns the hover/pressed overlay: +4 % hover, +8 % pressed, eased.
func overlay(gtx layout.Context, th *theme.Theme, c *widget.Clickable) color.NRGBA {
	target := float32(0)
	switch {
	case !gtx.Enabled():
	case c.Pressed():
		target = 0.08
	case c.Hovered():
		target = 0.04
	}
	a := eased(gtx, c, target)
	ov := color.NRGBA{} // darken in light themes
	if isDark(th) {
		ov = color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF}
	}
	return theme.WithAlpha(ov, uint8(a*255+0.5))
}

// Button is a text button with an optional leading icon.
func Button(th *theme.Theme, c *widget.Clickable, txt string, kind ButtonKind, opts ...ButtonOpt) layout.Widget {
	var cfg buttonCfg
	for _, o := range opts {
		o(&cfg)
	}
	return func(gtx layout.Context) layout.Dimensions {
		bg, fg, border := buttonColors(th, kind)
		h, padX, isz := unit.Dp(32), theme.S3, unit.Dp(16)
		if cfg.small {
			h, padX, isz = 28, 10, 14
		}
		lastButtonOpacity = 1
		if !gtx.Enabled() {
			lastButtonOpacity = disabledAlpha
			defer paint.PushOpacity(gtx.Ops, disabledAlpha).Pop()
		}
		return FocusRing(gtx, th, c, theme.RadiusControl, func(gtx layout.Context) layout.Dimensions {
			return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				if gtx.Enabled() {
					pointer.CursorPointer.Add(gtx.Ops)
				}
				ov := overlay(gtx, th, c)
				gtx.Constraints.Min.Y = gtx.Dp(h)
				return layout.Background{}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					sz := gtx.Constraints.Min
					fillRRect(gtx, bg, sz, theme.RadiusControl)
					if border.A > 0 {
						widget.Border{Color: border, CornerRadius: theme.RadiusControl, Width: 1}.Layout(gtx, func(layout.Context) layout.Dimensions {
							return layout.Dimensions{Size: sz}
						})
					}
					if ov.A > 0 {
						fillRRect(gtx, ov, sz, theme.RadiusControl)
					}
					return layout.Dimensions{Size: sz}
				}, func(gtx layout.Context) layout.Dimensions {
					return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Inset{Left: padX, Right: padX}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									if cfg.icon == nil {
										return layout.Dimensions{}
									}
									d := cfg.icon(gtx, isz, fg)
									return layout.Dimensions{Size: image.Pt(d.Size.X+gtx.Dp(6), d.Size.Y)}
								}),
								layout.Rigid(styled(th, theme.StyleBodyStrong, fg, txt).Layout),
							)
						})
					})
				})
			})
		})
	}
}

// IconButton is a 28-dp square ghost button; desc is its accessible description.
func IconButton(th *theme.Theme, c *widget.Clickable, icon icons.Icon, desc string) layout.Widget {
	return IconButtonSize(th, c, icon, desc, 28)
}

// IconButtonSize is IconButton with a size x size dp hit area (touch
// layouts use 40 dp); the glyph stays 16 dp.
func IconButtonSize(th *theme.Theme, c *widget.Clickable, icon icons.Icon, desc string, size unit.Dp) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		if !gtx.Enabled() {
			defer paint.PushOpacity(gtx.Ops, disabledAlpha).Pop()
		}
		return FocusRing(gtx, th, c, theme.RadiusControl, func(gtx layout.Context) layout.Dimensions {
			return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				semantic.DescriptionOp(desc).Add(gtx.Ops)
				if gtx.Enabled() {
					pointer.CursorPointer.Add(gtx.Ops)
				}
				hot := eased(gtx, c, b2f(gtx.Enabled() && (c.Hovered() || c.Pressed())))
				sz := image.Pt(gtx.Dp(size), gtx.Dp(size))
				if hot > 0 {
					bg := th.SurfaceSunken
					if c.Pressed() {
						bg = th.Hairline
					}
					fillRRect(gtx, theme.WithAlpha(bg, uint8(float32(bg.A)*hot)), sz, theme.RadiusControl)
				}
				fg := theme.Mix(th.TextMuted, th.Text, hot)
				gtx = fixed(gtx, size, size)
				layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					if icon == nil {
						return layout.Dimensions{}
					}
					return icon(gtx, 16, fg)
				})
				return layout.Dimensions{Size: sz}
			})
		})
	}
}

func b2f(b bool) float32 {
	if b {
		return 1
	}
	return 0
}
