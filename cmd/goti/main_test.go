package main

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/raptordnj/goti/internal/app"
	"github.com/raptordnj/goti/internal/domain"
	"github.com/raptordnj/goti/internal/platform"
	"github.com/raptordnj/goti/tests/faultserver"
)

func TestMainWiringHeadless(t *testing.T) {
	srv := faultserver.New(t, faultserver.Options{Size: 256 << 10, Seed: 7})
	dir := t.TempDir()
	rt, err := build(dir)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- rt.run() }()

	if _, err := build(dir); !errors.Is(err, platform.ErrAlreadyRunning) {
		t.Fatalf("second build: %v", err)
	}

	dest := filepath.Join(dir, "out")
	if err := os.MkdirAll(dest, 0o700); err != nil {
		t.Fatal(err)
	}
	if !rt.app.Send(app.AddDownload{URL: srv.URL + "/file", DestDir: dest, FileName: "f.bin"}) {
		t.Fatal("send failed")
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		snap := rt.store.Load()
		if len(snap.Tasks) == 1 && snap.Tasks[0].State == domain.StateCompleted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not completed: %+v toasts=%+v", snap.Tasks, snap.Toasts)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if fi, err := os.Stat(filepath.Join(dest, "f.bin")); err != nil || fi.Size() != 256<<10 {
		t.Fatalf("file: %v %v", fi, err)
	}
	rt.app.Send(app.Quit{})
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("run did not return after Quit")
	}
	// Lock released: a new build succeeds.
	rt2, err := build(dir)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	rt2.close()
}

func TestDataDirFlagOverridesEnv(t *testing.T) {
	if got := dataDirFor("/flag", map[string]string{"GOTI_DATA_DIR": "/env"}, nil); got != "/flag" {
		t.Errorf("got %q", got)
	}
	if got := dataDirFor("", map[string]string{"GOTI_DATA_DIR": "/env"}, nil); got != "/env" {
		t.Errorf("got %q", got)
	}
}

// TestForwardSignalsRetriesFullBuffer: a signal is not dropped while the
// command buffer is briefly full.
func TestForwardSignalsRetriesFullBuffer(t *testing.T) {
	var calls, quits atomic.Int64
	send := func(c app.Command) bool {
		if calls.Add(1) <= 3 {
			return false
		}
		if _, ok := c.(app.Quit); ok {
			quits.Add(1)
		}
		return true
	}
	sigs := make(chan os.Signal, 1)
	sigs <- os.Interrupt
	close(sigs)
	forwardSignals(sigs, send, slog.New(slog.DiscardHandler))
	if quits.Load() != 1 {
		t.Fatalf("quits %d after %d sends", quits.Load(), calls.Load())
	}
}

func TestStartupMessage(t *testing.T) {
	if got := startupMessage(platform.ErrAlreadyRunning); got != platform.ErrAlreadyRunning.Error() {
		t.Fatalf("lock: %q", got)
	}
	if got := startupMessage(errors.New("cannot open database: boom")); got != "goti: cannot open database: boom" {
		t.Fatalf("db: %q", got)
	}
}
