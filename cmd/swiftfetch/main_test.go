package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/raptordnj/swiftfetch/internal/app"
	"github.com/raptordnj/swiftfetch/internal/domain"
	"github.com/raptordnj/swiftfetch/internal/platform"
	"github.com/raptordnj/swiftfetch/tests/faultserver"
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
