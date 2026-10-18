package screens

import (
	"image/color"
	"sync"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget/material"

	"github.com/raptordnj/goti/internal/ui/theme"
)

// Test hook: when recording is on, every string drawn through Text is kept.
var rec struct {
	sync.Mutex
	on    bool
	texts []string
}

// RecordTexts turns text recording on or off (and clears the record).
func RecordTexts(on bool) {
	rec.Lock()
	defer rec.Unlock()
	rec.on, rec.texts = on, nil
}

// RecordedTexts returns the strings drawn since recording was turned on.
func RecordedTexts() []string {
	rec.Lock()
	defer rec.Unlock()
	return append([]string(nil), rec.texts...)
}

// Note records s as drawn (for strings drawn by components).
func Note(s string) { record(s) }

func record(s string) {
	rec.Lock()
	if rec.on {
		rec.texts = append(rec.texts, s)
	}
	rec.Unlock()
}

// Text returns a label widget of the given size and color; long text wraps
// to at most maxLines lines (0 = unlimited).
func Text(th *theme.Theme, size unit.Sp, c color.NRGBA, s string, maxLines int) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		record(s)
		l := labelStyle(th, size, c, s)
		l.MaxLines = maxLines
		return l.Layout(gtx)
	}
}

func labelStyle(th *theme.Theme, size unit.Sp, c color.NRGBA, s string) material.LabelStyle {
	return material.LabelStyle{Text: s, Color: c, TextSize: size, Shaper: th.Shaper, MaxLines: 1}
}

// Label is a single-line Text.
func Label(th *theme.Theme, size unit.Sp, c color.NRGBA, s string) layout.Widget {
	return Text(th, size, c, s, 1)
}

// Rigids wraps widgets as rigid flex children.
func Rigids(ws ...layout.Widget) []layout.FlexChild {
	out := make([]layout.FlexChild, len(ws))
	for i, w := range ws {
		out[i] = layout.Rigid(w)
	}
	return out
}

// Row lays out widgets horizontally with S2 gaps.
func Row(ws ...layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		var ch []layout.FlexChild
		for i, w := range ws {
			if i > 0 {
				ch = append(ch, layout.Rigid(layout.Spacer{Width: theme.S2}.Layout))
			}
			ch = append(ch, layout.Rigid(w))
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx, ch...)
	}
}

// Column lays out widgets vertically with gap between them.
func Column(gap unit.Dp, ws ...layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		var ch []layout.FlexChild
		for i, w := range ws {
			if i > 0 {
				ch = append(ch, layout.Rigid(layout.Spacer{Height: gap}.Layout))
			}
			ch = append(ch, layout.Rigid(w))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
	}
}
