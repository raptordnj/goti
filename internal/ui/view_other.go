//go:build !android

package ui

import "gioui.org/io/event"

// androidView reports the view handle carried by an AndroidViewEvent; there
// are none off Android.
func androidView(event.Event) (uintptr, bool) { return 0, false }
