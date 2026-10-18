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

	"github.com/raptordnj/goti/internal/ui/anim"
	"github.com/raptordnj/goti/internal/ui/theme"
)

// dialogWidth is the card width in dp (narrower windows shrink it).
const dialogWidth = 440

// dialogMargin is the minimum gap in dp between the card and the window edge.
const dialogMargin = 16

// scrimTag absorbs pointer input behind a dialog.
var scrimTag = new(int)

// DialogState is the persistent state of a dialog: its fade, the body's
// scroll list and the card rectangle laid out last frame.
type DialogState struct {
	Fade anim.Fade
	List widget.List
	// Card is the card's window-space rectangle from the last frame.
	Card image.Rectangle
}

// DialogShell is Dialog without persistent state (body does not scroll).
func DialogShell(th *theme.Theme, fade *anim.Fade, title string, body, actions layout.Widget) layout.Widget {
	return dialog(th, fade, nil, title, body, actions)
}

// Dialog fills the window with a scrim and centres a card min(440 dp,
// window − 32 dp) wide with a Display title, a scrollable body and
// right-aligned actions; the card never exceeds the window height minus
// margins. ds.Fade drives the enter fade and 8-dp rise.
func Dialog(th *theme.Theme, ds *DialogState, title string, body, actions layout.Widget) layout.Widget {
	return dialog(th, &ds.Fade, ds, title, body, actions)
}

func dialog(th *theme.Theme, fade *anim.Fade, ds *DialogState, title string, body, actions layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		a := float32(1)
		if fade != nil {
			a = fade.Alpha(gtx)
		}
		full := gtx.Constraints.Max
		scrimA := float32(0.30)
		if isDark(th) {
			scrimA = 0.60
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
		margin := gtx.Dp(dialogMargin)
		gtx.Constraints.Min = image.Point{}
		gtx.Constraints.Max.X = max(min(full.X-2*margin, gtx.Dp(dialogWidth)), 0)
		gtx.Constraints.Max.Y = max(full.Y-2*margin, 0)
		m := op.Record(gtx.Ops)
		dims := Overlay(gtx, th, theme.RadiusPanel, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return card(gtx, th, ds, title, body, actions)
		})
		call := m.Stop()
		rise := int(float32(gtx.Dp(8)) * (1 - a))
		off := image.Pt((full.X-dims.Size.X)/2, (full.Y-dims.Size.Y)/2)
		if ds != nil {
			ds.Card = image.Rectangle{Min: off, Max: off.Add(dims.Size)}
		}
		defer op.Offset(off.Add(image.Pt(0, rise))).Push(gtx.Ops).Pop()
		call.Add(gtx.Ops)
		return layout.Dimensions{Size: full}
	}
}

// measureH lays out w into a discarded macro and returns its height.
func measureH(gtx layout.Context, w layout.Widget) int {
	m := op.Record(gtx.Ops)
	gtx.Constraints.Min = image.Point{}
	h := w(gtx).Size.Y
	m.Stop()
	return h
}

func card(gtx layout.Context, th *theme.Theme, ds *DialogState, title string, body, actions layout.Widget) layout.Dimensions {
	return layout.Background{}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		sz := gtx.Constraints.Min
		fillRRect(gtx, th.Surface, sz, theme.RadiusPanel)
		widget.Border{Color: th.Hairline, CornerRadius: theme.RadiusPanel, Width: 1}.Layout(gtx, func(layout.Context) layout.Dimensions {
			return layout.Dimensions{Size: sz}
		})
		return layout.Dimensions{Size: sz}
	}, func(gtx layout.Context) layout.Dimensions {
		return layout.UniformInset(theme.S5).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			titleW := styled(th, theme.StyleDisplay, th.Text, title).Layout
			actionsW := func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.E.Layout(gtx, actions)
			}
			// Reserve title, actions and gaps; the body gets what is left.
			reserved := measureH(gtx, titleW) + measureH(gtx, actionsW) + gtx.Dp(theme.S4) + gtx.Dp(theme.S5)
			bodyW := func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.Y = 0
				gtx.Constraints.Max.Y = max(gtx.Constraints.Max.Y-reserved, 0)
				if ds == nil {
					defer clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops).Pop()
					return body(gtx)
				}
				ds.List.Axis = layout.Vertical
				return ds.List.Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions { return body(gtx) })
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(titleW),
				layout.Rigid(layout.Spacer{Height: theme.S4}.Layout),
				layout.Rigid(bodyW),
				layout.Rigid(layout.Spacer{Height: theme.S5}.Layout),
				layout.Rigid(actionsW),
			)
		})
	})
}
