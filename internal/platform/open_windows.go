//go:build windows

package platform

// OpenPath opens p with the default application.
func OpenPath(p string) error { return startCmd("rundll32", "url.dll,FileProtocolHandler", p) }

// RevealPath shows p selected in Explorer.
func RevealPath(p string) error { return startCmd("explorer", "/select,"+p) }
