//go:build darwin

package platform

// OpenPath opens p with the default application.
func OpenPath(p string) error { return startCmd("open", p) }

// RevealPath shows p in Finder.
func RevealPath(p string) error { return startCmd("open", "-R", p) }
