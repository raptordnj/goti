package components

import (
	"image/color"

	"gioui.org/layout"
	"gioui.org/widget"

	"github.com/raptordnj/swiftfetch/internal/ui/state"
	"github.com/raptordnj/swiftfetch/internal/ui/theme"
)

func toastAccent(th *theme.Theme, k state.ToastKind) color.NRGBA {
	switch k {
	case state.ToastSuccess:
		return th.Success
	case state.ToastError:
		return th.Error
	}
	return th.Accent
}

// ToastStack renders toasts stacked vertically (newest last); dismiss[i] is
// the close button of toasts[i] (missing entries get no button).
func ToastStack(th *theme.Theme, toasts []state.Toast, dismiss []widget.Clickable) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		children := make([]layout.FlexChild, 0, 2*len(toasts))
		for i := range toasts {
			t := toasts[i]
			var btn *widget.Clickable
			if i < len(dismiss) {
				btn = &dismiss[i]
			}
			if i > 0 {
				children = append(children, layout.Rigid(layout.Spacer{Height: theme.S2}.Layout))
			}
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return toast(gtx, th, t, btn)
			}))
		}
		return layout.Flex{Axis: layout.Vertical, Alignment: layout.End}.Layout(gtx, children...)
	}
}

func toast(gtx layout.Context, th *theme.Theme, t state.Toast, btn *widget.Clickable) layout.Dimensions {
	return widget.Border{Color: toastAccent(th, t.Kind), CornerRadius: theme.RadiusSm, Width: 1}.Layout(gtx,
		background(th.Surface, theme.RadiusSm, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: theme.S2, Bottom: theme.S2, Left: theme.S3, Right: theme.S1}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(label(th, theme.TextMd, th.Text, t.Text).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if btn == nil {
							return layout.Dimensions{}
						}
						return IconButton(th, btn, glyphDismiss, "Dismiss")(gtx)
					}),
				)
			})
		}))
}
