//go:build !android

package mobile

import "log/slog"

// platformHost is nil off Android.
func platformHost(*slog.Logger) uriHost { return nil }

// installOpeners is a no-op off Android (platform.AndroidOpen is android-only).
func installOpeners(open, share func(string) error) { testOpeners = [2]func(string) error{open, share} }

// testOpeners records the openers for desktop tests.
var testOpeners [2]func(string) error
