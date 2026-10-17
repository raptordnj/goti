package storage

import "golang.org/x/sys/windows"

// renameNoReplace renames old to new using MoveFileEx without
// MOVEFILE_REPLACE_EXISTING, so an existing destination yields an error
// satisfying errors.Is(err, fs.ErrExist).
func renameNoReplace(old, newPath string) error {
	return moveFileEx(old, newPath, windows.MOVEFILE_WRITE_THROUGH)
}

// renameReplace renames old to new, replacing new if it exists.
func renameReplace(old, newPath string) error {
	return moveFileEx(old, newPath, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

func moveFileEx(old, newPath string, flags uint32) error {
	from, err := windows.UTF16PtrFromString(old)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(newPath)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, flags)
}
