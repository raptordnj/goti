// This file implements crash-safe checkpointing (spec §9.1, invariant I1).

package downloader

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/raptordnj/swiftfetch/internal/domain"
	"github.com/raptordnj/swiftfetch/internal/storage"
)

// syncer flushes written data to stable storage (*storage.TempFile).
type syncer interface{ Sync() error }

// recorder snapshots segment progress (*Scheduler).
type recorder interface {
	Records() []domain.SegmentRecord
}

// checkpointer persists a task's segment progress. It is safe for concurrent
// use by several workers; checkpoints are serialised.
type checkpointer struct {
	store    Store
	taskID   string
	file     syncer
	sched    recorder
	clock    domain.Clock
	interval time.Duration
	bytes    int64

	ckMu sync.Mutex // serialises Checkpoint

	mu      sync.Mutex // guards pending and last
	pending int64
	last    time.Time
}

// newCheckpointer returns a checkpointer that fires every interval or every
// bytes written, whichever comes first.
func newCheckpointer(store Store, taskID string, file *storage.TempFile, sched *Scheduler, clock domain.Clock, interval time.Duration, bytes int64) *checkpointer {
	return newCheckpointerWith(store, taskID, file, sched, clock, interval, bytes)
}

// newCheckpointerWith is newCheckpointer over the narrow interfaces, so tests
// can observe call ordering.
func newCheckpointerWith(store Store, taskID string, file syncer, sched recorder, clock domain.Clock, interval time.Duration, bytes int64) *checkpointer {
	return &checkpointer{
		store: store, taskID: taskID, file: file, sched: sched, clock: clock,
		interval: interval, bytes: bytes, last: clock.Now(),
	}
}

// MaybeCheckpoint accounts n newly written bytes and checkpoints when the
// interval has elapsed or the byte threshold is reached since the last
// checkpoint. It does not allocate when no checkpoint fires.
func (c *checkpointer) MaybeCheckpoint(ctx context.Context, n int64) error {
	c.mu.Lock()
	c.pending += n
	due := c.pending >= c.bytes || c.clock.Now().Sub(c.last) >= c.interval
	c.mu.Unlock()
	if !due {
		return nil
	}
	return c.Checkpoint(ctx)
}

// Checkpoint snapshots the scheduler, syncs the file, then saves the
// snapshot. The snapshot is taken before Sync so it never claims bytes
// written after the sync (I1). A Sync error is KindLocal and the store is
// not touched.
func (c *checkpointer) Checkpoint(ctx context.Context) error {
	c.ckMu.Lock()
	defer c.ckMu.Unlock()
	c.mu.Lock()
	c.pending, c.last = 0, c.clock.Now()
	c.mu.Unlock()

	recs := c.sched.Records()
	if err := c.file.Sync(); err != nil {
		var de *domain.Error
		if errors.As(err, &de) && de.Kind == domain.KindLocal {
			return err
		}
		return domain.NewError(domain.KindLocal, "sync temp file: "+err.Error(), "check the disk", err)
	}
	var downloaded int64
	for _, r := range recs {
		downloaded += r.Pos - r.Start
	}
	return c.store.SaveCheckpoint(ctx, c.taskID, domain.Checkpoint{Segments: recs, Downloaded: downloaded})
}
