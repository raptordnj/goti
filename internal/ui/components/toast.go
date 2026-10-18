package components

import (
	"image"
	"image/color"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/widget"

	"github.com/raptordnj/goti/internal/ui/anim"
	"github.com/raptordnj/goti/internal/ui/icons"
	"github.com/raptordnj/goti/internal/ui/state"
	"github.com/raptordnj/goti/internal/ui/theme"
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

// ToastFades holds per-toast enter fades, keyed by toast identity. The
// caller owns it; entries are dropped once their toast disappears.
type ToastFades struct {
	m map[toastKey]*anim.Fade
}

type toastKey struct {
	text string
	at   time.Time
}

// get returns the fade for t, creating a shown (entering) one if needed.
func (f *ToastFades) get(t state.Toast) *anim.Fade {
	if f.m == nil {
		f.m = map[toastKey]*anim.Fade{}
	}
	k := toastKey{t.Text, t.At}
	fd, ok := f.m[k]
	if !ok {
		fd = new(anim.Fade)
		fd.Show(true)
		f.m[k] = fd
	}
	return fd
}

// prune drops fades of toasts no longer present.
func (f *ToastFades) prune(toasts []state.Toast) {
	if len(f.m) == 0 {
		return
	}
	live := make(map[toastKey]bool, len(toasts))
	for _, t := range toasts {
		live[toastKey{t.Text, t.At}] = true
	}
	for k := range f.m {
		if !live[k] {
			delete(f.m, k)
		}
	}
}

// ToastStack renders up to the newest three toasts bottom-centre within its
// constraints (newest last); dismiss[i] is the close button of toasts[i]
// (missing entries get no button). Each toast fades in with a 6-dp rise
// driven by fades (nil: no animation). Dismissal is not faded (deferred:
// the model removes toasts outright).
func ToastStack(th *theme.Theme, fades *ToastFades, toasts []state.Toast, dismiss []widget.Clickable) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		if fades != nil {
			fades.prune(toasts)
		}
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
				if fades == nil {
					return toast(gtx, th, t, btn)
				}
				a := fades.get(t).Alpha(gtx)
				if a >= 1 {
					return toast(gtx, th, t, btn)
				}
				defer paint.PushOpacity(gtx.Ops, a).Pop()
				defer op.Offset(image.Pt(0, int(float32(gtx.Dp(6))*(1-a)))).Push(gtx.Ops).Pop()
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
