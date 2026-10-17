package transport

import (
	"testing"

	"github.com/raptordnj/swiftfetch/internal/domain"
)

func TestParseContentRange(t *testing.T) {
	tests := []struct {
		in   string
		want ContentRange
	}{
		{"bytes 0-0/100", ContentRange{Range: domain.ByteRange{Start: 0, End: 1}, Total: 100, TotalKnown: true}},
		{"bytes 5368709120-5368709121/5368709122", ContentRange{Range: domain.ByteRange{Start: 5368709120, End: 5368709122}, Total: 5368709122, TotalKnown: true}},
		{"bytes 0-9/*", ContentRange{Range: domain.ByteRange{Start: 0, End: 10}}},
		{"bytes */0", ContentRange{Unsatisfied: true, TotalKnown: true}},
		{"bytes */500", ContentRange{Unsatisfied: true, TotalKnown: true, Total: 500}},
	}
	for _, tc := range tests {
		got, err := ParseContentRange(tc.in)
		if err != nil {
			t.Errorf("ParseContentRange(%q) error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseContentRange(%q) = %+v, want %+v", tc.in, got, tc.want)
		}
	}
}

func TestParseContentRangeErrors(t *testing.T) {
	for _, in := range []string{
		"bytes 9-0/100", "bytes 0-100/100", "bytes -1-5/10", "0-5/10",
		"bytes 0-5/10 junk", "bytes 0-99999999999999999999/1", "", "bytes", "bytes 0-5",
		"bytes 0-9223372036854775807/*", "bytes */-1", "bytes */", "bytes a-b/c", "items 0-5/10",
		"bytes 0-5/99999999999999999999", "bytes +1-5/10",
	} {
		if got, err := ParseContentRange(in); err == nil {
			t.Errorf("ParseContentRange(%q) = %+v, want error", in, got)
		}
	}
}

func TestRangeHeader(t *testing.T) {
	if got := RangeHeader(domain.ByteRange{Start: 0, End: 1}); got != "bytes=0-0" {
		t.Errorf("got %q", got)
	}
	if got := RangeHeader(domain.ByteRange{Start: 10, End: 20}); got != "bytes=10-19" {
		t.Errorf("got %q", got)
	}
	if got := OpenRangeHeader(42); got != "bytes=42-" {
		t.Errorf("got %q", got)
	}
}

func FuzzParseContentRange(f *testing.F) {
	for _, s := range []string{"bytes 0-0/100", "bytes 0-9/*", "bytes */0", "bytes 9-0/1", "", "bytes 0-99999999999999999999/1"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		cr, err := ParseContentRange(s)
		if err != nil || cr.Unsatisfied {
			return
		}
		if cr.Range.Start < 0 || cr.Range.Start >= cr.Range.End {
			t.Fatalf("bad range %+v from %q", cr.Range, s)
		}
		if cr.TotalKnown && cr.Range.End > cr.Total {
			t.Fatalf("range %+v exceeds total %d from %q", cr.Range, cr.Total, s)
		}
	})
}
