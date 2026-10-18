// Package persistence stores tasks, segments and attempts in SQLite.
package persistence

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver

	"github.com/raptordnj/goti/internal/domain"
)

// SchemaVersion is the newest schema this binary understands.
const SchemaVersion = 2

const maxAttempts = 200

//go:embed migrations/*.sql
var migrationFS embed.FS

// DB is a SQLite-backed store.
type DB struct {
	sql *sql.DB
}

// Open opens (creating if needed) the database at path and migrates it.
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("persistence: create dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // path is operator-configured
	if err != nil {
		return nil, fmt.Errorf("persistence: create file: %w", err)
	}
	_ = f.Close()

	q := url.Values{}
	for _, p := range []string{"journal_mode(WAL)", "synchronous(FULL)", "foreign_keys(ON)",
		"busy_timeout(5000)", "cache_size(-512)", "mmap_size(0)"} {
		q.Add("_pragma", p)
	}
	dsn := "file:" + filepath.ToSlash(path) + "?" + q.Encode()
	raw, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("persistence: open: %w", err)
	}
	raw.SetMaxOpenConns(1) // single writer; keeps memory minimal
	db := &DB{sql: raw}
	if err := db.migrate(); err != nil {
		_ = raw.Close()
		return nil, err
	}
	return db, nil
}

// Close closes the database.
func (db *DB) Close() error { return db.sql.Close() }

type migration struct {
	version int
	sql     string
}

func loadMigrations() ([]migration, error) {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return nil, err
	}
	var ms []migration
	for _, e := range entries {
		num, _, _ := strings.Cut(e.Name(), "_")
		v, err := strconv.Atoi(num)
		if err != nil {
			return nil, fmt.Errorf("persistence: bad migration name %q", e.Name())
		}
		b, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, err
		}
		ms = append(ms, migration{v, string(b)})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].version < ms[j].version })
	return ms, nil
}

func (db *DB) migrate() error {
	ctx := context.Background()
	if _, err := db.sql.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version(version INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("persistence: schema_version: %w", err)
	}
	cur := 0
	err := db.sql.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&cur)
	hasRow := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("persistence: read version: %w", err)
	}
	if cur > SchemaVersion {
		return fmt.Errorf("persistence: database schema version %d is newer than supported %d", cur, SchemaVersion)
	}
	ms, err := loadMigrations()
	if err != nil {
		return err
	}
	for _, m := range ms {
		if m.version <= cur {
			continue
		}
		if err := db.applyMigration(ctx, m, hasRow); err != nil {
			return fmt.Errorf("persistence: migration %d: %w", m.version, err)
		}
		hasRow = true
	}
	return nil
}

func (db *DB) applyMigration(ctx context.Context, m migration, hasRow bool) error {
	return db.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			return err
		}
		if hasRow {
			_, err := tx.ExecContext(ctx, `UPDATE schema_version SET version = ?`, m.version)
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO schema_version(version) VALUES (?)`, m.version)
		return err
	})
}

func (db *DB) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func ts(t time.Time) int64 { return t.Unix() }

func fromTS(s int64) time.Time { return time.Unix(s, 0).UTC() }

const taskCols = `id, original_url, final_url, dest_dir, file_name, temp_path, size, size_known,
etag, etag_strong, last_modified, strategy, decision_json, state, last_error_kind, last_error_msg,
expected_sha256, existing_policy, settings_json, final_path, verify_result, downloaded, created_at, updated_at`

func taskArgs(t *domain.Task) []any {
	return []any{t.ID, t.OriginalURL, t.FinalURL, t.DestDir, t.FileName, t.TempPath, t.Size, b2i(t.SizeKnown),
		t.Validators.ETag, b2i(t.Validators.ETagStrong), t.Validators.LastModified, string(t.Strategy),
		t.DecisionJSON, string(t.State), int(t.LastErrorKind), t.LastErrorMsg,
		t.ExpectedSHA256, string(t.ExistingPolicy), t.SettingsJSON, t.FinalPath, t.VerifyResult, t.Downloaded,
		ts(t.CreatedAt), ts(t.UpdatedAt)}
}

type scanner interface{ Scan(...any) error }

func scanTask(s scanner) (*domain.Task, error) {
	var t domain.Task
	var sizeKnown, strong, kind int
	var strategy, state, policy string
	var created, updated int64
	err := s.Scan(&t.ID, &t.OriginalURL, &t.FinalURL, &t.DestDir, &t.FileName, &t.TempPath, &t.Size, &sizeKnown,
		&t.Validators.ETag, &strong, &t.Validators.LastModified, &strategy, &t.DecisionJSON, &state, &kind,
		&t.LastErrorMsg, &t.ExpectedSHA256, &policy, &t.SettingsJSON, &t.FinalPath, &t.VerifyResult,
		&t.Downloaded, &created, &updated)
	if err != nil {
		return nil, err
	}
	t.SizeKnown, t.Validators.ETagStrong = sizeKnown != 0, strong != 0
	t.Strategy, t.State, t.ExistingPolicy = domain.Strategy(strategy), domain.State(state), domain.ExistingFilePolicy(policy)
	t.LastErrorKind = domain.ErrorKind(kind)
	t.CreatedAt, t.UpdatedAt = fromTS(created), fromTS(updated)
	return &t, nil
}

// CreateTask inserts a new task.
func (db *DB) CreateTask(ctx context.Context, t *domain.Task) error {
	_, err := db.sql.ExecContext(ctx, `INSERT INTO tasks(`+taskCols+`) VALUES (`+placeholders(24)+`)`, taskArgs(t)...) //nolint:gosec // constant column list and placeholder count only
	return err
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// GetTask returns the task or domain.ErrNotFound.
func (db *DB) GetTask(ctx context.Context, id string) (*domain.Task, error) {
	t, err := scanTask(db.sql.QueryRowContext(ctx, `SELECT `+taskCols+` FROM tasks WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return t, err
}

