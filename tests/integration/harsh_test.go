package integration

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/raptordnj/swiftfetch/internal/config"
	"github.com/raptordnj/swiftfetch/internal/domain"
	"github.com/raptordnj/swiftfetch/internal/downloader"
	"github.com/raptordnj/swiftfetch/internal/events"
	"github.com/raptordnj/swiftfetch/tests/faultserver"
)

// heapBound is the Task 27 bound: in-use heap above the pre-run baseline.
const heapBound = 4 << 20

// TestHarshNetwork is the spec §18.2 harsh-network soak: 20% dropped
// responses, 800 ms latency, low bandwidth, one outage and one blackhole.
// Runs are sequential so the heap measurement sees only one download.
func TestHarshNetwork(t *testing.T) {
	size, rate := 4*mib, int64(64<<10)
	outage := faultserver.Outage{After: 5 * time.Second, Duration: 15 * time.Second}
	seeds := 5
	if testing.Short() {
		size, rate = 512<<10, 256<<10
		outage = faultserver.Outage{After: time.Second, Duration: 3 * time.Second}
		seeds = 2
	}
	modes := []struct {
		name string
		opts func(*faultserver.Options)
		want domain.Strategy
	}{
		{"single", func(o *faultserver.Options) { o.IgnoreRanges = true }, domain.StrategySingle},
		{"multipart", func(o *faultserver.Options) { o.ETag = "v1" }, domain.StrategyMultipart},
	}
	for _, m := range modes {
		t.Run(m.name, func(t *testing.T) {
			for s := 1; s <= seeds; s++ {
				t.Run(fmt.Sprintf("seed-%d", s), func(t *testing.T) {
					opts := faultserver.Options{
						Size: size, Seed: uint64(s), //nolint:gosec // s in 1..5
						DropProbability: 0.2, DropPerResponse: true,
						WriteLatency:         800 * time.Millisecond,
						BytesPerSecondGlobal: rate,
						Outages:              []faultserver.Outage{outage},
						BlackholeOnNth:       4,
					}
					m.opts(&opts)
					runHarsh(t, opts, m.want)
				})
			}
		})
	}
}

// runHarsh downloads once under opts. A multipart run lowers
// MinMultipartSize to its 1 MiB floor so the 4 MiB soak file is split; the
// 512 KiB -short file stays below it and runs single-stream with ranges.
func runHarsh(t *testing.T, opts faultserver.Options, want domain.Strategy) {
	h := newHarness(t, opts, harnessOpts{
		cfg: func(c *config.Config) {
			c.StallTimeout = 5 * time.Second
			c.MinMultipartSize = 1 << 20
		},
		offlineBaseDelay: 500 * time.Millisecond,
	})
	task := h.add(t, downloader.AddRequest{})

	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	baseline := ms.HeapInuse
	peakc := make(chan uint64, 1)
	ctx, stop := context.WithCancel(context.Background())
	go func() {
		peak := baseline
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				peakc <- peak
				return
			case <-tick.C:
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				peak = max(peak, m.HeapInuse)
			}
		}
	}()
	start := time.Now()
	task = h.run(t, task.ID, 20*time.Minute)
	stop()
	peak := <-peakc
	t.Logf("elapsed %s, strategy %s, heap peak +%d KiB", time.Since(start).Round(time.Millisecond), task.Strategy, (peak-baseline)>>10)

	if task.State != domain.StateCompleted {
		t.Fatalf("state %s, want completed (kind %s, msg %q)", task.State, task.LastErrorKind, task.LastErrorMsg)
	}
	if opts.Size < 1<<20 {
		want = domain.StrategySingle
	}
	if task.Strategy != want {
		t.Errorf("strategy %s, want %s", task.Strategy, want)
	}
	sum, n := fileSHA256(t, task.FinalPath)
	if n != opts.Size || sum != h.expectedSHA256(opts.Size) {
		t.Errorf("file size %d (want %d) or SHA-256 differs from the generator", n, opts.Size)
	}
	for _, ev := range h.events() {
		if e, ok := ev.(events.StateChanged); ok && e.ID == task.ID && e.To == domain.StateFailed {
			t.Errorf("task entered failed: %+v", e)
		}
	}
	if peak > baseline+heapBound {
		t.Errorf("heap peak %d B above baseline, bound %d", peak-baseline, heapBound)
	}
	atts, err := h.db.ListAttempts(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range atts {
		if strings.Contains(strings.ToLower(a.Message), "budget") {
			t.Errorf("retry budget exhausted: %s", a.Message)
		}
	}
	t.Logf("%d attempts recorded", len(atts))
}
