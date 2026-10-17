// This file holds the per-run environment shared by the single-stream and
// multipart executors.

package downloader

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/raptordnj/swiftfetch/internal/config"
	"github.com/raptordnj/swiftfetch/internal/domain"
	"github.com/raptordnj/swiftfetch/internal/ratelimit"
	"github.com/raptordnj/swiftfetch/internal/storage"
	"github.com/raptordnj/swiftfetch/internal/strategy"
)

// bufSize is the size of the pooled read buffers (spec §6.5) and the bufSize
// passed to Scheduler.TrySplit.
const bufSize = 64 << 10

// progressBytes is the new verified data that resets the retry budget (§8.1).
const progressBytes = 1 << 20

// runEnv is everything one execution run of a task needs.
type runEnv struct {
	task      *domain.Task
	decision  strategy.Decision
	client    *http.Client
	file      *storage.TempFile
	sched     *Scheduler
	limits    []*ratelimit.Bandwidth // per-download, global; nil entries are unlimited
	conns     *ratelimit.Semaphore   // nil = no global connection limit
	cfg       config.Config
	clock     domain.Clock
	backoff   Backoff
	budget    *Budget                                // nil = unlimited
	onBytes   func(n int64)                          // progress + controller accounting (atomic); may be nil
	onRetry   func(a domain.Attempt)                 // event + store; may be nil
	bufPool   *sync.Pool                             // *[]byte of bufSize
	writeAt   func(p []byte, off int64) (int, error) // defaults to file.WriteAt; tests inject faults
	windowMin time.Duration                          // controller window minimum; 0 means 2s
	// onCheckpoint is called after every n bytes written and hashed, with
	// checkpointer.MaybeCheckpoint semantics; an error aborts the run. May be nil.
	onCheckpoint func(ctx context.Context, n int64) error
}

// newBufPool returns a pool of bufSize byte buffers, stored as *[]byte so
// Get/Put do not allocate.
func newBufPool() *sync.Pool {
	return &sync.Pool{New: func() any {
		b := make([]byte, bufSize)
		return &b
	}}
}

// write calls env.writeAt, defaulting to the temp file.
func (env *runEnv) write(p []byte, off int64) (int, error) {
	if env.writeAt != nil {
		return env.writeAt(p, off)
	}
	return env.file.WriteAt(p, off)
}

func (env *runEnv) addBytes(n int64) {
	if env.onBytes != nil {
		env.onBytes(n)
	}
}

// stopCause returns the cause of ctx ending (ErrPaused, ErrCancelled, ...),
// or nil while ctx is live. Pause and cancel are never retried.
func stopCause(ctx context.Context) error {
	if ctx.Err() == nil {
		return nil
	}
	return context.Cause(ctx)
}
