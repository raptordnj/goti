package config

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/raptordnj/goti/internal/storage"
)

func testLog() (*slog.Logger, *bytes.Buffer) {
	var b bytes.Buffer
	return slog.New(slog.NewTextHandler(&b, nil)), &b
}

func TestMigrateDataDir(t *testing.T) {
	t.Run("old exists new missing renames", func(t *testing.T) {
		root := t.TempDir()
		old, nw := filepath.Join(root, "swift"), filepath.Join(root, "goti")
		if err := os.MkdirAll(old, 0o700); err != nil {
			t.Fatal(err)
		}
		db, err := sql.Open("sqlite", filepath.Join(old, LegacyDBName))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("CREATE TABLE t(x)"); err != nil {
			t.Fatal(err)
		}
		_ = db.Close()
		log, buf := testLog()
		got, err := MigrateDataDir(old, nw, log)
		if err != nil || got != nw {
			t.Fatalf("got %q, %v; want %q", got, err, nw)
		}
		if _, err := os.Stat(old); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("old dir still exists: %v", err)
		}
		if _, err := os.Stat(filepath.Join(nw, LegacyDBName)); err != nil {
			t.Errorf("db not moved with dir: %v", err)
		}
		if !strings.Contains(buf.String(), "migrated data from") {
			t.Errorf("log = %q", buf.String())
		}
	})
	t.Run("both exist uses new and leaves old", func(t *testing.T) {
		root := t.TempDir()
		old, nw := filepath.Join(root, "swift"), filepath.Join(root, "goti")
		for _, d := range []string{old, nw} {
			if err := os.MkdirAll(d, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		log, _ := testLog()
		got, err := MigrateDataDir(old, nw, log)
		if err != nil || got != nw {
			t.Fatalf("got %q, %v", got, err)
		}
		if _, err := os.Stat(old); err != nil {
			t.Errorf("old touched: %v", err)
		}
	})
	t.Run("rename failure keeps old with warning", func(t *testing.T) {
		root := t.TempDir()
		old := filepath.Join(root, "swift")
		if err := os.MkdirAll(old, 0o700); err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS == "windows" || os.Geteuid() == 0 {
			t.Skip("needs POSIX permissions as non-root")
		}
		// New's parent is read-only, so the rename fails.
		ro := filepath.Join(root, "ro")
		if err := os.MkdirAll(ro, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(ro, 0o700) })
		nw := filepath.Join(ro, "goti")
		log, buf := testLog()
		got, err := MigrateDataDir(old, nw, log)
		if err != nil || got != old {
			t.Fatalf("got %q, %v; want %q", got, err, old)
		}
		if !strings.Contains(buf.String(), "WARN") {
			t.Errorf("no warning logged: %q", buf.String())
		}
	})
	t.Run("old locked skips migration", func(t *testing.T) {
		if runtime.GOOS != "linux" {
			t.Skip("relies on per-descriptor flock semantics")
		}
		root := t.TempDir()
		old, nw := filepath.Join(root, "swift"), filepath.Join(root, "goti")
		if err := os.MkdirAll(old, 0o700); err != nil {
			t.Fatal(err)
		}
		l, err := storage.TryLock(filepath.Join(old, "gui.lock"))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = l.Unlock() }()
		log, buf := testLog()
		got, err := MigrateDataDir(old, nw, log)
		if err != nil || got != old {
			t.Fatalf("got %q, %v; want %q", got, err, old)
		}
		if _, err := os.Stat(nw); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("new dir created: %v", err)
		}
		if !strings.Contains(buf.String(), "WARN") {
			t.Errorf("no warning: %q", buf.String())
		}
	})
}

func TestMigrateSkipsWhenCLIRunning(t *testing.T) {
	setup := func(t *testing.T) (string, string) {
		root := t.TempDir()
		old := filepath.Join(root, "swift")
		if err := os.MkdirAll(filepath.Join(old, "locks"), 0o700); err != nil {
			t.Fatal(err)
		}
		return old, filepath.Join(root, "goti")
	}
	check := func(t *testing.T, old, nw string) {
		t.Helper()
		log, buf := testLog()
		got, err := MigrateDataDir(old, nw, log)
		if err != nil || got != old {
			t.Fatalf("got %q, %v; want %q", got, err, old)
		}
		if _, err := os.Stat(nw); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("new dir created: %v", err)
		}
		if !strings.Contains(buf.String(), "WARN") {
			t.Errorf("no warning: %q", buf.String())
		}
	}
	t.Run("task flock held", func(t *testing.T) {
		if runtime.GOOS != "linux" {
			t.Skip("relies on per-descriptor flock semantics")
		}
		old, nw := setup(t)
		l, err := storage.TryLock(filepath.Join(old, "locks", "x.lock"))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = l.Unlock() }()
		check(t, old, nw)
	})
	t.Run("legacy db write transaction open", func(t *testing.T) {
		old, nw := setup(t)
		db, err := sql.Open("sqlite", filepath.Join(old, LegacyDBName))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()
		conn, err := db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close() }()
		ctx := context.Background()
		for _, q := range []string{"CREATE TABLE t(x)", "BEGIN IMMEDIATE", "INSERT INTO t VALUES(1)"} {
			if _, err := conn.ExecContext(ctx, q); err != nil {
				t.Fatal(q, err)
			}
		}
		defer func() { _, _ = conn.ExecContext(ctx, "ROLLBACK") }()
		check(t, old, nw)
	})
	t.Run("idle legacy db migrates", func(t *testing.T) {
		old, nw := setup(t)
		db, err := sql.Open("sqlite", filepath.Join(old, LegacyDBName))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("CREATE TABLE t(x)"); err != nil {
			t.Fatal(err)
		}
		_ = db.Close()
		log, _ := testLog()
		if got, _ := MigrateDataDir(old, nw, log); got != nw {
			t.Fatalf("got %q want %q", got, nw)
		}
	})
}

func TestDBPath(t *testing.T) {
	dir := t.TempDir()
	if got := DBPath(dir); got != filepath.Join(dir, DBName) {
		t.Errorf("fresh dir: %q", got)
	}
	if err := os.WriteFile(filepath.Join(dir, LegacyDBName), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := DBPath(dir); got != filepath.Join(dir, LegacyDBName) {
		t.Errorf("legacy present: %q", got)
	}
}

func TestDataDirEnvPrecedence(t *testing.T) {
	log, buf := testLog()
	if got := ResolveDataDir(map[string]string{EnvDataDir: "/a", LegacyEnvDataDir: "/b"}, log); got != "/a" {
		t.Errorf("GOTI_DATA_DIR not preferred: %q", got)
	}
	if got := ResolveDataDir(map[string]string{LegacyEnvDataDir: "/b"}, log); got != "/b" {
		t.Errorf("legacy fallback: %q", got)
	}
	if !strings.Contains(buf.String(), "deprecated") {
		t.Errorf("legacy env not logged as deprecated: %q", buf.String())
	}
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	if got := ResolveDataDir(map[string]string{}, log); got != DefaultDataDir() {
		t.Errorf("default: %q want %q", got, DefaultDataDir())
	}
}
