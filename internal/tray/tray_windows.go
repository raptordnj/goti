//go:build windows

package tray

import (
	"runtime"

	"fyne.io/systray"
)

func hostAvailable() bool { return true }

// launch runs systray on its own locked OS thread. RunWithExternalLoop is
// not used on Windows: it creates the hidden tray window on the calling
// thread but its start func pumps GetMessage from a different, unlocked
// goroutine, which never receives that window's messages; and Gio's message
// loops run on their own per-window threads. systray.Run on a locked
// goroutine keeps window creation and the message pump on one thread.
func launch(onReady func()) (end func()) {
	go func() {
		runtime.LockOSThread()
		systray.Run(onReady, nil)
	}()
	return systray.Quit
}

func icon() []byte { return iconICO }
