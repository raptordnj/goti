package storage

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/raptordnj/goti/internal/domain"
)

const maxRenameAttempts = 1000

// Finalize moves tempPath to destPath according to policy and returns the
// path the file ended up at. Unless policy is PolicyOverwrite, an existing
// file is never replaced: the move is atomic and fails on conflict. The
// parent directory is synced afterwards where the OS supports it.
//
// A non-empty finalPath together with a non-nil err means the file WAS
// placed at finalPath but the follow-up step failed (directory sync, or
// removing the temp file after a link-based move): the caller must not
// retry, and should treat the file as present but possibly not durable.
func Finalize(tempPath, destPath string, policy domain.ExistingFilePolicy) (finalPath string, err error) {
	return finalizeWith(tempPath, destPath, policy, syncDir)
}

func finalizeWith(tempPath, destPath string, policy domain.ExistingFilePolicy, sync func(string) error) (string, error) {
	dir := filepath.Dir(destPath)
	var finalPath string
	var err error
	switch policy {
	case domain.PolicyOverwrite:
		err = renameReplace(tempPath, destPath)
		finalPath = destPath
	case domain.PolicyFail:
		err = renameNoReplace(tempPath, destPath)
		finalPath = destPath
		if errors.Is(err, fs.ErrExist) {
			return "", domain.NewError(domain.KindLocal, "destination exists",
				"choose a different destination or an existing-file policy", err)
		}
	case domain.PolicyRename:
		finalPath, err = renameWithSuffix(tempPath, destPath)
	default:
		return "", domain.NewError(domain.KindPolicy, fmt.Sprintf("unknown existing-file policy %q", policy), "", nil)
	}
	if err != nil {
		var pe *placedError
		if !errors.As(err, &pe) {
			if errors.Is(err, errNoFreeName) {
				return "", domain.NewError(domain.KindLocal, "no free destination name",
					"remove some files named like the destination from "+dir, err)
			}
			return "", localError(dir, "finalize download", err)
		}
		return finalPath, localError(dir, "finalize download (file placed, cleanup failed)", err)
	}
	if err := sync(dir); err != nil {
		return finalPath, localError(dir, "sync destination directory", err)
	}
	return finalPath, nil
}

var errNoFreeName = errors.New("storage: no free destination name")

// placedError reports that a move put the file at its destination but a
// later step failed, so the destination must not be retried or replaced.
type placedError struct{ err error }

func (e *placedError) Error() string { return e.err.Error() }
func (e *placedError) Unwrap() error { return e.err }

func renameWithSuffix(tempPath, destPath string) (string, error) {
	for n := 0; n <= maxRenameAttempts; n++ {
		target := destPath
		if n > 0 {
			target = NextFreeName(destPath, n)
		}
		err := renameNoReplace(tempPath, target)
		if err == nil {
			return target, nil
		}
		var pe *placedError
		if errors.As(err, &pe) {
			return target, err // placed; never move on to another name
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", err
		}
	}
	return "", errNoFreeName
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
		return &placedError{err: err} // new exists; only the cleanup failed
	}
	return nil
}
