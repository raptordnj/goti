package ui

import (
	"os"

	gioapp "gioui.org/app"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/raptordnj/goti/internal/brand"
	"github.com/raptordnj/goti/internal/ui/components"
	"github.com/raptordnj/goti/internal/ui/theme"
)

// ShowFatal shows msg in a small window with a Quit button and exits with
// code 1 once it closes. It never returns and must be called from main,
// since it runs gioapp.Main.
func ShowFatal(msg string) {
	go func() {
		runFatal(msg)
		os.Exit(1)
	}()
	gioapp.Main()
}

func runFatal(msg string) {
	w := new(gioapp.Window)
	w.Option(gioapp.Title(brand.Name), gioapp.Size(unit.Dp(480), unit.Dp(240)))
	th := theme.New(theme.Light())
	var quit widget.Clickable
	var ops op.Ops
	for {
		switch e := w.Event().(type) {
		case gioapp.DestroyEvent:
			return
		case gioapp.FrameEvent:
			gtx := gioapp.NewContext(&ops, e)
			if fatalLayout(gtx, th, &quit, msg) {
				w.Perform(system.ActionClose)
			}
			e.Frame(gtx.Ops)
		}
	}
}

// fatalLayout draws the startup-error screen: a dialog titled with the app
// name, msg as its body and a Quit button. It reports whether Quit was clicked.
func fatalLayout(gtx layout.Context, th *theme.Theme, quit *widget.Clickable, msg string) bool {
	clicked := quit.Clicked(gtx)
	paint.Fill(gtx.Ops, th.Bg)
	body := func(gtx layout.Context) layout.Dimensions {
		l := material.LabelStyle{Text: msg, Color: th.Text, TextSize: theme.Body, Shaper: th.Shaper}
		return l.Layout(gtx)
	}
	actions := components.Button(th, quit, "Quit", components.Primary)
	components.DialogShell(th, nil, brand.Name, body, actions)(gtx)
	return clicked
}
