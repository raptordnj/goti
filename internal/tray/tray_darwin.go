//go:build darwin

package tray

import "fyne.io/systray"

func hostAvailable() bool { return true }

// launch uses RunWithExternalLoop because Gio's app.Main owns the main thread
// and NSApplication run loop.
//
// macOS caveat: start (C.nativeStart) creates the NSStatusItem by calling the
// delegate's applicationDidFinishLaunching directly on the calling thread,
// which AppKit requires to be the main thread; here it is called from a
// goroutine. This cannot be exercised on Linux and is covered by the manual
// macOS check (spec §9). If the tray does not become ready within 2 s, Start
// reports it unavailable and closing the window quits.
func launch(onReady func()) (end func()) {
	start, end := systray.RunWithExternalLoop(onReady, nil)
	start()
	return end
}

func icon() []byte { return iconPNG }
