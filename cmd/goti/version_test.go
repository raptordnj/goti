package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestVersionFlag(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "goti")
	build := exec.Command("go", "build", "-ldflags", "-X main.version=1.2.3", "-o", bin, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=1")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	out, err := exec.Command(bin, "--version").Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, want := string(out), "goti 1.2.3\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
