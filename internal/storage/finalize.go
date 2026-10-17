package storage

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/raptordnj/swiftfetch/internal/domain"
)

const maxRenameAttempts = 1000

// Finalize moves tempPath to destPath according to policy and returns the
// path the file ended up at. Unless policy is PolicyOverwrite, an existing
// file is never replaced: the move is atomic and fails on conflict. The
// parent directory is synced afterwards where the OS supports it.
func Finalize(tempPath, destPath string, policy domain.ExistingFilePolicy) (finalPath string, err error) {
	dir := filepath.Dir(destPath)
	switch policy {
	case domain.PolicyOverwrite:
		if err := renameReplace(tempPath, destPath); err != nil {
			return "", localError(dir, "finalize download", err)
		}
		finalPath = destPath
	case domain.PolicyFail:
		if err := renameNoReplace(tempPath, destPath); err != nil {
			if errors.Is(err, fs.ErrExist) {
				return "", domain.NewError(domain.KindLocal, "destination exists",
					"choose a different destination or an existing-file policy", err)
			}
			return "", localError(dir, "finalize download", err)
		}
		finalPath = destPath
	case domain.PolicyRename:
		finalPath, err = renameWithSuffix(tempPath, destPath)
		if err != nil {
			return "", err
		}
	default:
		return "", domain.NewError(domain.KindPolicy, fmt.Sprintf("unknown existing-file policy %q", policy), "", nil)
	}
	if err := syncDir(dir); err != nil {
		return "", localError(dir, "sync destination directory", err)
	}
	return finalPath, nil
}

func renameWithSuffix(tempPath, destPath string) (string, error) {
	dir := filepath.Dir(destPath)
	var lastErr error
	for n := 0; n <= maxRenameAttempts; n++ {
		target := destPath
		if n > 0 {
			target = NextFreeName(destPath, n)
		}
		err := renameNoReplace(tempPath, target)
		if err == nil {
			return target, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", localError(dir, "finalize download", err)
		}
		lastErr = err
	}
	return "", domain.NewError(domain.KindLocal, "no free destination name",
		"remove some files named like the destination from "+dir, lastErr)
}

// NextFreeName returns path with " (n)" inserted before the extension, so
// /d/a.tar.gz with n=1 becomes /d/a (1).tar.gz. The compound extensions
// .tar.gz, .tar.xz, .tar.bz2 and .tar.zst count as one extension.
func NextFreeName(path string, n int) string {
	dir, name := filepath.Split(path)
	stem, ext := splitExt(name)
	return dir + fmt.Sprintf("%s (%d)%s", stem, n, ext)
}

// linkRename moves old to new without replacing new: a hard link (which
// fails if new exists) followed by removal of old. It is the fallback for
// filesystems without a native no-replace rename.
func linkRename(old, newPath string) error {
	if err := os.Link(old, newPath); err != nil {
		return err
	}
	if err := os.Remove(old); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
