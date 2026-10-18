package config

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/raptordnj/goti/internal/storage"

	_ "modernc.org/sqlite" // probe the legacy DB for use
)

// Data directory environment variables and file names. The Legacy* values
// are the pre-rebrand (SwiftFetch) names, kept only for migration.
const (
	EnvDataDir       = "GOTI_DATA_DIR"
	LegacyEnvDataDir = "SWIFTFETCH_DATA_DIR"
	DBName           = "goti.db"
	LegacyDBName     = "swiftfetch.db"
	lockName         = "gui.lock"
)

// ResolveDataDir picks the data directory: GOTI_DATA_DIR, then the
// deprecated SWIFTFETCH_DATA_DIR, then the per-OS default (migrating the
// legacy default directory into it). It must run before the DB is opened or
// any lock is taken in the directory.
func ResolveDataDir(env map[string]string, log *slog.Logger) string {
	var dir string
	switch {
	case env[EnvDataDir] != "":
		dir = env[EnvDataDir]
	case env[LegacyEnvDataDir] != "":
		log.Warn("SWIFTFETCH_DATA_DIR is deprecated; use GOTI_DATA_DIR", "dir", env[LegacyEnvDataDir])
		dir = env[LegacyEnvDataDir]
	default:
		d, err := MigrateDataDir(LegacyDataDir(), DefaultDataDir(), log)
		if err != nil {
			log.Warn("data dir migration", "err", err)
		}
		dir = d
	}
	return dir
}

// MigrateDataDir moves the legacy data directory old to nw when nw does not
// exist and old does, returning the directory to use. It never copies: when
// the rename fails, or old's gui.lock is held by a running instance, it
// returns old for this run and logs a warning.
func MigrateDataDir(old, nw string, log *slog.Logger) (string, error) {
	if st, err := os.Stat(old); err != nil || !st.IsDir() {
		return nw, nil
	}
	if _, err := os.Stat(nw); err == nil {
		return nw, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		log.Warn("cannot stat new data dir; using legacy dir this run", "dir", nw, "err", err)
		return old, nil
	}
	if inUse(old) {
		log.Warn("legacy data dir is in use by a running instance; not migrating this run", "dir", old)
		return old, nil
	}
	if err := os.MkdirAll(filepath.Dir(nw), 0o700); err == nil {
		err = os.Rename(old, nw)
		if err == nil {
			log.Info("migrated data from "+old, "to", nw)
			return nw, nil
		}
		log.Warn("cannot migrate data dir; using legacy dir this run", "from", old, "to", nw, "err", err)
	} else {
		log.Warn("cannot migrate data dir; using legacy dir this run", "from", old, "to", nw, "err", err)
	}
	return old, nil
}

// DBPath returns the database path in dir: the legacy swiftfetch.db when it
// exists (the SQLite files are never renamed), else goti.db.
func DBPath(dir string) string {
	if _, err := os.Stat(filepath.Join(dir, LegacyDBName)); err == nil {
		return filepath.Join(dir, LegacyDBName)
	}
	return filepath.Join(dir, DBName)
}

// inUse reports whether a running instance uses dir: its gui.lock or any
// locks/*.lock is flock-held, or its legacy database is busy.
func inUse(dir string) bool {
	if flockHeld(filepath.Join(dir, lockName)) {
		return true
	}
	locks, _ := filepath.Glob(filepath.Join(dir, "locks", "*.lock"))
	for _, l := range locks {
		if flockHeld(l) {
			return true
		}
	}
	return dbBusy(filepath.Join(dir, LegacyDBName))
}

// dbBusy reports whether the SQLite database at p is in use: BEGIN
// EXCLUSIVE with busy_timeout=0 fails. It never creates a missing database
// and always rolls back and closes.
func dbBusy(p string) bool {
	if _, err := os.Stat(p); err != nil {
		return false
	}
	db, err := sql.Open("sqlite", "file:"+p+"?mode=rw")
	if err != nil {
		return true
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return true
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, "PRAGMA busy_timeout=0"); err != nil {
		return true
	}
	if _, err := conn.ExecContext(ctx, "BEGIN EXCLUSIVE"); err != nil {
		return true
	}
	_, _ = conn.ExecContext(ctx, "ROLLBACK")
	return false
}

// flockHeld reports whether another holder has an flock on p. It never
// creates p.
func flockHeld(p string) bool {
	if _, err := os.Stat(p); err != nil {
		return false
	}
	l, err := storage.TryLock(p)
	if err != nil {
		return errors.Is(err, storage.ErrLocked)
	}
	_ = l.Unlock()
	return false
}
