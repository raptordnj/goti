//go:build android

package ui

import (
	"gioui.org/io/event"

	gioapp "gioui.org/app"
)

// androidView reports the view handle carried by an AndroidViewEvent.
func androidView(ev event.Event) (uintptr, bool) {
	e, ok := ev.(gioapp.AndroidViewEvent)
	return e.View, ok
}
