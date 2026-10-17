package faultinjection

import (
	"bytes"
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
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
)

// TestCrashChild is the download process TestCrashRecovery kills. It does
// nothing unless SWIFTFETCH_CRASH_CHILD=1.
func TestCrashChild(t *testing.T) {
	if os.Getenv(envChild) != "1" {
		return
	}
	db := openDB(t, os.Getenv(envDB))
	eng := newEngine(t, db, os.Getenv(envDest))
	task, err := eng.Add(context.Background(), downloader.AddRequest{URL: os.Getenv(envURL)})
	if err != nil {
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
	for s := uint64(1); s <= uint64(iterations); s++ {
		t.Run(fmt.Sprintf("seed%02d", s), func(t *testing.T) {
			crashAndRecover(t, s)
		})
	}
}

func crashAndRecover(t *testing.T, s uint64) {
	const size = 32 << 20
	srv := faultserver.New(t, faultserver.Options{
		Size: size, Seed: seed, ETag: "v1", BytesPerSecondGlobal: 8_000_000,
	})
	dbPath := filepath.Join(t.TempDir(), "swiftfetch.db")
	dest := t.TempDir()
	rng := rand.New(rand.NewPCG(s, 26)) //nolint:gosec // seeded for reproducible kill delays
	delay := 300*time.Millisecond + time.Duration(rng.Int64N(int64(3200*time.Millisecond)+1))

	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashChild$", "-test.count=1") //nolint:gosec // re-exec of this test binary
	cmd.Env = append(os.Environ(), envChild+"=1", envURL+"="+srv.URL+"/file", envDB+"="+dbPath, envDest+"="+dest)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(delay)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	t.Logf("killed child after %v", delay)

	db := openDB(t, dbPath)
	tasks, err := db.ListTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("found %d tasks after crash, want 1; child output:\n%s", len(tasks), out.String())
	}
	task := tasks[0]
	verifyDoneSegments(t, db, task)

	if err := runTask(t, newEngine(t, db, dest), task.ID); err != nil {
		t.Fatalf("recovery run: %v", err)
	}
	assertCompleted(t, db, task.ID, size)
}

// verifyDoneSegments asserts invariant I1 after a crash: every segment
// recorded done hashes, over [Start,End) of the temp file, to its stored
// Hash. This is the check recoverSegments makes for done records.
func verifyDoneSegments(t *testing.T, store downloader.Store, task *domain.Task) {
	t.Helper()
	recs, err := store.LoadSegments(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	var done int
	for _, r := range recs {
		if !r.Done {
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
	t.Logf("state=%s segments=%d done=%d verified", task.State, len(recs), done)
}
