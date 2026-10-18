//go:build unix

package storage

import "os"

// renameReplace renames old to new, replacing new if it exists.
func renameReplace(old, newPath string) error { return os.Rename(old, newPath) }
