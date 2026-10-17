package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/raptordnj/swiftfetch/internal/domain"
	"github.com/raptordnj/swiftfetch/internal/events"
)

const maxNameRunes = 40

// renderProgress formats one progress line (spec §15):
// name  42.0%  420.0 MiB / 1.0 GiB  12.3 MiB/s  ETA 0:48  conns 4  multipart
func renderProgress(p events.Progress, name string, strategy domain.Strategy) string {
	var b strings.Builder
	b.WriteString(truncateName(name))
	if p.TotalKnown && p.Total > 0 {
		fmt.Fprintf(&b, "  %.1f%%  %s / %s", float64(p.Downloaded)*100/float64(p.Total), humanBytes(p.Downloaded), humanBytes(p.Total))
	} else {
		fmt.Fprintf(&b, "  %s", humanBytes(p.Downloaded))
	}
	fmt.Fprintf(&b, "  %s/s  ETA %s  conns %d  %s", humanBytes(int64(p.SpeedBps)), formatETA(p.ETA), p.ActiveConns, strategy)
	return b.String()
}

func truncateName(name string) string {
	r := []rune(name)
	if len(r) <= maxNameRunes {
		return name
	}
	return string(r[:maxNameRunes-1]) + "…"
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	f := float64(n)
	for _, u := range []string{"KiB", "MiB", "GiB", "TiB"} {
		f /= unit
		if f < unit || u == "TiB" {
			return fmt.Sprintf("%.1f %s", f, u)
		}
	}
	return ""
}

func formatETA(eta *time.Duration) string {
	if eta == nil {
		return "—"
	}
	s := int64(eta.Round(time.Second) / time.Second)
	if s < 0 {
		s = 0
	}
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s%3600/60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// parseRate parses a bytes/s rate: a bare number, or a number with a binary
// K, M or G suffix (1024 based).
func parseRate(s string) (int64, error) {
	orig := s
	mult := int64(1)
	if s != "" {
		switch s[len(s)-1] {
		case 'K', 'k':
			mult, s = 1<<10, s[:len(s)-1]
		case 'M', 'm':
			mult, s = 1<<20, s[:len(s)-1]
		case 'G', 'g':
			mult, s = 1<<30, s[:len(s)-1]
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid rate %q (use e.g. 512K, 2M, 1G or a byte count)", orig)
	}
	if n > (1<<63-1)/mult {
		return 0, errors.New("rate " + strconv.Quote(orig) + " is too large")
	}
	return n * mult, nil
}
