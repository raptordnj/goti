package storage

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/raptordnj/swiftfetch/internal/domain"
)

func TestCreateTempPreallocates(t *testing.T) {
	dir := t.TempDir()
	tf, err := CreateTemp(dir, "big.bin", 10<<20, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tf.Close() }()
	fi, err := os.Stat(tf.Path())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() != 10<<20 {
		t.Fatalf("size = %d", fi.Size())
	}
	if !strings.HasSuffix(tf.Path(), "big.bin.swiftfetch-part") {
		t.Fatalf("path = %s", tf.Path())
	}
}

func TestCreateTempUnknownSize(t *testing.T) {
	tf, err := CreateTemp(t.TempDir(), "u", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tf.Close() }()
	if fi, _ := os.Stat(tf.Path()); fi.Size() != 0 {
		t.Fatalf("size = %d", fi.Size())
	}
}

func TestCreateTempExclusive(t *testing.T) {
	dir := t.TempDir()
	a, err := CreateTemp(dir, "f", 1, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	b, err := CreateTemp(dir, "f", 1, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close() }()
	if !strings.HasSuffix(b.Path(), " (1).swiftfetch-part") {
		t.Fatalf("second path = %s", b.Path())
	}
}

func TestConcurrentWriteAt(t *testing.T) {
	const n, blk = 16, 64 << 10
	tf, err := CreateTemp(t.TempDir(), "c", n*blk, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tf.Close() }()
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := tf.WriteAt(bytes.Repeat(pattern(i), blk), int64(i*blk)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if err := tf.Sync(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		got := make([]byte, blk)
		if _, err := tf.ReadAt(got, int64(i*blk)); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, bytes.Repeat(pattern(i), blk)) {
			t.Fatalf("block %d mismatch", i)
		}
	}
}

func testData(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7 + i>>8)
	}
	return b
}

func TestHashRange(t *testing.T) {
	data := testData(3<<20 + 123)
	r := bytes.NewReader(data)
	br := domain.ByteRange{Start: 1000, End: 2<<20 + 77}
	got, err := HashRange(r, br)
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(data[br.Start:br.End])
	if !bytes.Equal(got, want[:]) {
		t.Fatal("hash mismatch")
	}
}

func TestHashRangeShort(t *testing.T) {
	if _, err := HashRange(bytes.NewReader(make([]byte, 10)), domain.ByteRange{Start: 0, End: 20}); err == nil {
		t.Fatal("expected error on short read")
	}
}

func TestResumeHasher(t *testing.T) {
	data := testData(1 << 20)
	h := sha256.New()
	h.Write(data[:len(data)/2])
	state, err := h.(interface{ MarshalBinary() ([]byte, error) }).MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	r, err := ResumeHasher(state)
	if err != nil {
		t.Fatal(err)
	}
	r.Write(data[len(data)/2:])
	want := sha256.Sum256(data)
	if !bytes.Equal(r.Sum(nil), want[:]) {
		t.Fatal("resumed hash mismatch")
	}
	if _, err := ResumeHasher([]byte("junk")); err == nil {
		t.Fatal("expected error for bad state")
	}
}

func TestOpenTemp(t *testing.T) {
	tf, err := CreateTemp(t.TempDir(), "o", 4, true)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = tf.WriteAt([]byte("abcd"), 0)
	_ = tf.Close()
	o, err := OpenTemp(tf.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = o.Close() }()
	b := make([]byte, 4)
	if _, err := o.ReadAt(b, 0); err != nil || string(b) != "abcd" {
		t.Fatalf("%q %v", b, err)
	}
	if _, err := OpenTemp(tf.Path() + "x"); domain.KindOf(err) != domain.KindLocal {
		t.Fatalf("missing file kind = %v", domain.KindOf(err))
	}
}

func TestCreateTempReadOnlyDir(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission semantics differ on windows/root")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil { //nolint:gosec // test needs a read-only dir
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) //nolint:gosec // restore for cleanup
	_, err := CreateTemp(dir, "x", 1, true)
	if err == nil {
		t.Fatal("expected error")
	}
	if domain.KindOf(err) != domain.KindLocal {
		t.Fatalf("kind = %v", domain.KindOf(err))
	}
	var de *domain.Error
	if !errors.As(err, &de) || !strings.Contains(de.Action, dir) {
		t.Fatalf("action = %+v", de)
	}
}

// pattern returns the one-byte fill value for block i (i < 255).
func pattern(i int) []byte { return []byte{byte(i + 1)} } //nolint:gosec // i is a small test index
