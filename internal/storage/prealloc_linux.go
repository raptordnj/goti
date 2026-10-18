package storage

import (
	"os"

	"golang.org/x/sys/unix"
)

// preallocate reserves disk blocks without changing the file size. It is
// best-effort: filesystems that lack support simply return an error, which
// is ignored.
func preallocate(f *os.File, size int64) {
	if size <= 0 {
		return
	}
	_ = unix.Fallocate(int(f.Fd()), unix.FALLOC_FL_KEEP_SIZE, 0, size)
}
