//go:build linux

package platform

// OpenPath opens p with the default application.
func OpenPath(p string) error { return startCmd("xdg-open", p) }

// RevealPath opens the folder containing p.
func RevealPath(p string) error { return startCmd("xdg-open", parentDir(p)) }
