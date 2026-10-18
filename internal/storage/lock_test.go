package storage

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestTryLockAcrossProcesses holds the lock in a child process and checks
// that this process is refused until the child exits.
func TestTryLockAcrossProcesses(t *testing.T) {
	if p := os.Getenv("GOTI_LOCK_HELPER"); p != "" {
		l, err := TryLock(p)
		if err != nil {
			os.Exit(3)
		}
		_, _ = os.Stdout.WriteString("locked\n")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n') // hold until stdin closes
		_ = l.Unlock()
		os.Exit(0)
	}
	path := filepath.Join(t.TempDir(), "task.lock")
	cmd := exec.Command(os.Args[0], "-test.run=^TestTryLockAcrossProcesses$") //nolint:gosec // re-exec of the test binary
	cmd.Env = append(os.Environ(), "GOTI_LOCK_HELPER="+path)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if line, _ := bufio.NewReader(stdout).ReadString('\n'); line != "locked\n" {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("helper did not lock: %q", line)
	}
	if _, err := TryLock(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("TryLock while child holds it: %v, want ErrLocked", err)
	}
	_ = stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	l, err := TryLock(path)
	if err != nil {
		t.Fatalf("TryLock after child exit: %v", err)
	}
	if err := l.Unlock(); err != nil {
		t.Fatal(err)
	}
}
