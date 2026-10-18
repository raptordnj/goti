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

// ToastFades holds per-toast fades, keyed by toast identity. The caller
// owns it. A toast that disappears from the list keeps a short-lived
// "leaving" copy that fades out over anim.FadeDuration (none when motion is
// reduced); it is dropped once invisible.
type ToastFades struct {
	m       map[toastKey]*anim.Fade
	kinds   map[toastKey]state.ToastKind // kinds of last frame's toasts
	leaving []leavingToast
}

type leavingToast struct {
	t    state.Toast
	fade *anim.Fade
}

type toastKey struct {
	text string
	at   time.Time
}

// Leaving reports how many toasts are fading out.
func (f *ToastFades) Leaving() int { return len(f.leaving) }

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

// prune turns fades of toasts no longer present into leaving copies.
func (f *ToastFades) prune(toasts []state.Toast) {
	if len(f.m) == 0 {
		return
	}
	live := make(map[toastKey]bool, len(toasts))
	for _, t := range toasts {
		live[toastKey{t.Text, t.At}] = true
	}
	reduce := anim.Reduce != nil && anim.Reduce()
	for k, fd := range f.m {
		if live[k] {
			continue
		}
		delete(f.m, k)
		if !reduce {
			fd.Show(false)
			f.leaving = append(f.leaving, leavingToast{state.Toast{Text: k.text, At: k.at, Kind: f.kind(k)}, fd})
		}
	}
}

// kind is filled in by ToastStack from the previous frame's toasts.
func (f *ToastFades) kind(k toastKey) state.ToastKind { return f.kinds[k] }

// sweep drops leaving copies that have fully faded.
func (f *ToastFades) sweep() {
	kept := f.leaving[:0]
	for _, l := range f.leaving {
		if l.fade.Visible() {
			kept = append(kept, l)
		}
	}
	clear(f.leaving[len(kept):])
	f.leaving = kept
}

// faded draws w with alpha a and a 6-dp rise while a < 1.
func faded(gtx layout.Context, a float32, w layout.Widget) layout.Dimensions {
	if a >= 1 {
		return w(gtx)
	}
	defer paint.PushOpacity(gtx.Ops, a).Pop()
	defer op.Offset(image.Pt(0, int(float32(gtx.Dp(6))*(1-a)))).Push(gtx.Ops).Pop()
	return w(gtx)
}

// ToastStack renders up to the newest three toasts bottom-centre within its
// constraints (newest last); dismiss[i] is the close button of toasts[i]
// (missing entries get no button). Each toast fades in with a 6-dp rise
// and fades out when it leaves the list, driven by fades (nil: no
// animation).
func ToastStack(th *theme.Theme, fades *ToastFades, toasts []state.Toast, dismiss []widget.Clickable) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		if fades != nil {
			fades.prune(toasts)
			if fades.kinds == nil {
				fades.kinds = map[toastKey]state.ToastKind{}
			}
			clear(fades.kinds)
			for _, t := range toasts {
				fades.kinds[toastKey{t.Text, t.At}] = t.Kind
			}
		}
		first := max(0, len(toasts)-maxToasts)
		children := make([]layout.FlexChild, 0, 2*maxToasts+2)
		gap := func() {
			if len(children) > 0 {
				children = append(children, layout.Rigid(layout.Spacer{Height: theme.S2}.Layout))
			}
		}
		if fades != nil {
			for _, l := range fades.leaving {
				gap()
				children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return faded(gtx, l.fade.Alpha(gtx), func(gtx layout.Context) layout.Dimensions { return toast(gtx, th, l.t, nil) })
				}))
			}
		}
		for i := first; i < len(toasts); i++ {
			t := toasts[i]
			var btn *widget.Clickable
			if i < len(dismiss) {
				btn = &dismiss[i]
			}
			gap()
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				w := func(gtx layout.Context) layout.Dimensions { return toast(gtx, th, t, btn) }
				if fades == nil {
					return w(gtx)
				}
				return faded(gtx, fades.get(t).Alpha(gtx), w)
			}))
		}
		gtx.Constraints.Min = gtx.Constraints.Max
		d := layout.S.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min = image.Point{}
			gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(toastWidth))
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
		})
		if fades != nil {
			fades.sweep()
		}
		return d
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
