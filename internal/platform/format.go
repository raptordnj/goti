// Package platform holds small OS and formatting helpers for the GUI.
package platform

import (
	"fmt"
	"path/filepath"
	"time"
)

// FormatBytes renders n in binary units with one decimal ("1.5 KiB").
func FormatBytes(n int64) string {
	if n < 1024 {
		if n < 0 {
			n = 0
		}
		return fmt.Sprintf("%d B", n)
	}
	v := float64(n)
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB"}
	i := 0
	for v/1024 >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", v/1024, units[i])
}

// FormatSpeed renders bytes per second ("2.4 MiB/s").
func FormatSpeed(bps float64) string {
	if bps < 0 {
		bps = 0
	}
	return FormatBytes(int64(bps)) + "/s"
}

// FormatETA renders a remaining duration compactly.
func FormatETA(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	}
	return fmt.Sprintf("%d h %02d min", int(d.Hours()), int(d.Minutes())%60)
}

// MiddleEllipsis shortens s to at most n runes, eliding the middle and
// keeping the file extension when it fits.
func MiddleEllipsis(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	if ext := []rune(filepath.Ext(s)); len(ext) > 0 && n-1-len(ext) >= 1 {
		return string(r[:n-1-len(ext)]) + "…" + string(ext)
	}
	back := (n - 1) / 2
	front := n - 1 - back
	return string(r[:front]) + "…" + string(r[len(r)-back:])
}
