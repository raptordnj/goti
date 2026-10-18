package platform

import (
	"errors"
	"path/filepath"

	"github.com/raptordnj/swiftfetch/internal/storage"
)

// ErrAlreadyRunning means another SwiftFetch instance holds the lock.
var ErrAlreadyRunning = errors.New("SwiftFetch is already running")

// InstanceLock takes the single-instance lock in dataDir.
func InstanceLock(dataDir string) (*storage.FileLock, error) {
	l, err := storage.TryLock(filepath.Join(dataDir, "swiftfetch.lock"))
	if errors.Is(err, storage.ErrLocked) {
		return nil, ErrAlreadyRunning
	}
	return l, err
}
