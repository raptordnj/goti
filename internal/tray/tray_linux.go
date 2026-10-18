//go:build linux || freebsd || openbsd || netbsd

package tray

import (
	"github.com/godbus/dbus/v5"

	"fyne.io/systray"
)

// statusNotifierWatcher is the D-Bus name a StatusNotifierItem host owns.
const statusNotifierWatcher = "org.kde.StatusNotifierWatcher"

// hostAvailable reports whether a StatusNotifierItem host runs on the
// session bus; without one, systray would "succeed" but show nothing.
func hostAvailable() bool {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return false
	}
	defer conn.Close()
	var has bool
	if err := conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, statusNotifierWatcher).Store(&has); err != nil {
		return false
	}
	return has
}

// launch starts systray alongside Gio's main loop. On Linux the start func
// of RunWithExternalLoop only fires onReady and exports the item on D-Bus
// (no native loop, no thread affinity), so it is called here directly.
func launch(onReady func()) (end func()) {
	start, end := systray.RunWithExternalLoop(onReady, nil)
	start()
	return end
}

func icon() []byte { return iconPNG }
