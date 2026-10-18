package platform

import (
	"testing"
	"time"
	"unicode/utf8"
)

func TestFormatBytes(t *testing.T) {
	for in, want := range map[int64]string{0: "0 B", 512: "512 B", 1023: "1023 B", 1536: "1.5 KiB", 2516582: "2.4 MiB", 1 << 30: "1.0 GiB"} {
		if got := FormatBytes(in); got != want {
			t.Errorf("FormatBytes(%d)=%q want %q", in, got, want)
		}
	}
}

func TestFormatSpeed(t *testing.T) {
	if got := FormatSpeed(2516582); got != "2.4 MiB/s" {
		t.Errorf("got %q", got)
	}
}

func TestFormatETA(t *testing.T) {
	for in, want := range map[time.Duration]string{
		45 * time.Second: "45 s", -5 * time.Second: "0 s", 179 * time.Second: "2 min", 3 * time.Minute: "3 min",
		65 * time.Minute: "1 h 05 min", time.Hour: "1 h 00 min",
	} {
		if got := FormatETA(in); got != want {
			t.Errorf("FormatETA(%v)=%q want %q", in, got, want)
		}
	}
}

func TestMiddleEllipsis(t *testing.T) {
	if got := MiddleEllipsis("short.zip", 20); got != "short.zip" {
		t.Errorf("got %q", got)
	}
	if got := MiddleEllipsis("averyverylongfilename.zip", 12); got != "averyver….zip" && got != "averyve….zip" {
		t.Errorf("got %q", got)
	}
	if got := MiddleEllipsis("abcdef", 1); got != "…" {
		t.Errorf("got %q", got)
	}
}

func TestMiddleEllipsisRunes(t *testing.T) {
	for _, s := range []string{"日本語のとても長いファイル名.zip", "😀😀😀😀😀😀😀😀😀😀.png", "abcdefghij.verylongextension"} {
		for n := 0; n < 14; n++ {
			got := MiddleEllipsis(s, n)
			if !utf8.ValidString(got) {
				t.Fatalf("invalid utf8 %q", got)
			}
			if n >= 2 && utf8.RuneCountInString(got) > n {
				t.Fatalf("%q n=%d too long: %q", s, n, got)
			}
		}
	}
	if got := MiddleEllipsis("日本語のとても長いファイル名.zip", 8); got != "日本語….zip" {
		t.Errorf("got %q", got)
	}
}
