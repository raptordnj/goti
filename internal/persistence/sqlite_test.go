package persistence_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/raptordnj/swiftfetch/internal/domain"
	"github.com/raptordnj/swiftfetch/internal/downloader"
	"github.com/raptordnj/swiftfetch/internal/persistence"
)

var _ downloader.Store = (*persistence.DB)(nil)

func open(t *testing.T) (*persistence.DB, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "data", "swiftfetch.db")
	db, err := persistence.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, p
}

func mkTask(id string, created time.Time) *domain.Task {
	return &domain.Task{
		ID: id, OriginalURL: "http://a/x", FinalURL: "http://b/x", DestDir: "/d",
		FileName: "x.bin", TempPath: "/d/x.part", FinalPath: "/d/x.bin",
		Size: 1 << 40, SizeKnown: true,
		Validators:   domain.Validators{ETag: `"e1"`, ETagStrong: true, LastModified: "Mon"},
		Strategy:     domain.StrategyMultipart,
		DecisionJSON: []byte(`{"a":1}`),
		State:        domain.StateDownloading, LastErrorKind: domain.KindAuth, LastErrorMsg: "boom",
		ExpectedSHA256: "abc", ExistingPolicy: domain.PolicyOverwrite,
		SettingsJSON: []byte(`{"s":2}`), VerifyResult: "ok", Downloaded: 123,
		CreatedAt: created, UpdatedAt: created.Add(time.Minute),
	}
}

