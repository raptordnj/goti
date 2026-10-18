// Package storage implements safe local file handling: filename
// sanitization, temp files with positional writes, and no-replace
// atomic finalization.
package storage

import (
	"errors"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxNameBytes = 200
	fallbackName = "download"
	maxExtBytes  = 32
)

// SanitizeFilename turns an untrusted, server-supplied name into a single
// safe path element: no separators, control characters, leading dots,
// trailing dots/spaces or Windows reserved device names, at most
// maxNameBytes bytes of valid UTF-8. It never returns an empty string.
func SanitizeFilename(name string) string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	var b strings.Builder
	for i := 0; i < len(name); {
		r, w := utf8.DecodeRuneInString(name[i:])
		i += w
		switch {
		case r == utf8.RuneError && w == 1:
			continue // invalid byte
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r): // C0, DEL, C1, format chars (RLO, zero-width)
			continue
		case strings.ContainsRune(`<>:"|?*`, r):
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	name = trimName(b.String())
	if name == "" {
		return fallbackName
	}
	name = capName(name, maxNameBytes-1) // leave room for a reserved-name suffix
	if name == "" {
		return fallbackName
	}
	if isReserved(name) {
		// Windows matches the part before the FIRST dot, so suffix there.
		i := strings.IndexByte(name, '.')
		if i < 0 {
			i = len(name)
		}
		name = name[:i] + "_" + name[i:]
	}
	return name
}

// trimName strips whitespace and leading dots, then trailing dots/spaces.
func trimName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimLeft(s, ".")
	s = strings.TrimSpace(s)
	return strings.TrimRight(s, ". \t")
}

var tarSuffixes = []string{".tar.gz", ".tar.xz", ".tar.bz2", ".tar.zst"}

// splitExt splits name into stem and extension, treating compound tar
// extensions as one. The extension is empty when there is none.
func splitExt(name string) (stem, ext string) {
	lower := strings.ToLower(name)
	for _, s := range tarSuffixes {
		if strings.HasSuffix(lower, s) && len(name) > len(s) {
			return name[:len(name)-len(s)], name[len(name)-len(s):]
		}
	}
	i := strings.LastIndexByte(name, '.')
	if i <= 0 {
		return name, ""
	}
	return name[:i], name[i:]
}

// capName shortens name to at most limit bytes without splitting a UTF-8
// sequence, keeping the extension where possible.
func capName(name string, limit int) string {
	if len(name) <= limit {
		return name
	}
	stem, ext := splitExt(name)
	if len(ext) > maxExtBytes || len(ext) >= limit {
		stem, ext = name, ""
	}
	cut := limit - len(ext)
	for cut > 0 && !utf8.RuneStart(stem[cut]) {
		cut--
	}
	stem = strings.TrimRight(stem[:cut], ". ")
	return trimName(stem + ext)
}

func isReserved(name string) bool {
	base, _, _ := strings.Cut(name, ".")
	base = strings.ToUpper(strings.TrimRight(base, " "))
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	if rest, ok := strings.CutPrefix(base, "COM"); ok {
		return isDeviceDigit(rest)
	}
	if rest, ok := strings.CutPrefix(base, "LPT"); ok {
		return isDeviceDigit(rest)
	}
	return false
}

// isDeviceDigit reports whether s is a single digit 1-9 or a superscript
// 1, 2 or 3 (which Windows also treats as COM/LPT port numbers).
func isDeviceDigit(s string) bool {
	switch s {
	case "1", "2", "3", "4", "5", "6", "7", "8", "9", "\u00b9", "\u00b2", "\u00b3":
		return true
	}
	return false
}

// SafeJoin joins a sanitized name onto dir and verifies the result stays
// strictly inside dir.
func SafeJoin(dir, name string) (string, error) {
	p := filepath.Join(dir, name)
	rel, err := filepath.Rel(dir, p)
	if err != nil {
		return "", err
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", errors.New("storage: path escapes destination directory")
	}
	return p, nil
}
