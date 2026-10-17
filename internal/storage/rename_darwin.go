package storage

import (
	"errors"

	"golang.org/x/sys/unix"
)

// renameNoReplace atomically renames old to new, failing with an error
// satisfying errors.Is(err, fs.ErrExist) if new exists.
func renameNoReplace(old, newPath string) error {
	err := unix.RenamexNp(old, newPath, unix.RENAME_EXCL)
	if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.ENOSYS) {
		return linkRename(old, newPath)
	}
	return err
}
