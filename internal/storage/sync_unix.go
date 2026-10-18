//go:build unix

package storage

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// syncDir fsyncs a directory so a completed rename survives a crash.
// Filesystems that cannot sync directories (EINVAL) are tolerated.
func syncDir(dir string) error {
	d, err := os.Open(dir) //nolint:gosec // directory chosen by the caller
	if err != nil {
		return err
	}
	err = d.Sync()
	_ = d.Close()
	if errors.Is(err, unix.EINVAL) {
		return nil
	}
	return err
}
