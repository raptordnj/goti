package components

import (
	"image"
	"image/color"

	"gioui.org/layout"
	"gioui.org/widget"

	"github.com/raptordnj/swiftfetch/internal/ui/icons"
	"github.com/raptordnj/swiftfetch/internal/ui/state"
	"github.com/raptordnj/swiftfetch/internal/ui/theme"
)

const (
	toastWidth = 360
	maxToasts  = 3
)

func toastStyle(th *theme.Theme, k state.ToastKind) (icons.Icon, color.NRGBA) {
	switch k {
	case state.ToastSuccess:
		return icons.Check, th.Success
	case state.ToastError:
		return icons.Alert, th.Error
	}
	return icons.ArrowDown, th.Accent
}

// ToastStack renders up to the newest three toasts bottom-centre within its
// constraints (newest last); dismiss[i] is the close button of toasts[i]
// (missing entries get no button).
func ToastStack(th *theme.Theme, toasts []state.Toast, dismiss []widget.Clickable) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		first := max(0, len(toasts)-maxToasts)
		children := make([]layout.FlexChild, 0, 2*maxToasts)
		for i := first; i < len(toasts); i++ {
			t := toasts[i]
			var btn *widget.Clickable
			if i < len(dismiss) {
				btn = &dismiss[i]
			}
			if i > first {
				children = append(children, layout.Rigid(layout.Spacer{Height: theme.S2}.Layout))
			}
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return toast(gtx, th, t, btn)
			}))
		}
		gtx.Constraints.Min = gtx.Constraints.Max
		return layout.S.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min = image.Point{}
			gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(toastWidth))
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
		})
	}
}

func toast(gtx layout.Context, th *theme.Theme, t state.Toast, btn *widget.Clickable) layout.Dimensions {
	ic, col := toastStyle(th, t.Kind)
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	return Raised(gtx, th, theme.RadiusRow, func(gtx layout.Context) layout.Dimensions {
		return layout.Background{}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			sz := gtx.Constraints.Min
			fillRRect(gtx, th.Surface, sz, theme.RadiusRow)
			widget.Border{Color: th.Hairline, CornerRadius: theme.RadiusRow, Width: 1}.Layout(gtx, func(layout.Context) layout.Dimensions {
				return layout.Dimensions{Size: sz}
			})
			return layout.Dimensions{Size: sz}
		}, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: theme.S1 + 2, Bottom: theme.S1 + 2, Left: theme.S3, Right: theme.S1 + 2}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions { return ic(gtx, 16, col) }),
					layout.Rigid(layout.Spacer{Width: theme.S2 + 2}.Layout),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.Y = gtx.Dp(28)
						return midLeft(gtx, styled(th, theme.StyleBody, th.Text, t.Text).Layout)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if btn == nil {
							return layout.Dimensions{}
						}
						return IconButton(th, btn, icons.Close, "Dismiss")(gtx)
					}),
				)
			})
		})
	})
}
