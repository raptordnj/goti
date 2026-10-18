package platform

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestInstanceLockTwice(t *testing.T) {
	dir := t.TempDir()
	l, err := InstanceLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Unlock()
	if _, err := os.Stat(filepath.Join(dir, "gui.lock")); err != nil {
		t.Fatal(err)
	}
	if _, err := InstanceLock(dir); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("want ErrAlreadyRunning, got %v", err)
	}
}

func TestOpenPathArgs(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only args")
	}
	old := startCmd
	defer func() { startCmd = old }()
	var name string
	var args []string
	startCmd = func(n string, a ...string) error { name, args = n, a; return nil }
	if err := OpenPath("/tmp/a b.txt"); err != nil {
		t.Fatal(err)
	}
	if name != "xdg-open" || !reflect.DeepEqual(args, []string{"/tmp/a b.txt"}) {
		t.Errorf("got %s %v", name, args)
	}
	if err := RevealPath("/tmp/a b.txt"); err != nil {
		t.Fatal(err)
	}
	if name != "xdg-open" || !reflect.DeepEqual(args, []string{"/tmp"}) {
		t.Errorf("got %s %v", name, args)
	}
}
