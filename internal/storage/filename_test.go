package storage

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitizeFilename(t *testing.T) {
	long := strings.Repeat("é", 150) + ".tar.gz"
	cases := []struct{ in, want string }{
		{"../../etc/passwd", "passwd"},
		{"a/b\\c.txt", "c.txt"},
		{"...", "download"},
		{".bashrc", "bashrc"},
		{"CON", "CON_"},
		{"con.txt", "con_.txt"},
		{"LPT9.log", "LPT9_.log"},
		{"a<b>:c|d?e*.txt", "a_b__c_d_e_.txt"},
		{"name. . ", "name"},
		{"\x00\x1fx\u0085y", "xy"},
		{"", "download"},
		{"ok name.txt", "ok name.txt"},
	}
	for _, c := range cases {
		if got := SanitizeFilename(c.in); got != c.want {
			t.Errorf("SanitizeFilename(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	got := SanitizeFilename(long)
	if len(got) > maxNameBytes || !utf8.ValidString(got) || !strings.HasSuffix(got, ".tar.gz") {
		t.Errorf("long name: len=%d valid=%v got=%q", len(got), utf8.ValidString(got), got)
	}
}

func TestSafeJoin(t *testing.T) {
	if _, err := SafeJoin("/d", "x"); err != nil {
		t.Fatalf("SafeJoin ok case: %v", err)
	}
	for _, bad := range []string{"..", ".", "", "../x"} {
		if _, err := SafeJoin("/d", bad); err == nil {
			t.Errorf("SafeJoin(%q) expected error", bad)
		}
	}
}

func FuzzSanitizeFilename(f *testing.F) {
	for _, s := range []string{"../../etc/passwd", "CON", "a b.", "\xff\xfe", "é.tar.gz", "...", "con .txt", strings.Repeat("a", 300)} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out := SanitizeFilename(in)
		if out == "" || !utf8.ValidString(out) || len(out) > maxNameBytes {
			t.Fatalf("bad output %q for %q", out, in)
		}
		if strings.ContainsAny(out, "/\\\x00") {
			t.Fatalf("forbidden char in %q", out)
		}
		if _, err := SafeJoin(t.TempDir(), out); err != nil {
			t.Fatalf("SafeJoin(%q): %v", out, err)
		}
	})
}
