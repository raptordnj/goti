package components

import (
	"image"
	"image/color"

	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget"

	"github.com/raptordnj/swiftfetch/internal/ui/anim"
	"github.com/raptordnj/swiftfetch/internal/ui/theme"
)

// dialogWidth is the card width in dp (narrower windows shrink it).
const dialogWidth = 440

// scrimTag absorbs pointer input behind a dialog.
var scrimTag = new(int)

// DialogShell fills the window with a scrim and centres a 440-dp card with
// a Display title, body and right-aligned actions. fade (nil: fully shown)
// drives the enter/exit fade and 8-dp rise.
func DialogShell(th *theme.Theme, fade *anim.Fade, title string, body, actions layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		a := float32(1)
		if fade != nil {
			a = fade.Alpha(gtx)
		}
		full := gtx.Constraints.Max
		scrimA := float32(0.30)
		if isDark(th) {
			scrimA = 0.45
		}
		area := clip.Rect{Max: full}.Push(gtx.Ops)
		event.Op(gtx.Ops, scrimTag)
		for {
			if _, ok := gtx.Event(pointer.Filter{Target: scrimTag, Kinds: pointer.Press | pointer.Scroll, ScrollY: pointer.ScrollRange{Min: -1e6, Max: 1e6}}); !ok {
				break
			}
		}
		paint.FillShape(gtx.Ops, color.NRGBA{A: uint8(scrimA * a * 255)}, clip.Rect{Max: full}.Op())
		area.Pop()

		defer paint.PushOpacity(gtx.Ops, a).Pop()
		gtx.Constraints.Min = image.Point{}
		gtx.Constraints.Max.X = min(full.X-gtx.Dp(theme.S6*2), gtx.Dp(dialogWidth))
		m := op.Record(gtx.Ops)
		dims := Overlay(gtx, th, theme.RadiusPanel, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return card(gtx, th, title, body, actions)
		})
		call := m.Stop()
		rise := int(float32(gtx.Dp(8)) * (1 - a))
		off := image.Pt((full.X-dims.Size.X)/2, (full.Y-dims.Size.Y)/2+rise)
		defer op.Offset(off).Push(gtx.Ops).Pop()
		call.Add(gtx.Ops)
		return layout.Dimensions{Size: full}
	}
}

func card(gtx layout.Context, th *theme.Theme, title string, body, actions layout.Widget) layout.Dimensions {
	return layout.Background{}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		sz := gtx.Constraints.Min
		fillRRect(gtx, th.Surface, sz, theme.RadiusPanel)
		widget.Border{Color: th.Hairline, CornerRadius: theme.RadiusPanel, Width: 1}.Layout(gtx, func(layout.Context) layout.Dimensions {
			return layout.Dimensions{Size: sz}
		})
		return layout.Dimensions{Size: sz}
	}, func(gtx layout.Context) layout.Dimensions {
		return layout.UniformInset(theme.S5).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(styled(th, theme.StyleDisplay, th.Text, title).Layout),
				layout.Rigid(layout.Spacer{Height: theme.S4}.Layout),
				layout.Rigid(body),
				layout.Rigid(layout.Spacer{Height: theme.S5}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.E.Layout(gtx, actions)
				}),
			)
		})
	})
}