func TestTaskRoundTrip(t *testing.T) {
	db, _ := open(t)
	ctx := context.Background()
	in := mkTask("t1", time.Now().Add(123456789*time.Nanosecond))
	if err := db.CreateTask(ctx, in); err != nil {
		t.Fatal(err)
	}
	out, err := db.GetTask(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	want := *in
	want.CreatedAt = in.CreatedAt.Truncate(time.Second).UTC()
	want.UpdatedAt = in.UpdatedAt.Truncate(time.Second).UTC()
	if !out.CreatedAt.Equal(want.CreatedAt) || !out.UpdatedAt.Equal(want.UpdatedAt) {
		t.Fatalf("times: %v %v", out.CreatedAt, out.UpdatedAt)
	}
	if out.CreatedAt.Nanosecond() != 0 {
		t.Fatal("not second precision")
	}
	out.CreatedAt, out.UpdatedAt = want.CreatedAt, want.UpdatedAt
	if string(out.DecisionJSON) != `{"a":1}` || string(out.SettingsJSON) != `{"s":2}` {
		t.Fatal("json mismatch")
	}
	if out.Validators != in.Validators || out.Size != in.Size || !out.SizeKnown ||
		out.Strategy != in.Strategy || out.State != in.State || out.LastErrorKind != in.LastErrorKind ||
		out.LastErrorMsg != "boom" || out.ExpectedSHA256 != "abc" || out.ExistingPolicy != in.ExistingPolicy ||
		out.VerifyResult != "ok" || out.Downloaded != 123 || out.FinalPath != in.FinalPath ||
		out.OriginalURL != in.OriginalURL || out.FinalURL != in.FinalURL || out.DestDir != "/d" ||
		out.FileName != "x.bin" || out.TempPath != in.TempPath {
		t.Fatalf("mismatch: %+v", out)
	}

	in.State = domain.StatePaused
	in.Downloaded = 999
	if err := db.UpdateTask(ctx, in); err != nil {
		t.Fatal(err)
	}
	out, _ = db.GetTask(ctx, "t1")
	if out.State != domain.StatePaused || out.Downloaded != 999 {
		t.Fatal("update not applied")
	}
	if err := db.UpdateTask(ctx, mkTask("nope", time.Now())); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if _, err := db.GetTask(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestListNewestFirst(t *testing.T) {
	db, _ := open(t)
	ctx := context.Background()
	base := time.Now()
	for i, id := range []string{"a", "b", "c"} {
		if err := db.CreateTask(ctx, mkTask(id, base.Add(time.Duration(i)*time.Hour))); err != nil {
			t.Fatal(err)
		}
	}
	l, err := db.ListTasks(ctx)
	if err != nil || len(l) != 3 || l[0].ID != "c" || l[2].ID != "a" {
		t.Fatalf("%v %v", l, err)
	}
}

func segs(n int) []domain.SegmentRecord {
	var s []domain.SegmentRecord
	for i := 0; i < n; i++ {
		s = append(s, domain.SegmentRecord{Start: int64(i) * 10, End: int64(i+1) * 10, Pos: int64(i)*10 + 5,
			Done: i == 0, Hash: []byte{byte(i)}, HasherState: []byte{1, 2, byte(i)}})
	}
	return s
}

func TestCheckpointReplacesSegments(t *testing.T) {
	db, _ := open(t)
	ctx := context.Background()
	_ = db.CreateTask(ctx, mkTask("t", time.Now()))
	if err := db.SaveCheckpoint(ctx, "t", domain.Checkpoint{Segments: segs(3), Downloaded: 50}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveCheckpoint(ctx, "t", domain.Checkpoint{Segments: segs(2), Downloaded: 70}); err != nil {
		t.Fatal(err)
	}
	got, err := db.LoadSegments(ctx, "t")
	if err != nil || len(got) != 2 || got[1].Start != 10 || !got[0].Done || got[1].Done ||
		string(got[1].HasherState) != string([]byte{1, 2, 1}) || got[0].Pos != 5 {
		t.Fatalf("%+v %v", got, err)
	}
	tk, _ := db.GetTask(ctx, "t")
	if tk.Downloaded != 70 {
		t.Fatalf("downloaded %d", tk.Downloaded)
	}
	if err := db.ClearSegments(ctx, "t"); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.LoadSegments(ctx, "t"); len(got) != 0 {
		t.Fatal("not cleared")
	}
}

func TestCheckpointAtomic(t *testing.T) {
	db, _ := open(t)
	ctx := context.Background()
	_ = db.CreateTask(ctx, mkTask("t", time.Now()))
	_ = db.SaveCheckpoint(ctx, "t", domain.Checkpoint{Segments: segs(3), Downloaded: 50})
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if err := db.SaveCheckpoint(cctx, "t", domain.Checkpoint{Segments: segs(1), Downloaded: 5}); err == nil {
		t.Fatal("expected error")
	}
	// A duplicate start fails mid-transaction and must roll back the delete.
	dup := []domain.SegmentRecord{{Start: 0, End: 5}, {Start: 0, End: 5}}
	if err := db.SaveCheckpoint(ctx, "t", domain.Checkpoint{Segments: dup, Downloaded: 5}); err == nil {
		t.Fatal("expected error")
	}
	got, _ := db.LoadSegments(ctx, "t")
	tk, _ := db.GetTask(ctx, "t")
	if len(got) != 3 || tk.Downloaded != 50 {
		t.Fatalf("not intact: %d %d", len(got), tk.Downloaded)
	}
}

func TestAttemptsPruned(t *testing.T) {
	db, _ := open(t)
	ctx := context.Background()
	_ = db.CreateTask(ctx, mkTask("t", time.Now()))
	_ = db.CreateTask(ctx, mkTask("u", time.Now()))
	_ = db.AddAttempt(ctx, "u", domain.Attempt{At: time.Now(), Message: "keep"})
	base := time.Now()
	for i := 0; i < 250; i++ {
		a := domain.Attempt{At: base.Add(time.Duration(i) * time.Second), Kind: domain.KindOffline,
			Message: string(rune('a' + i%26)), Delay: 1500 * time.Millisecond}
		if i == 249 {
			a.Message = "last"
		}
		if err := db.AddAttempt(ctx, "t", a); err != nil {
			t.Fatal(err)
		}
	}
	l, err := db.ListAttempts(ctx, "t")
	if err != nil || len(l) != 200 {
		t.Fatalf("%d %v", len(l), err)
	}
	if l[len(l)-1].Message != "last" || l[0].At.Before(base.Add(50*time.Second).Truncate(time.Second)) {
		t.Fatal("wrong rows kept / order")
	}
	if l[0].Kind != domain.KindOffline || l[0].Delay != 1500*time.Millisecond {
		t.Fatalf("%+v", l[0])
	}
	if u, _ := db.ListAttempts(ctx, "u"); len(u) != 1 {
		t.Fatal("other task pruned")
	}
}

func TestDeleteCascades(t *testing.T) {
	db, _ := open(t)
	ctx := context.Background()
	_ = db.CreateTask(ctx, mkTask("t", time.Now()))
	_ = db.SaveCheckpoint(ctx, "t", domain.Checkpoint{Segments: segs(2)})
	_ = db.AddAttempt(ctx, "t", domain.Attempt{At: time.Now()})
	if err := db.DeleteTask(ctx, "t"); err != nil {
		t.Fatal(err)
	}
	if s, _ := db.LoadSegments(ctx, "t"); len(s) != 0 {
		t.Fatal("segments remain")
	}
	if a, _ := db.ListAttempts(ctx, "t"); len(a) != 0 {
		t.Fatal("attempts remain")
	}
	if err := db.DeleteTask(ctx, "t"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions")
	}
	_, p := open(t)
	fi, err := os.Stat(p)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file %v %v", fi, err)
	}
	di, _ := os.Stat(filepath.Dir(p))
	if di.Mode().Perm() != 0o700 {
		t.Fatalf("dir %v", di.Mode())
	}
}

func TestRefuseNewerSchema(t *testing.T) {
	db, p := open(t)
	_ = db.Close()
	raw, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec("UPDATE schema_version SET version = 99"); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	_, err = persistence.Open(p)
	if err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("got %v", err)
	}
}

func TestReopenPersists(t *testing.T) {
	db, p := open(t)
	ctx := context.Background()
	_ = db.CreateTask(ctx, mkTask("t", time.Now()))
	_ = db.SaveCheckpoint(ctx, "t", domain.Checkpoint{Segments: segs(2), Downloaded: 9})
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db2, err := persistence.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db2.Close() }()
	if s, _ := db2.LoadSegments(ctx, "t"); len(s) != 2 {
		t.Fatal("segments lost")
	}
	if _, err := db2.GetTask(ctx, "t"); err != nil {
		t.Fatal(err)
	}
}

func TestMigration2OnV1DB(t *testing.T) {
	db, p := open(t)
	ctx := context.Background()
	if err := db.CreateTask(ctx, mkTask("keep", time.Now())); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// Downgrade to a v1 database: no settings table, version 1.
	raw, err := sql.Open("sqlite", "file:"+filepath.ToSlash(p))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`DROP TABLE settings; UPDATE schema_version SET version = 1`); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()

	db2, err := persistence.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if _, err := db2.GetSetting(ctx, "app"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing setting err = %v", err)
	}
	if err := db2.PutSetting(ctx, "app", "v1"); err != nil {
		t.Fatal(err)
	}
	if err := db2.PutSetting(ctx, "app", "v2"); err != nil {
		t.Fatal(err)
	}
	if v, err := db2.GetSetting(ctx, "app"); err != nil || v != "v2" {
		t.Fatalf("got %q, %v", v, err)
	}
	if _, err := db2.GetTask(ctx, "keep"); err != nil {
		t.Fatalf("task lost: %v", err)
	}
}
