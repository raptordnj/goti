package main

import (
	"strings"
	"testing"
	"time"

	"github.com/raptordnj/goti/internal/domain"
	"github.com/raptordnj/goti/internal/events"
)

func TestRenderProgress(t *testing.T) {
	eta := 48 * time.Second
	long := strings.Repeat("a", 50)
	cases := []struct {
		name string
		p    events.Progress
		file string
		want string
	}{
		{"known", events.Progress{Downloaded: 420 << 20, Total: 1 << 30, TotalKnown: true, SpeedBps: 12.3 * (1 << 20), ActiveConns: 4, ETA: &eta},
			"file.bin", "file.bin  41.0%  420.0 MiB / 1.0 GiB  12.3 MiB/s  ETA 0:48  conns 4  multipart"},
		{"unknown total nil eta", events.Progress{Downloaded: 2048, SpeedBps: 1024},
			"x", "x  2.0 KiB  1.0 KiB/s  ETA —  conns 0  multipart"},
		{"long name", events.Progress{Downloaded: 1, Total: 2, TotalKnown: true},
			long, strings.Repeat("a", 39) + "…  50.0%  1 B / 2 B  0 B/s  ETA —  conns 0  multipart"},
	}
	for _, c := range cases {
		if got := renderProgress(c.p, c.file, domain.StrategyMultipart); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

func TestParseRate(t *testing.T) {
	ok := map[string]int64{"512K": 512 << 10, "2M": 2 << 20, "1G": 1 << 30, "100": 100}
	for in, want := range ok {
		if got, err := parseRate(in); err != nil || got != want {
			t.Errorf("parseRate(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"abc", "-1", "", "K"} {
		if _, err := parseRate(in); err == nil {
			t.Errorf("parseRate(%q) should fail", in)
		}
	}
}
