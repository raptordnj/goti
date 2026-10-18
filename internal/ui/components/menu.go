package components

import (
	"image"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/widget"

	"github.com/raptordnj/goti/internal/ui/icons"
	"github.com/raptordnj/goti/internal/ui/theme"
)

const (
	menuWidth  = 200
	menuItemH  = 30
	menuMargin = 4
)

// MenuItem is one entry of a popover menu.
type MenuItem struct {
	Key, Label string
	Icon       icons.Icon
	Danger     bool
}

// MenuState is a popover menu anchored at a window point (px).
type MenuState struct {
	Open   bool
	Anchor image.Point
	Items  []MenuItem

	list    widget.List
	clicks  []widget.Clickable
	cardTag int             // pointer tag of the card (absorbs clicks)
	rect    image.Rectangle // card bounds of the last frame (window px)
}

// Layout draws the open menu over the whole window (call it last, with
// window constraints) and returns the key of the picked item, if any.
// Esc, or a press outside the card, closes it; Up/Down move focus.
func (m *MenuState) Layout(gtx layout.Context, th *theme.Theme) (picked string) {
	if !m.Open {
		m.rect = image.Rectangle{}
		return ""
	}
	if len(m.clicks) != len(m.Items) {
		m.clicks = make([]widget.Clickable, len(m.Items))
	}
	for i := range m.clicks {
		if m.clicks[i].Clicked(gtx) {
			picked = m.Items[i].Key
		}
	}
	m.handleKeys(gtx)
	win := gtx.Constraints.Max
	// Outside-press catcher over the whole window.
	area := clip.Rect{Max: win}.Push(gtx.Ops)
	event.Op(gtx.Ops, m)
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: m, Kinds: pointer.Press})
		if !ok {
			break
		}
		if e, ok := ev.(pointer.Event); ok && e.Kind == pointer.Press {
			m.Open = false
		}
	}
	area.Pop()
	if picked != "" {
		m.Open = false
	}
	if !m.Open {
		m.rect = image.Rectangle{}
		return picked
	}

	cg := gtx
	cg.Constraints = layout.Exact(image.Pt(gtx.Dp(menuWidth), 0))
	cg.Constraints.Max.Y = win.Y - 2*gtx.Dp(menuMargin)
	rec := op.Record(gtx.Ops)
	dims := Raised(cg, th, theme.RadiusRow, func(gtx layout.Context) layout.Dimensions {
		return m.card(gtx, th)
	})
	call := rec.Stop()
	m.rect = placeMenu(m.Anchor, dims.Size, win, gtx.Dp(menuMargin))
	defer op.Offset(m.rect.Min).Push(gtx.Ops).Pop()
	cardArea := clip.Rect{Max: dims.Size}.Push(gtx.Ops)
	event.Op(gtx.Ops, &m.cardTag)
	for {
		if _, ok := gtx.Event(pointer.Filter{Target: &m.cardTag, Kinds: pointer.Press}); !ok {
			break
		}
	}
	cardArea.Pop()
	call.Add(gtx.Ops)
	return picked
}

// placeMenu positions a box of size sz below-right of anchor, flipping
// above/left when it would overflow win, and clamps it inside win.
func placeMenu(anchor, sz, win image.Point, margin int) image.Rectangle {
	p := anchor
	if p.X+sz.X > win.X-margin {
		p.X = anchor.X - sz.X
	}
	if p.Y+sz.Y > win.Y-margin {
		p.Y = anchor.Y - sz.Y
	}
	p.X = max(margin, min(p.X, win.X-margin-sz.X))
	p.Y = max(margin, min(p.Y, win.Y-margin-sz.Y))
	return image.Rectangle{Min: p, Max: p.Add(sz)}
}

func (m *MenuState) handleKeys(gtx layout.Context) {
	for {
		ev, ok := gtx.Event(key.Filter{Name: key.NameEscape}, key.Filter{Name: key.NameUpArrow}, key.Filter{Name: key.NameDownArrow})
		if !ok {
			return
		}
		e, ok := ev.(key.Event)
		if !ok || e.State != key.Press {
			continue
		}
		switch e.Name {
		case key.NameEscape:
			m.Open = false
		case key.NameUpArrow, key.NameDownArrow:
			m.moveFocus(gtx, e.Name == key.NameDownArrow)
		}
	}
}

func (m *MenuState) moveFocus(gtx layout.Context, down bool) {
	n := len(m.clicks)
	if n == 0 {
		return
	}
	cur := -1
	for i := range m.clicks {
		if gtx.Focused(&m.clicks[i]) {
			cur = i
		}
	}
	next := 0
	switch {
	case cur < 0 && !down:
		next = n - 1
	case cur >= 0 && down:
		next = (cur + 1) % n
	case cur >= 0:
		next = (cur - 1 + n) % n
	}
	gtx.Execute(key.FocusCmd{Tag: &m.clicks[next]})
}

func (m *MenuState) card(gtx layout.Context, th *theme.Theme) layout.Dimensions {
	return layout.Background{}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		sz := gtx.Constraints.Min
		fillRRect(gtx, th.Surface, sz, theme.RadiusRow)
		widget.Border{Color: th.Hairline, CornerRadius: theme.RadiusRow, Width: 1}.Layout(gtx, func(layout.Context) layout.Dimensions {
			return layout.Dimensions{Size: sz}
		})
		return layout.Dimensions{Size: sz}
	}, func(gtx layout.Context) layout.Dimensions {
		return layout.UniformInset(theme.S1).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			m.list.Axis = layout.Vertical
			return m.list.Layout(gtx, len(m.Items), func(gtx layout.Context, i int) layout.Dimensions {
				return m.item(gtx, th, i)
			})
		})
	})
}

func (m *MenuState) item(gtx layout.Context, th *theme.Theme, i int) layout.Dimensions {
	it, c := m.Items[i], &m.clicks[i]
	fg, ic := th.Text, th.TextMuted
	if it.Danger {
		fg, ic = th.Error, th.Error
	}
	return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		gtx.Constraints.Min = image.Pt(gtx.Constraints.Max.X, gtx.Dp(menuItemH))
		hot := eased(gtx, c, b2f(c.Hovered() || gtx.Focused(c)))
		return layout.Background{}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			sz := gtx.Constraints.Min
			if hot > 0 {
				bg := th.SurfaceSunken
				fillRRect(gtx, theme.WithAlpha(bg, uint8(float32(bg.A)*hot)), sz, theme.RadiusControl)
			}
			return layout.Dimensions{Size: sz}
		}, func(gtx layout.Context) layout.Dimensions {
			return midLeft(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: theme.S2, Right: theme.S2}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if it.Icon == nil {
								return layout.Dimensions{Size: image.Pt(gtx.Dp(14), 0)}
							}
							return it.Icon(gtx, 14, ic)
						}),
						layout.Rigid(layout.Spacer{Width: theme.S2 + 2}.Layout),
						layout.Rigid(styled(th, theme.StyleBody, fg, it.Label).Layout),
					)
				})
			})
		})
	})
}
