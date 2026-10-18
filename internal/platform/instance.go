package platform

import (
	"errors"
	"path/filepath"

	"github.com/raptordnj/goti/internal/storage"
)

// ErrAlreadyRunning means another Goti instance holds the lock.
var ErrAlreadyRunning = errors.New("Goti is already running")

// InstanceLock takes the single-instance lock in dataDir.
func InstanceLock(dataDir string) (*storage.FileLock, error) {
	l, err := storage.TryLock(filepath.Join(dataDir, "gui.lock"))
	if errors.Is(err, storage.ErrLocked) {
		return nil, ErrAlreadyRunning
	}
	return l, err
}
