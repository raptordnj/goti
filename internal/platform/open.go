package platform

import (
	"os/exec"
	"path/filepath"
)

// startCmd starts a process without a shell and reaps it in the background.
var startCmd = func(name string, args ...string) error {
	c := exec.Command(name, args...)
	if err := c.Start(); err != nil {
		return err
	}
	go func() { _ = c.Wait() }()
	return nil
}

func parentDir(p string) string { return filepath.Dir(p) }