// UpdateTask rewrites every mutable column of an existing task.
func (db *DB) UpdateTask(ctx context.Context, t *domain.Task) error {
	res, err := db.sql.ExecContext(ctx, `UPDATE tasks SET
original_url=?, final_url=?, dest_dir=?, file_name=?, temp_path=?, size=?, size_known=?,
etag=?, etag_strong=?, last_modified=?, strategy=?, decision_json=?, state=?, last_error_kind=?, last_error_msg=?,
expected_sha256=?, existing_policy=?, settings_json=?, final_path=?, verify_result=?, downloaded=?, created_at=?, updated_at=?
WHERE id = ?`, append(taskArgs(t)[1:], t.ID)...)
	return affected(res, err)
}

func affected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ListTasks returns all tasks, newest first.
func (db *DB) ListTasks(ctx context.Context) ([]*domain.Task, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT `+taskCols+` FROM tasks ORDER BY created_at DESC, rowid DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*domain.Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// DeleteTask removes a task; segments and attempts cascade.
func (db *DB) DeleteTask(ctx context.Context, id string) error {
	return affected(db.sql.ExecContext(ctx, `DELETE FROM tasks WHERE id = ?`, id))
}

// SaveCheckpoint atomically replaces the task's segments and sets downloaded.
func (db *DB) SaveCheckpoint(ctx context.Context, taskID string, cp domain.Checkpoint) error {
	return db.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM segments WHERE task_id = ?`, taskID); err != nil {
			return err
		}
		for _, s := range cp.Segments {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO segments(task_id, start, end, pos, done, hash, hasher_state) VALUES (?,?,?,?,?,?,?)`,
				taskID, s.Start, s.End, s.Pos, b2i(s.Done), s.Hash, s.HasherState); err != nil {
				return err
			}
		}
		res, err := tx.ExecContext(ctx, `UPDATE tasks SET downloaded = ? WHERE id = ?`, cp.Downloaded, taskID)
		return affected(res, err)
	})
}

// LoadSegments returns the task's segments ordered by start.
func (db *DB) LoadSegments(ctx context.Context, taskID string) ([]domain.SegmentRecord, error) {
	rows, err := db.sql.QueryContext(ctx,
		`SELECT start, end, pos, done, hash, hasher_state FROM segments WHERE task_id = ? ORDER BY start`, taskID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []domain.SegmentRecord
	for rows.Next() {
		var s domain.SegmentRecord
		var done int
		if err := rows.Scan(&s.Start, &s.End, &s.Pos, &done, &s.Hash, &s.HasherState); err != nil {
			return nil, err
		}
		s.Done = done != 0
		out = append(out, s)
	}
	return out, rows.Err()
}

// ClearSegments removes all segments of a task.
func (db *DB) ClearSegments(ctx context.Context, taskID string) error {
	_, err := db.sql.ExecContext(ctx, `DELETE FROM segments WHERE task_id = ?`, taskID)
	return err
}

// AddAttempt records an attempt and prunes the task to its 200 newest.
func (db *DB) AddAttempt(ctx context.Context, taskID string, a domain.Attempt) error {
	return db.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO attempts(task_id, at, kind, message, delay_ms) VALUES (?,?,?,?,?)`,
			taskID, ts(a.At), int(a.Kind), a.Message, a.Delay.Milliseconds()); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM attempts WHERE task_id = ? AND id NOT IN
(SELECT id FROM attempts WHERE task_id = ? ORDER BY id DESC LIMIT ?)`, taskID, taskID, maxAttempts)
		return err
	})
}

// ListAttempts returns the task's attempts, oldest first.
func (db *DB) ListAttempts(ctx context.Context, taskID string) ([]domain.Attempt, error) {
	rows, err := db.sql.QueryContext(ctx,
		`SELECT at, kind, message, delay_ms FROM attempts WHERE task_id = ? ORDER BY id`, taskID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []domain.Attempt
	for rows.Next() {
		var a domain.Attempt
		var at, kind, delay int64
		if err := rows.Scan(&at, &kind, &a.Message, &delay); err != nil {
			return nil, err
		}
		a.At, a.Kind, a.Delay = fromTS(at), domain.ErrorKind(kind), time.Duration(delay)*time.Millisecond
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetSetting returns the stored value for key, or domain.ErrNotFound.
func (db *DB) GetSetting(ctx context.Context, key string) (string, error) {
	var v string
	err := db.sql.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", domain.ErrNotFound
	}
	return v, err
}

// PutSetting stores value under key, replacing any existing value.
func (db *DB) PutSetting(ctx context.Context, key, value string) error {
	_, err := db.sql.ExecContext(ctx,
		`INSERT INTO settings(key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value)
	return err
}
