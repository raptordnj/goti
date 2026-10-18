package transport

import (
	"mime"
	"net/url"
	"strings"
	"unicode/utf8"
)

// ParseContentDisposition extracts the raw (unsanitized) filename from a
// Content-Disposition header. filename* (RFC 5987/6266, charset UTF-8 or
// ISO-8859-1) wins over filename; otherwise mime.ParseMediaType supplies the
// quoted-string or token form.
func ParseContentDisposition(h string) (name string, ok bool) {
	if n, found := extendedFilename(h); found {
		return n, true
	}
	_, params, err := mime.ParseMediaType(h)
	if err != nil && params == nil {
		return "", false
	}
	name = params["filename"]
	if name == "" {
		return "", false
	}
	return name, true
}

// extendedFilename decodes the filename* parameter, if present and valid.
func extendedFilename(h string) (string, bool) {
	for _, part := range strings.Split(h, ";") {
		key, val, ok := strings.Cut(part, "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "filename*") {
			continue
		}
		charset, rest, ok := strings.Cut(strings.TrimSpace(val), "'")
		if !ok {
			continue
		}
		_, enc, ok := strings.Cut(rest, "'") // skip language tag
		if !ok {
			continue
		}
		raw, err := url.PathUnescape(enc)
		if err != nil || raw == "" {
			continue
		}
		switch strings.ToLower(charset) {
		case "utf-8":
			if utf8.ValidString(raw) {
				return raw, true
			}
		case "iso-8859-1":
			rs := make([]rune, len(raw))
			for i := 0; i < len(raw); i++ {
				rs[i] = rune(raw[i])
			}
			return string(rs), true
		}
	}
	return "", false
}
