package storage

import (
	"errors"

	"golang.org/x/sys/unix"
)

// renameNoReplace atomically renames old to new, failing with an error
// satisfying errors.Is(err, fs.ErrExist) if new exists.
func renameNoReplace(old, newPath string) error {
	err := unix.Renameat2(unix.AT_FDCWD, old, unix.AT_FDCWD, newPath, unix.RENAME_NOREPLACE)
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) {
		return linkRename(old, newPath)
	}
	return err
}
