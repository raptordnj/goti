package faultinjection

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/raptordnj/goti/internal/config"
	"github.com/raptordnj/goti/internal/domain"
	"github.com/raptordnj/goti/internal/downloader"
	"github.com/raptordnj/goti/internal/events"
	"github.com/raptordnj/goti/internal/persistence"
	"github.com/raptordnj/goti/internal/ratelimit"
	"github.com/raptordnj/goti/internal/transport"
	"github.com/raptordnj/goti/tests/faultserver"
)

const seed = 26

// newEngine wires an Engine like cmd/goti-cli over store, downloading
// into dir with data dir dataDir ("" → a t.TempDir()). Its Shutdown is
// registered with t.Cleanup.
func newEngine(t *testing.T, store downloader.Store, dir, dataDir string) *downloader.Engine {
	t.Helper()
	if dataDir == "" {
		dataDir = t.TempDir()
	}
	cfg := config.Default()
	cfg.DownloadDir = dir
	cfg.DataDir = dataDir
	cfg.RetryBaseDelay, cfg.RetryMaxDelay = 5*time.Millisecond, 50*time.Millisecond
	cfg.StallTimeout = 10 * time.Second
	cfg.CheckpointInterval = 50 * time.Millisecond
	bus := events.NewBus(cfg.ProgressHz, domain.RealClock{})
	eng := downloader.NewEngine(downloader.EngineDeps{
		Store: store, Config: cfg, Bus: bus, Clock: domain.RealClock{},
		Global: ratelimit.NewBandwidth(cfg.GlobalRateLimit),
		Conns:  ratelimit.NewSemaphore(cfg.GlobalMaxConnections),
		NewClient: func(allowPrivate bool) *http.Client {
			return transport.NewClient(transport.ClientOptions{
				Policy: cfg.NetworkPolicy, AllowPrivate: allowPrivate,
				AllowHTTPSDowngrade: cfg.AllowHTTPSDowngrade,
			})
		},
		OfflineBaseDelay: 100 * time.Millisecond,
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := eng.Shutdown(ctx); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	return eng
}

func openDB(t *testing.T, path string) *persistence.DB {
	t.Helper()
	db, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close db: %v", err)
		}
	})
	return db
}

func runTask(t *testing.T, eng *downloader.Engine, id string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return eng.Run(ctx, id)
}

// assertCompleted checks the stored task completed with the expected content.
func assertCompleted(t *testing.T, store downloader.Store, id string, size int64) {
	t.Helper()
	task, err := store.GetTask(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if task.State != domain.StateCompleted {
		t.Fatalf("state = %s (%s: %s), want completed", task.State, task.LastErrorKind, task.LastErrorMsg)
	}
	f, err := os.Open(task.FinalPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		t.Fatal(err)
	}
	var got [32]byte
	copy(got[:], h.Sum(nil))
	if n != size || got != faultserver.ExpectedSHA256(seed, size) {
		t.Fatalf("final file: %d bytes, sha256 match %v; want %d bytes, match", n, got == faultserver.ExpectedSHA256(seed, size), size)
	}
}

// throttled serves size bytes slowly enough for many checkpoints.
func throttled(t *testing.T, size int64) *faultserver.Server {
	return faultserver.New(t, faultserver.Options{
		Size: size, Seed: seed, ETag: "v1", BytesPerSecondGlobal: 8 << 20,
	})
}

func TestCheckpointFailureThenRecover(t *testing.T) {
	const size = 8 << 20
	srv := throttled(t, size)
	db := openDB(t, filepath.Join(t.TempDir(), "goti.db"))
	dir := t.TempDir()
	faulty := &FaultyStore{Store: db, FailSaveCheckpointAfter: 3}
	eng := newEngine(t, faulty, dir, "")
	task, err := eng.Add(context.Background(), downloader.AddRequest{URL: srv.URL + "/file"})
	if err != nil {
		t.Fatal(err)
	}
	err = runTask(t, eng, task.ID)
	if err == nil {
		t.Fatal("Run with failing SaveCheckpoint succeeded, want error")
	}
	if !errors.Is(err, ErrInjected) {
		t.Fatalf("first run error = %v, want it to wrap ErrInjected", err)
	}
	if faulty.saveCalls.Load() < 3 {
		t.Fatalf("SaveCheckpoint called %d times, want >= 3", faulty.saveCalls.Load())
	}

	if err := runTask(t, newEngine(t, db, dir, ""), task.ID); err != nil {
		t.Fatalf("recovery run: %v", err)
	}
	assertCompleted(t, db, task.ID, size)
}

func TestUpdateTaskFailure(t *testing.T) {
	const size = 4 << 20
	srv := throttled(t, size)
	db := openDB(t, filepath.Join(t.TempDir(), "goti.db"))
	dir := t.TempDir()
	faulty := &FaultyStore{Store: db, FailUpdateTaskAfter: 2}
	eng := newEngine(t, faulty, dir, "")
	task, err := eng.Add(context.Background(), downloader.AddRequest{URL: srv.URL + "/file"})
	if err != nil {
		t.Fatal(err)
	}
	err = runTask(t, eng, task.ID)
	if err == nil {
		t.Fatal("Run with failing UpdateTask succeeded, want error")
	}
	if !errors.Is(err, ErrInjected) {
		t.Fatalf("first run error = %v, want it to wrap ErrInjected", err)
	}
	if n := faulty.updateCalls.Load(); n < 2 {
		t.Fatalf("UpdateTask called %d times, want >= 2 (fault never fired)", n)
	}

	if err := runTask(t, newEngine(t, db, dir, ""), task.ID); err != nil {
		t.Fatalf("recovery run: %v", err)
	}
	assertCompleted(t, db, task.ID, size)
}
