package storage

import (
	"crypto/sha256"
	"encoding"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/raptordnj/swiftfetch/internal/domain"
)

const (
	tempSuffix     = ".swiftfetch-part"
	maxTempRetries = 1000
	hashBufSize    = 1 << 20
)

// TempFile is a download's partial file. WriteAt and ReadAt are safe for
// concurrent use because they never touch a shared file offset.
type TempFile struct {
	f      *os.File
	closed atomic.Bool
}

// CreateTemp exclusively creates "<dir>/<base>.swiftfetch-part" (mode 0644),
// falling back to "<base> (N).swiftfetch-part" when it exists. When
// sizeKnown, the file is truncated to size and, where supported,
// preallocated.
func CreateTemp(dir, base string, size int64, sizeKnown bool) (*TempFile, error) {
	if !validTempBase(base) {
		return nil, domain.NewError(domain.KindLocal, "invalid temp file name", "use a plain file name without path separators", nil)
	}
	if sizeKnown && size < 0 {
		return nil, domain.NewError(domain.KindLocal, "invalid temp file size", "", nil)
	}
	var f *os.File
	var err error
	for i := 0; i <= maxTempRetries; i++ {
		name := base + tempSuffix
		if i > 0 {
			name = fmt.Sprintf("%s (%d)%s", base, i, tempSuffix)
		}
		f, err = os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o644) //nolint:gosec // spec: temp file mode 0644
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, localError(dir, "create temp file", err)
		}
	}
	if err != nil {
		return nil, localError(dir, "create temp file", err)
	}
	if sizeKnown {
		if err := f.Truncate(size); err != nil {
			_ = f.Close()
			_ = os.Remove(f.Name())
			return nil, localError(dir, "size temp file", err)
		}
		preallocate(f, size)
	}
	return &TempFile{f: f}, nil
}

// validTempBase reports whether base is a single, plain path element.
func validTempBase(base string) bool {
	return base != "" && base != "." && base != ".." &&
		!strings.ContainsAny(base, "/\\\x00") && filepath.Base(base) == base
}

// OpenTemp opens an existing temp file for reading and writing.
func OpenTemp(path string) (*TempFile, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, localError(filepath.Dir(path), "open temp file", err)
	}
	return &TempFile{f: f}, nil
}

// WriteAt writes p at offset off.
func (t *TempFile) WriteAt(p []byte, off int64) (int, error) {
	n, err := t.f.WriteAt(p, off)
	if err != nil {
		err = localError(filepath.Dir(t.f.Name()), "write temp file", err)
	}
	return n, err
}

// ReadAt reads into p from offset off. io.EOF is returned unwrapped.
func (t *TempFile) ReadAt(p []byte, off int64) (int, error) {
	n, err := t.f.ReadAt(p, off)
	if err != nil && !errors.Is(err, io.EOF) {
		err = localError(filepath.Dir(t.f.Name()), "read temp file", err)
	}
	return n, err
}

// Sync flushes file data to stable storage.
func (t *TempFile) Sync() error {
	if err := t.f.Sync(); err != nil {
		return localError(filepath.Dir(t.f.Name()), "sync temp file", err)
	}
	return nil
}

// Close closes the file; calls after the first are no-ops.
func (t *TempFile) Close() error {
	if !t.closed.CompareAndSwap(false, true) {
		return nil
	}
	if err := t.f.Close(); err != nil {
		return localError(filepath.Dir(t.f.Name()), "close temp file", err)
	}
	return nil
}

// Path returns the temp file's path.
func (t *TempFile) Path() string { return t.f.Name() }

func localError(dir, what string, err error) *domain.Error {
	return domain.NewError(domain.KindLocal, what+": "+err.Error(),
		"check free disk space and write permission for "+dir, err)
}

// HashRange returns the SHA-256 of the bytes of r covered by br. A source
// shorter than the range is an error.
func HashRange(r io.ReaderAt, br domain.ByteRange) ([]byte, error) {
	return HashRangeBuf(r, br, make([]byte, hashBufSize))
}

// HashRangeBuf is HashRange using the caller's non-empty buf for reads, so
// repeated calls need not allocate a fresh buffer each time.
func HashRangeBuf(r io.ReaderAt, br domain.ByteRange, buf []byte) ([]byte, error) {
	h := sha256.New()
	n, err := io.CopyBuffer(h, io.NewSectionReader(r, br.Start, br.Len()), buf)
	if err != nil {
		return nil, err
	}
	if n != br.Len() {
		return nil, io.ErrUnexpectedEOF
	}
	return h.Sum(nil), nil
}

// ResumeHasher returns a SHA-256 hasher restored from a marshaled state.
func ResumeHasher(state []byte) (hash.Hash, error) {
	h := sha256.New()
	u, ok := h.(encoding.BinaryUnmarshaler)
	if !ok {
		return nil, errors.New("storage: sha256 state is not restorable")
	}
	if err := u.UnmarshalBinary(state); err != nil {
		return nil, err
	}
	return h, nil
}
