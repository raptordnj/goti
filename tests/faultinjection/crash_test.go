package faultinjection

import (
	"bytes"
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/raptordnj/swiftfetch/internal/domain"
	"github.com/raptordnj/swiftfetch/internal/downloader"
	"github.com/raptordnj/swiftfetch/internal/storage"
	"github.com/raptordnj/swiftfetch/tests/faultserver"
)

const (
	envChild = "SWIFTFETCH_CRASH_CHILD"
	envURL   = "SWIFTFETCH_CRASH_URL"
	envDB    = "SWIFTFETCH_CRASH_DB"
	envDest  = "SWIFTFETCH_CRASH_DEST"
	envData  = "SWIFTFETCH_CRASH_DATA"
	envReady = "SWIFTFETCH_CRASH_READY"

	// readyTimeout bounds how long the parent waits for the child's task row.
	readyTimeout = 60 * time.Second
)

// TestCrashChild is the download process TestCrashRecovery kills. It does
// nothing unless SWIFTFETCH_CRASH_CHILD=1. All its directories come from
// the parent (env), so being killed leaks nothing outside the parent's
// t.TempDir(). After Add it creates the ready marker file the parent waits on.
func TestCrashChild(t *testing.T) {
	if os.Getenv(envChild) != "1" {
		return
	}
	db := openDB(t, os.Getenv(envDB))
	eng := newEngine(t, db, os.Getenv(envDest), os.Getenv(envData))
	task, err := eng.Add(context.Background(), downloader.AddRequest{URL: os.Getenv(envURL)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv(envReady), nil, 0o600); err != nil { //nolint:gosec // path from the parent test
		t.Fatal(err)
	}
	if err := runTask(t, eng, task.ID); err != nil {
		t.Fatal(err)
	}
}

func TestCrashRecovery(t *testing.T) {
	iterations := 20
	if testing.Short() {
		iterations = 3
	}
	var totalDone int
	for s := uint64(1); s <= uint64(iterations); s++ {
		t.Run(fmt.Sprintf("seed%02d", s), func(t *testing.T) {
			totalDone += crashAndRecover(t, s)
		})
	}
	// Proves the I1 check was not vacuous: some crash left done records.
	if totalDone == 0 {
		t.Fatal("no iteration recorded a done segment before the kill; I1 was never exercised")
	}
}

// crashAndRecover kills a child download at a seeded point and recovers it
// in-process; it returns the number of done segments verified.
func crashAndRecover(t *testing.T, s uint64) int {
	const size = 32 << 20
	srv := faultserver.New(t, faultserver.Options{
		Size: size, Seed: seed, ETag: "v1", BytesPerSecondGlobal: 8_000_000,
	})
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "swiftfetch.db")
	dest, dataDir := filepath.Join(tmp, "dest"), filepath.Join(tmp, "data")
	ready := filepath.Join(tmp, "ready")
	for _, d := range []string{dest, dataDir} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	rng := rand.New(rand.NewPCG(s, 26)) //nolint:gosec // seeded for reproducible kill delays
	delay := 300*time.Millisecond + time.Duration(rng.Int64N(int64(3200*time.Millisecond)+1))

	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashChild$", "-test.count=1") //nolint:gosec // re-exec of this test binary
	cmd.Env = append(os.Environ(), envChild+"=1", envURL+"="+srv.URL+"/file", envDB+"="+dbPath,
		envDest+"="+dest, envData+"="+dataDir, envReady+"="+ready)
	var out syncBuffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	// The kill delay counts from the child's task row existing, not from
	// process start (which is slow under -race).
	if !waitReady(ready, exited) {
		_ = cmd.Process.Kill()
		<-exited
		t.Fatalf("child never became ready; output:\n%s", out.String())
	}
	select {
	case <-time.After(delay):
	case <-exited:
		t.Fatalf("child exited before the kill at %v; output:\n%s", delay, out.String())
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill child: %v; output:\n%s", err, out.String())
	}
	<-exited

	db := openDB(t, dbPath)
	tasks, err := db.ListTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	eng := newEngine(t, db, dest, dataDir)
	if len(tasks) == 0 {
		t.Logf("no task after crash (killed before Add committed); checking a fresh download; child output:\n%s", out.String())
		task, err := eng.Add(context.Background(), downloader.AddRequest{URL: srv.URL + "/file"})
		if err != nil {
			t.Fatal(err)
		}
		if err := runTask(t, eng, task.ID); err != nil {
			t.Fatalf("fresh run: %v", err)
		}
		assertCompleted(t, db, task.ID, size)
		return 0
	}
	if len(tasks) != 1 {
		t.Fatalf("found %d tasks after crash, want 1", len(tasks))
	}
	task := tasks[0]
	done := verifyDoneSegments(t, db, task)
	t.Logf("killed %v after ready", delay)

	if err := runTask(t, eng, task.ID); err != nil {
		t.Fatalf("recovery run: %v", err)
	}
	assertCompleted(t, db, task.ID, size)
	return done
}

// waitReady polls for the ready marker until it exists, the child exits,
// or readyTimeout passes.
func waitReady(path string, exited <-chan struct{}) bool {
	deadline := time.After(readyTimeout)
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		select {
		case <-exited:
			return false
		case <-deadline:
			return false
		case <-tick.C:
		}
	}
}

// syncBuffer is a bytes.Buffer safe for the child's concurrent stdout and
// stderr copiers and the parent's reads.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// verifyDoneSegments asserts invariant I1 after a crash: every segment
// recorded done hashes, over [Start,End) of the temp file, to its stored
// Hash. This is the check recoverSegments makes for done records. It
// returns the number of done segments.
func verifyDoneSegments(t *testing.T, store downloader.Store, task *domain.Task) int {
	t.Helper()
	recs, err := store.LoadSegments(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	var done, inProgress int
	for _, r := range recs {
		if !r.Done {
			if r.Pos > r.Start {
				inProgress++
			}
			continue
		}
		done++
		f, err := os.Open(task.TempPath)
		if err != nil {
			t.Fatalf("segment [%d,%d) recorded done but temp file unreadable: %v", r.Start, r.End, err)
		}
		got, err := storage.HashRange(f, domain.ByteRange{Start: r.Start, End: r.End})
		_ = f.Close()
		if err != nil {
			t.Fatalf("hash done segment [%d,%d): %v", r.Start, r.End, err)
		}
		if !bytes.Equal(got, r.Hash) {
			t.Fatalf("I1 violated: segment [%d,%d) recorded done but its bytes do not hash to the stored hash", r.Start, r.End)
		}
	}
	t.Logf("state=%s segments=%d done=%d (verified) in-progress=%d", task.State, len(recs), done, inProgress)
	return done
}
