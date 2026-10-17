package storage

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/raptordnj/swiftfetch/internal/domain"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // test path
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestNextFreeName(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"/d/a.tar.gz", 1, "/d/a (1).tar.gz"},
		{"/d/a.tar.xz", 2, "/d/a (2).tar.xz"},
		{"/d/a.tar.bz2", 3, "/d/a (3).tar.bz2"},
		{"/d/a.tar.zst", 4, "/d/a (4).tar.zst"},
		{"/d/a.txt", 1, "/d/a (1).txt"},
		{"/d/noext", 1, "/d/noext (1)"},
		{"/d/a.b.txt", 2, "/d/a.b (2).txt"},
	}
	for _, c := range cases {
		got := NextFreeName(filepath.FromSlash(c.in), c.n)
		if got != filepath.FromSlash(c.want) {
			t.Errorf("NextFreeName(%q,%d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}

func TestFinalizeNoConflict(t *testing.T) {
	dir := t.TempDir()
	tmp, dest := filepath.Join(dir, "a.part"), filepath.Join(dir, "a.txt")
	writeFile(t, tmp, "data")
	final, err := Finalize(tmp, dest, domain.PolicyRename)
	if err != nil {
		t.Fatal(err)
	}
	if final != dest || readFile(t, dest) != "data" {
		t.Fatalf("final = %q", final)
	}
	if _, err := os.Stat(tmp); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("temp still present: %v", err)
	}
}

func TestFinalizeRenamePolicy(t *testing.T) {
	dir := t.TempDir()
	tmp, dest := filepath.Join(dir, "a.part"), filepath.Join(dir, "a.txt")
	writeFile(t, tmp, "new")
	writeFile(t, dest, "keep")
	final, err := Finalize(tmp, dest, domain.PolicyRename)
	if err != nil {
		t.Fatal(err)
	}
	if final != filepath.Join(dir, "a (1).txt") || readFile(t, final) != "new" || readFile(t, dest) != "keep" {
		t.Fatalf("final = %q", final)
	}
}

func TestFinalizeTarGz(t *testing.T) {
	dir := t.TempDir()
	tmp, dest := filepath.Join(dir, "x.part"), filepath.Join(dir, "x.tar.gz")
	writeFile(t, tmp, "new")
	writeFile(t, dest, "keep")
	final, err := Finalize(tmp, dest, domain.PolicyRename)
	if err != nil {
		t.Fatal(err)
	}
	if final != filepath.Join(dir, "x (1).tar.gz") {
		t.Fatalf("final = %q", final)
	}
}

func TestFinalizeFailPolicy(t *testing.T) {
	dir := t.TempDir()
	tmp, dest := filepath.Join(dir, "a.part"), filepath.Join(dir, "a.txt")
	writeFile(t, tmp, "new")
	writeFile(t, dest, "keep")
	_, err := Finalize(tmp, dest, domain.PolicyFail)
	if err == nil || domain.KindOf(err) != domain.KindLocal {
		t.Fatalf("err = %v", err)
	}
	var de *domain.Error
	if !errors.As(err, &de) || de.Msg != "destination exists" {
		t.Fatalf("err = %#v", err)
	}
	if readFile(t, tmp) != "new" || readFile(t, dest) != "keep" {
		t.Fatal("files modified")
	}
}

func TestFinalizeOverwrite(t *testing.T) {
	dir := t.TempDir()
	tmp, dest := filepath.Join(dir, "a.part"), filepath.Join(dir, "a.txt")
	writeFile(t, tmp, "new")
	writeFile(t, dest, "old")
	final, err := Finalize(tmp, dest, domain.PolicyOverwrite)
	if err != nil {
		t.Fatal(err)
	}
	if final != dest || readFile(t, dest) != "new" {
		t.Fatalf("final = %q", final)
	}
}

func TestFinalizeMissingTemp(t *testing.T) {
	dir := t.TempDir()
	_, err := Finalize(filepath.Join(dir, "nope"), filepath.Join(dir, "a"), domain.PolicyRename)
	if domain.KindOf(err) != domain.KindLocal {
		t.Fatalf("err = %v", err)
	}
}

func TestFinalizeRaceNoOverwrite(t *testing.T) {
	const n = 20
	dir := t.TempDir()
	dest := filepath.Join(dir, "a.txt")
	finals := make([]string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		tmp := filepath.Join(dir, fmt.Sprintf("t%d.part", i))
		writeFile(t, tmp, fmt.Sprintf("content-%d", i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			f, err := Finalize(tmp, dest, domain.PolicyRename)
			if err != nil {
				t.Error(err)
			}
			finals[i] = f
		}()
	}
	wg.Wait()
	seen := map[string]bool{}
	for i, f := range finals {
		if seen[f] {
			t.Fatalf("duplicate final %q", f)
		}
		seen[f] = true
		if got := readFile(t, f); got != fmt.Sprintf("content-%d", i) {
			t.Fatalf("%q has content %q", f, got)
		}
	}
	if len(seen) != n {
		t.Fatalf("got %d finals", len(seen))
	}
}

func TestLinkRenameNoReplace(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	writeFile(t, a, "A")
	writeFile(t, b, "B")
	if err := linkRename(a, b); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("err = %v", err)
	}
	if readFile(t, a) != "A" || readFile(t, b) != "B" {
		t.Fatal("files modified")
	}
	c := filepath.Join(dir, "c")
	if err := linkRename(a, c); err != nil {
		t.Fatal(err)
	}
	if readFile(t, c) != "A" {
		t.Fatal("c content")
	}
	if _, err := os.Stat(a); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("old not removed")
	}
}

func TestRenameNoReplaceConflict(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	writeFile(t, a, "A")
	writeFile(t, b, "B")
	if err := renameNoReplace(a, b); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("err = %v", err)
	}
}

func TestFinalizeSyncFailureReportsPath(t *testing.T) {
	dir := t.TempDir()
	tmp, dest := filepath.Join(dir, "a.part"), filepath.Join(dir, "a.txt")
	writeFile(t, tmp, "new")
	writeFile(t, dest, "keep")
	boom := errors.New("sync boom")
	final, err := finalizeWith(tmp, dest, domain.PolicyRename, func(string) error { return boom })
	if err == nil || domain.KindOf(err) != domain.KindLocal || !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if final != filepath.Join(dir, "a (1).txt") || readFile(t, final) != "new" {
		t.Fatalf("final = %q", final)
	}
}

func TestLinkRenameRemoveFailureIsPlaced(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs unix permissions and non-root")
	}
	src, dst := filepath.Join(t.TempDir(), "s"), t.TempDir()
	if err := os.Mkdir(src, 0o700); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(src, "a")
	writeFile(t, old, "A")
	if err := os.Chmod(src, 0o500); err != nil { //nolint:gosec // test needs unremovable file
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(src, 0o700) }) //nolint:gosec // restore for cleanup
	newPath := filepath.Join(dst, "b")
	err := linkRename(old, newPath)
	var pe *placedError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v, want placedError", err)
	}
	if readFile(t, newPath) != "A" {
		t.Fatal("link target missing")
	}
}
