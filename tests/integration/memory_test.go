package integration

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/raptordnj/swiftfetch/internal/domain"
	"github.com/raptordnj/swiftfetch/internal/downloader"
	"github.com/raptordnj/swiftfetch/tests/faultserver"
)

// TestMemoryBound is acceptance criterion 7: a 1 GiB, 8-connection download
// keeps in-use heap within heapBound of the pre-download baseline.
func TestMemoryBound(t *testing.T) {
	if testing.Short() {
		t.Skip("1 GiB download")
	}
	const size = 1 << 30
	h := newHarness(t, faultserver.Options{Size: size, ETag: "v1"}, harnessOpts{})
	task := h.add(t, downloader.AddRequest{Connections: 8})

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
	task = h.run(t, task.ID, 10*time.Minute)
	stop()
	peak := <-peakc
	t.Logf("baseline %d KiB, peak +%d KiB", baseline>>10, (peak-baseline)>>10)

	if task.State != domain.StateCompleted {
		t.Fatalf("state %s, want completed", task.State)
	}
	if task.Strategy != domain.StrategyMultipart {
		t.Errorf("strategy %s, want multipart", task.Strategy)
	}
	sum, n := fileSHA256(t, task.FinalPath)
	if n != size || sum != h.expectedSHA256(size) {
		t.Errorf("file size %d or SHA-256 differs from the generator", n)
	}
	if peak > baseline+heapBound {
		t.Errorf("heap peak %d B above baseline, bound %d", peak-baseline, heapBound)
	}
}

// TestCLIRSS bounds the CLI process's resident set: ≤ 20 MiB before data
// flows and ≤ 40 MiB peak during a throttled 8-connection download.
func TestCLIRSS(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc")
	}
	if testing.Short() {
		t.Skip("builds the CLI and downloads 256 MiB")
	}
	const (
		size     = 256 << 20
		idleMax  = 20 << 20
		peakMax  = 40 << 20
		perConnS = 4 << 20
	)
	bin := filepath.Join(t.TempDir(), "swiftfetch-cli")
	build := exec.Command("go", "build", "-o", bin, "github.com/raptordnj/swiftfetch/cmd/swiftfetch-cli") //nolint:gosec // temp output path
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	srv := faultserver.New(t, faultserver.Options{
		Size: size, Seed: seed, ETag: "v1",
		BytesPerSecondPerConn: perConnS,
		// Delay the first body byte so the first samples see the idle process.
		WriteLatency: 500 * time.Millisecond,
	})
	dest := t.TempDir()
	cmd := exec.Command(bin, "get", srv.URL+"/file", "-o", dest, "--conns", "8") //nolint:gosec // test binary
	cmd.Env = append(os.Environ(), "SWIFTFETCH_DATA_DIR="+t.TempDir())
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitc := make(chan error, 1)
	go func() { waitc <- cmd.Wait() }()

	status := fmt.Sprintf("/proc/%d/status", cmd.Process.Pid)
	var idle, peak int64 = -1, 0
	samples := 0
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	deadline := time.After(5 * time.Minute)
	var werr error
loop:
	for {
		select {
		case werr = <-waitc:
			break loop
		case <-deadline:
			_ = cmd.Process.Kill()
			t.Fatalf("CLI did not finish\n%s", stderr.String())
		case <-tick.C:
			rss, ok := vmRSS(status)
			if !ok {
				continue
			}
			samples++
			if idle < 0 {
				idle = rss
			}
			peak = max(peak, rss)
		}
	}
	t.Logf("%d samples: idle %d KiB, peak %d KiB", samples, idle>>10, peak>>10)
	if werr != nil {
		t.Fatalf("CLI: %v\n%s", werr, stderr.String())
	}
	if samples < 10 {
		t.Fatalf("only %d RSS samples", samples)
	}
	if idle > idleMax {
		t.Errorf("idle RSS %d KiB > %d KiB", idle>>10, idleMax>>10)
	}
	if peak > peakMax {
		t.Errorf("peak RSS %d KiB > %d KiB", peak>>10, peakMax>>10)
	}
	ents, err := os.ReadDir(dest)
	if err != nil || len(ents) != 1 {
		t.Fatalf("dest entries %v (%v)", ents, err)
	}
	sum, n := fileSHA256(t, filepath.Join(dest, ents[0].Name()))
	if n != size || sum != faultserver.ExpectedSHA256(seed, size) {
		t.Errorf("file size %d or SHA-256 differs from the generator", n)
	}
}

// vmRSS returns VmRSS in bytes from a /proc/<pid>/status file.
func vmRSS(path string) (int64, bool) {
	f, err := os.Open(path) //nolint:gosec // /proc path
	if err != nil {
		return 0, false
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "VmRSS:"); ok {
			kb, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(v), " kB"), 10, 64)
			return kb << 10, err == nil
		}
	}
	return 0, false
}
