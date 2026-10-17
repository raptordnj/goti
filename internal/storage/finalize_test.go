package storage

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
