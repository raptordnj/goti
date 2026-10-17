package storage

import (
	"errors"
	"os"
)

// ErrLocked is returned by TryLock when another process holds the lock.
var ErrLocked = errors.New("storage: file is locked by another process")

// FileLock is an exclusive advisory lock on a file, held across processes
// until Unlock (or process exit).
type FileLock struct {
	f *os.File
}

// TryLock opens (creating if needed) path and takes an exclusive,
// non-blocking advisory lock on it. It returns ErrLocked when another process
// already holds it. Within one process a second TryLock on the same path may
// succeed on some platforms, so callers keep their own in-process guard.
func TryLock(path string) (*FileLock, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // engine-built path in DataDir
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &FileLock{f: f}, nil
}

// Unlock releases the lock and closes the file.
func (l *FileLock) Unlock() error {
	return l.f.Close() // closing the descriptor releases flock / LockFileEx
}
