//go:build darwin || android

package tray

// Stubs for platforms without a tray. On Android the foreground-service
// notification replaces the tray.
//
// On macOS hostAvailable is false: the tray stays disabled (closing the window
// quits) until a main-thread integration is verified on a Mac. systray's
// start func (C.nativeStart) creates the NSStatusItem on the calling thread,
// but Gio's app.Main owns the main thread, so starting it from a goroutine can
// abort the process instead of timing out.
func hostAvailable() bool { return false }

// launch is never reached on macOS (hostAvailable is false).
func launch(func()) (end func()) { return func() {} }

// setup is never reached on macOS (hostAvailable is false).
func setup(Deps, <-chan struct{}) {}
