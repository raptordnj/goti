package downloader

import (
	"context"
	"errors"
	"io/fs"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/raptordnj/goti/internal/clocktest"
	"github.com/raptordnj/goti/internal/domain"
	"github.com/raptordnj/goti/tests/faultserver"
)

// seqSyncer records the sequence number of each Sync call.
type seqSyncer struct {
	seq   *atomic.Int64
	calls []int64
	err   error
}

func (s *seqSyncer) Sync() error {
	s.calls = append(s.calls, s.seq.Add(1))
	return s.err
}

// seqRecorder records the sequence number of each Records call.
type seqRecorder struct {
	seq   *atomic.Int64
	calls []int64
	recs  []domain.SegmentRecord
}

func (r *seqRecorder) Records() []domain.SegmentRecord {
	r.calls = append(r.calls, r.seq.Add(1))
	return r.recs
}

func TestCheckpointOrdering(t *testing.T) {
	var seq atomic.Int64
	recs := []domain.SegmentRecord{
		{Start: 0, End: 100, Pos: 100, Done: true, Hash: make([]byte, 32)},
		{Start: 100, End: 300, Pos: 150, HasherState: []byte{1}},
		{Start: 300, End: 400, Pos: 300},
	}
	sy := &seqSyncer{seq: &seq}
	rc := &seqRecorder{seq: &seq, recs: recs}
	st := newMemStore()
	var saves []int64
	st.onSave = func() { saves = append(saves, seq.Add(1)) }
	c := newCheckpointerWith(st, "t1", sy, rc, clocktest.New(time.Unix(0, 0)), 2*time.Second, 16<<20)

	for range 3 {
		if err := c.Checkpoint(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(rc.calls) != 3 || len(sy.calls) != 3 || len(saves) != 3 {
		t.Fatalf("calls records=%d sync=%d save=%d, want 3 each", len(rc.calls), len(sy.calls), len(saves))
	}
	for i := range 3 {
		if rc.calls[i] >= sy.calls[i] || sy.calls[i] >= saves[i] {
			t.Fatalf("call %d order records=%d sync=%d save=%d, want snapshot < sync < save", i, rc.calls[i], sy.calls[i], saves[i])
		}
	}
	cp, _ := st.lastCheckpoint()
	if cp.Downloaded != 150 {
		t.Fatalf("Downloaded %d, want 150", cp.Downloaded)
	}
	if len(cp.Segments) != 3 || cp.Segments[1].Pos != 150 {
		t.Fatalf("segments %+v", cp.Segments)
	}

	// A failing Sync never reaches the store and is KindLocal.
	sy.err = errors.New("EIO")
	err := c.Checkpoint(context.Background())
	if Classify(err) != domain.KindLocal {
		t.Fatalf("Sync error %v classified %v, want KindLocal", err, Classify(err))
	}
	if !errors.Is(err, sy.err) {
		t.Fatalf("error %v does not wrap the Sync error", err)
	}
	if st.saves() != 3 {
		t.Fatalf("store called after Sync failure: %d saves", st.saves())
	}
}

func TestMaybeCheckpointTriggers(t *testing.T) {
	ctx := context.Background()
	var seq atomic.Int64
	clk := clocktest.New(time.Unix(1000, 0))
	st := newMemStore()
	c := newCheckpointerWith(st, "t1", &seqSyncer{seq: &seq}, &seqRecorder{seq: &seq}, clk, 2*time.Second, 16<<20)

	clk.Advance(1900 * time.Millisecond)
	if err := c.MaybeCheckpoint(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if st.saves() != 0 {
		t.Fatal("checkpoint fired at 1.9s")
	}
	clk.Advance(100 * time.Millisecond)
	if err := c.MaybeCheckpoint(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if st.saves() != 1 {
		t.Fatalf("saves %d at 2s, want 1", st.saves())
	}

	// Counters reset after a checkpoint: the bytes threshold now applies alone.
	if err := c.MaybeCheckpoint(ctx, 16<<20-1); err != nil {
		t.Fatal(err)
	}
	if st.saves() != 1 {
		t.Fatal("checkpoint fired below 16 MiB")
	}
	if err := c.MaybeCheckpoint(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if st.saves() != 2 {
		t.Fatalf("saves %d at 16 MiB, want 2", st.saves())
	}
}

func TestWriteENOSPCFailsLocal(t *testing.T) {
	const (
		size  = 2 << 20
		limit = 1 << 20
	)
	f := newSingleFixture(t, faultserver.Options{Size: size})
	f.env.sched = NewScheduler(size, size)
	st := newMemStore()
	cp := newCheckpointer(st, "t1", f.env.file, f.env.sched, domain.RealClock{}, time.Hour, 256<<10)
	f.env.onCheckpoint = cp.MaybeCheckpoint
	var written int64
	f.env.writeAt = func(p []byte, off int64) (int, error) {
		if written+int64(len(p)) > limit {
			return 0, &fs.PathError{Op: "write", Path: f.env.file.Path(), Err: syscall.ENOSPC}
		}
		written += int64(len(p))
		return f.env.file.WriteAt(p, off)
	}

	err := run(t, f)
	if Classify(err) != domain.KindLocal {
		t.Fatalf("err %v classified %v, want KindLocal", err, Classify(err))
	}
	if n := len(f.attemptList()); n != 0 {
		t.Fatalf("%d retry attempts, want 0", n)
	}
	last, ok := st.lastCheckpoint()
	if !ok || last.Downloaded < 768<<10 {
		t.Fatalf("last checkpoint %+v (ok=%v), want >= 768 KiB", last.Downloaded, ok)
	}
	// The checkpointed bytes are on disk and verify on recovery.
	recs, discarded, err := recoverSegments(context.Background(), f.env.file, size, last.Segments)
	if err != nil || discarded != 0 {
		t.Fatalf("recover: discarded=%d err=%v", discarded, err)
	}
	if recs[0].Pos != last.Downloaded {
		t.Fatalf("recovered pos %d, want %d", recs[0].Pos, last.Downloaded)
	}
}
