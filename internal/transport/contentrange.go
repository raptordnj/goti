// Package transport holds HTTP-level helpers: header parsing and URL redaction.
package transport

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/raptordnj/swiftfetch/internal/domain"
)

// ContentRange is a parsed Content-Range response header for the bytes unit.
type ContentRange struct {
	// Range is the half-open byte range served (End = last byte + 1).
	Range domain.ByteRange
	// Total is the complete representation length when TotalKnown is true.
	Total int64
	// TotalKnown is false for "bytes a-b/*".
	TotalKnown bool
	// Unsatisfied is true for "bytes */N" (416 responses).
	Unsatisfied bool
}

var errBadContentRange = errors.New("transport: malformed Content-Range")

// ParseContentRange parses "bytes a-b/N", "bytes a-b/*" and "bytes */N".
func ParseContentRange(h string) (ContentRange, error) {
	rest, ok := strings.CutPrefix(h, "bytes ")
	if !ok {
		return ContentRange{}, errBadContentRange
	}
	spec, totalStr, ok := strings.Cut(rest, "/")
	if !ok {
		return ContentRange{}, errBadContentRange
	}
	if spec == "*" {
		total, err := parseUint(totalStr)
		if err != nil {
			return ContentRange{}, err
		}
		return ContentRange{Total: total, TotalKnown: true, Unsatisfied: true}, nil
	}
	startStr, endStr, ok := strings.Cut(spec, "-")
	if !ok {
		return ContentRange{}, errBadContentRange
	}
	first, err := parseUint(startStr)
	if err != nil {
		return ContentRange{}, err
	}
	last, err := parseUint(endStr)
	if err != nil {
		return ContentRange{}, err
	}
	if last < first || last == 1<<63-1 {
		return ContentRange{}, fmt.Errorf("%w: invalid interval", errBadContentRange)
	}
	cr := ContentRange{Range: domain.ByteRange{Start: first, End: last + 1}}
	if totalStr != "*" {
		total, err := parseUint(totalStr)
		if err != nil {
			return ContentRange{}, err
		}
		if last >= total {
			return ContentRange{}, fmt.Errorf("%w: end beyond total", errBadContentRange)
		}
		cr.Total, cr.TotalKnown = total, true
	}
	return cr, nil
}

// parseUint parses a non-negative decimal int64 consisting only of digits.
func parseUint(s string) (int64, error) {
	if s == "" {
		return 0, errBadContentRange
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, errBadContentRange
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", errBadContentRange, err)
	}
	return n, nil
}

// RangeHeader formats r as a Range request header value.
func RangeHeader(r domain.ByteRange) string {
	return fmt.Sprintf("bytes=%d-%d", r.Start, r.End-1)
}

// OpenRangeHeader formats an open-ended Range request header value.
func OpenRangeHeader(start int64) string {
	return fmt.Sprintf("bytes=%d-", start)
}
