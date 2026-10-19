//go:build android

package platform

import "errors"

// AndroidOpen and AndroidShare are installed by the Android host bridge;
// nil means unsupported.
var AndroidOpen, AndroidShare func(path string) error

// OpenPath opens p through the Android host.
func OpenPath(p string) error {
	if AndroidOpen == nil {
		return errors.ErrUnsupported
	}
	return AndroidOpen(p)
}

// RevealPath shares p: Android has no folder view, so Share replaces
// Show in folder.
func RevealPath(p string) error {
	if AndroidShare == nil {
		return errors.ErrUnsupported
	}
	return AndroidShare(p)
}
