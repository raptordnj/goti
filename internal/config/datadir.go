package config

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/raptordnj/goti/internal/storage"
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
	MigrateDBName(dir, log)
	return dir
}

// MigrateDataDir moves the legacy data directory old to nw when nw does not
// exist and old does, returning the directory to use. It never copies: when
// the rename fails, or old's gui.lock is held by a running instance, it
// returns old for this run and logs a warning.
func MigrateDataDir(old, nw string, log *slog.Logger) (string, error) {
	if _, err := os.Stat(nw); err == nil {
		return nw, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nw, err
	}
	if st, err := os.Stat(old); err != nil || !st.IsDir() {
		return nw, nil
	}
	if locked(old) {
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

// MigrateDBName renames a legacy swiftfetch.db (and its WAL/SHM files) in
// dir to goti.db when goti.db does not exist and dir is not in use.
func MigrateDBName(dir string, log *slog.Logger) {
	if _, err := os.Stat(filepath.Join(dir, DBName)); err == nil {
		return
	}
	if _, err := os.Stat(filepath.Join(dir, LegacyDBName)); err != nil {
		return
	}
	if locked(dir) {
		return
	}
	for _, suf := range []string{"", "-wal", "-shm"} {
		from, to := filepath.Join(dir, LegacyDBName+suf), filepath.Join(dir, DBName+suf)
		if err := os.Rename(from, to); err != nil && !errors.Is(err, fs.ErrNotExist) {
			log.Warn("cannot rename legacy database", "from", from, "err", err)
		}
	}
}

// DBPath returns the database path in dir, falling back to the legacy name
// when only that exists (e.g. migration was skipped this run).
func DBPath(dir string) string {
	p := filepath.Join(dir, DBName)
	if _, err := os.Stat(p); err != nil {
		if _, err := os.Stat(filepath.Join(dir, LegacyDBName)); err == nil {
			return filepath.Join(dir, LegacyDBName)
		}
	}
	return p
}

// locked reports whether dir's gui.lock is held by another instance. It
// never creates the lock file.
func locked(dir string) bool {
	p := filepath.Join(dir, lockName)
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
