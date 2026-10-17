// This file implements the Engine: the public task API (add, run, pause,
// cancel, remove, explain, shutdown) and the per-task run guard (spec §16).
// The lifecycle of one run lives in engine_run.go.

package downloader

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/raptordnj/swiftfetch/internal/config"
	"github.com/raptordnj/swiftfetch/internal/domain"
	"github.com/raptordnj/swiftfetch/internal/events"
	"github.com/raptordnj/swiftfetch/internal/ratelimit"
	"github.com/raptordnj/swiftfetch/internal/storage"
	"github.com/raptordnj/swiftfetch/internal/strategy"
	"github.com/raptordnj/swiftfetch/internal/transport"
)

const (
	// shutdownWait bounds how long Shutdown waits for runs to checkpoint.
	shutdownWait = 10 * time.Second
	// maxManualConns is the per-download connection cap (spec §13).
	maxManualConns = 16
)

// ErrAlreadyRunning is returned by Run while another Run of the task is active.
var ErrAlreadyRunning = errors.New("task already running")

// errTaskCompleted is returned by Cancel for a task that already completed.
var errTaskCompleted = domain.NewError(domain.KindPermanent, "task already completed", "remove it instead", nil)

// ErrShuttingDown is returned by Run after Shutdown.
var ErrShuttingDown = errors.New("engine shutting down")

// EngineDeps are the Engine's collaborators.
type EngineDeps struct {
	Store     Store
	Config    config.Config
	Bus       *events.Bus
	Clock     domain.Clock
	Global    *ratelimit.Bandwidth
	Conns     *ratelimit.Semaphore
	Logger    *slog.Logger
	NewClient func(allowPrivate bool) *http.Client // production: transport.NewClient with cfg policy
	Resolver  *net.Resolver                        // nil → net.DefaultResolver
	// OfflineBaseDelay is the first connectivity-check interval; 0 → 5s.
	OfflineBaseDelay time.Duration
}

// AddRequest describes a new download.
type AddRequest struct {
	URL            string
	DestDir        string                    // "" → cfg.DownloadDir
	FileName       string                    // "" → from inspection
	ExpectedSHA256 string                    // hex, optional; validated 64 hex chars
	ExistingPolicy domain.ExistingFilePolicy // "" → cfg
	Connections    int                       // 0 = auto
	RateLimit      int64                     // 0 = cfg.PerDownloadRateLimit
	Conservative   bool
}

// taskSettings are the per-task overrides persisted in Task.SettingsJSON.
type taskSettings struct {
	Connections  int   `json:"connections,omitempty"`
	RateLimit    int64 `json:"rate_limit,omitempty"`
	Conservative bool  `json:"conservative,omitempty"`
}

// apply returns cfg with the per-task overrides applied.
func (s taskSettings) apply(cfg config.Config) config.Config {
	if s.Connections > 0 {
		cfg.MaxConnections, cfg.InitialConnections, cfg.AdaptiveConcurrency = s.Connections, s.Connections, false
	}
	if s.RateLimit > 0 {
		cfg.PerDownloadRateLimit = s.RateLimit
	}
	if s.Conservative {
		cfg.ConservativeMode = true
	}
	return cfg
}

// runHandle is the guard entry of one active Run (or of a Cancel/Remove of
// an idle task, which holds the guard with a nil cancel).
type runHandle struct {
	cancel context.CancelCauseFunc
	done   chan struct{}
}

// Engine runs download tasks. It is safe for concurrent use.
type Engine struct {
	d EngineDeps

	bufPool *sync.Pool // *[]byte of bufSize, shared by all runs and hashing

	mu       sync.Mutex
	running  map[string]*runHandle
	shutting bool
}

// NewEngine returns an Engine over d, filling defaults for the clock, logger
// and offline delay.
func NewEngine(d EngineDeps) *Engine {
	if d.Clock == nil {
		d.Clock = domain.RealClock{}
	}
	if d.Logger == nil {
		d.Logger = slog.New(slog.DiscardHandler)
	}
	if d.OfflineBaseDelay <= 0 {
		d.OfflineBaseDelay = defaultOfflineBase
	}
	if d.NewClient == nil {
		cfg := d.Config
		d.NewClient = func(allowPrivate bool) *http.Client {
			return transport.NewClient(transport.ClientOptions{
				Policy: cfg.NetworkPolicy, AllowPrivate: allowPrivate, AllowHTTPSDowngrade: cfg.AllowHTTPSDowngrade,
			})
		}
	}
	return &Engine{d: d, bufPool: newBufPool(), running: map[string]*runHandle{}}
}

func (e *Engine) publish(ev events.Event) {
	if e.d.Bus != nil {
		e.d.Bus.Publish(ev)
	}
}

// Add validates r, checks that the destination directory is writable and
// persists a queued task.
func (e *Engine) Add(ctx context.Context, r AddRequest) (*domain.Task, error) {
	cfg := e.d.Config
	if _, err := parseURL(r.URL); err != nil {
		return nil, err
	}
	sum, err := normalizeSHA256(r.ExpectedSHA256)
	if err != nil {
		return nil, err
	}
	policy := r.ExistingPolicy
	if policy == "" {
		policy = cfg.ExistingFilePolicy
	}
	switch policy {
	case domain.PolicyRename, domain.PolicyOverwrite, domain.PolicyFail:
	default:
		return nil, domain.NewError(domain.KindPolicy, fmt.Sprintf("unknown existing-file policy %q", policy), "", nil)
	}
	if r.Connections < 0 || r.Connections > maxManualConns {
		return nil, domain.NewError(domain.KindPolicy, fmt.Sprintf("connections must be 0 (auto) or 1-%d", maxManualConns), "", nil)
	}
	if r.RateLimit < 0 {
		return nil, domain.NewError(domain.KindPolicy, "rate limit must be >= 0", "", nil)
	}
	dir := r.DestDir
	if dir == "" {
		dir = cfg.DownloadDir
	}
	if dir, err = filepath.Abs(dir); err != nil {
		return nil, domain.NewError(domain.KindLocal, "invalid destination directory "+r.DestDir, "", err)
	}
	if err := checkWritableDir(dir); err != nil {
		return nil, err
	}
	name := ""
	if r.FileName != "" {
		name = storage.SanitizeFilename(r.FileName)
	}
	settings, err := json.Marshal(taskSettings{Connections: r.Connections, RateLimit: r.RateLimit, Conservative: r.Conservative})
	if err != nil {
		return nil, err
	}
	now := e.d.Clock.Now()
	t := &domain.Task{
		ID: newTaskID(), OriginalURL: r.URL, DestDir: dir, FileName: name,
		State: domain.StateQueued, ExpectedSHA256: sum, ExistingPolicy: policy,
		SettingsJSON: settings, CreatedAt: now, UpdatedAt: now,
	}
	if err := e.d.Store.CreateTask(ctx, t); err != nil {
		return nil, err
	}
	e.d.Logger.Info("task added", "task", t.ID, "url", transport.Redact(t.OriginalURL), "dir", dir)
	e.publish(events.StateChanged{ID: t.ID, To: domain.StateQueued, Reason: "added"})
	return t, nil
}

// parseURL accepts absolute http(s) URLs with a host.
func parseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return nil, domain.NewError(domain.KindPermanent, "invalid URL "+transport.Redact(raw), "use an absolute http or https URL", nil)
	}
	return u, nil
}

func normalizeSHA256(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 32 {
		return "", domain.NewError(domain.KindPolicy, "expected SHA-256 must be 64 hex characters", "", nil)
	}
	return hex.EncodeToString(b), nil
}

// checkWritableDir verifies dir exists, is a directory and accepts new files.
func checkWritableDir(dir string) error {
	fi, err := os.Stat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return domain.NewError(domain.KindLocal, "destination directory "+dir+" does not exist", "create it or choose another directory", err)
	case err != nil:
		return domain.NewError(domain.KindLocal, "destination directory "+dir+": "+err.Error(), "", err)
	case !fi.IsDir():
		return domain.NewError(domain.KindLocal, "destination "+dir+" is not a directory", "choose a directory", nil)
	}
	f, err := os.CreateTemp(dir, ".swiftfetch-probe-*")
	if err != nil {
		return domain.NewError(domain.KindLocal, "destination directory "+dir+" is not writable", "check its permissions", err)
	}
	_ = f.Close()
	_ = os.Remove(f.Name())
	return nil
}

func newTaskID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// acquire takes the run guard for id. cancel is nil for guard holders that
// are not runs.
func (e *Engine) acquire(id string, cancel context.CancelCauseFunc) (*runHandle, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.shutting && cancel != nil {
		return nil, ErrShuttingDown
	}
	if _, ok := e.running[id]; ok {
		return nil, ErrAlreadyRunning
	}
	h := &runHandle{cancel: cancel, done: make(chan struct{})}
	e.running[id] = h
	return h, nil
}

func (e *Engine) release(id string, h *runHandle) {
	e.mu.Lock()
	if e.running[id] == h {
		delete(e.running, id)
	}
	e.mu.Unlock()
	close(h.done)
}

// Run drives task id through its full lifecycle and returns nil once it is
// completed, domain.ErrPaused / domain.ErrCancelled when stopped, or the
// classified failure.
func (e *Engine) Run(ctx context.Context, id string) error {
	rctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	h, err := e.acquire(id, cancel)
	if err != nil {
		return err
	}
	defer e.release(id, h)
	t, err := e.d.Store.GetTask(rctx, id)
	if err != nil {
		return err
	}
	return newTaskRun(e, t).run(rctx)
}

// Pause stops a running task with cause domain.ErrPaused; it reports whether
// a run was found. A task that is verifying is not paused: the transition
// table has no verifying→paused, so verification and finalization run to
// completion (ruling R25).
func (e *Engine) Pause(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	h, ok := e.running[id]
	if !ok || h.cancel == nil {
		return false
	}
	h.cancel(domain.ErrPaused)
	return true
}

// stopRunning cancels a running Run of id with cause and waits for it to
// return; it reports whether a run was stopped.
func (e *Engine) stopRunning(ctx context.Context, id string, cause error) (bool, error) {
	e.mu.Lock()
	h, ok := e.running[id]
	e.mu.Unlock()
	if !ok {
		return false, nil
	}
	if h.cancel != nil {
		h.cancel(cause)
	}
	select {
	case <-h.done:
		return true, nil
	case <-ctx.Done():
		return true, ctx.Err()
	}
}

// Cancel moves the task to cancelled, stopping it if it runs. The temp file
// is kept.
func (e *Engine) Cancel(ctx context.Context, id string) error {
	for {
		stopped, err := e.stopRunning(ctx, id, domain.ErrCancelled)
		if err != nil {
			return err
		}
		if stopped {
			t, err := e.d.Store.GetTask(ctx, id)
			if err != nil || t.State == domain.StateCancelled {
				return err
			}
			if t.State == domain.StateCompleted {
				return errTaskCompleted
			}
			// The run ended for another reason (completed, failed, paused); retry.
		}
		h, err := e.acquire(id, nil)
		if errors.Is(err, ErrAlreadyRunning) {
			continue
		}
		if err != nil {
			return err
		}
		err = e.cancelIdle(ctx, id)
		e.release(id, h)
		return err
	}
}

func (e *Engine) cancelIdle(ctx context.Context, id string) error {
	t, err := e.d.Store.GetTask(ctx, id)
	if err != nil {
		return err
	}
	switch t.State {
	case domain.StateCancelled:
		return nil
	case domain.StateCompleted:
		return errTaskCompleted
	}
	return newTaskRun(e, t).setState(ctx, domain.StateCancelled, "cancelled")
}

// Remove deletes the task record, stopping it if it runs. With deletePartial
// the temp file of an unfinished task is deleted; a completed download is
// never deleted.
func (e *Engine) Remove(ctx context.Context, id string, deletePartial bool) error {
	for {
		if _, err := e.stopRunning(ctx, id, domain.ErrCancelled); err != nil {
			return err
		}
		h, err := e.acquire(id, nil)
		if errors.Is(err, ErrAlreadyRunning) {
			continue
		}
		if err != nil {
			return err
		}
		defer e.release(id, h)
		break
	}
	t, err := e.d.Store.GetTask(ctx, id)
	if err != nil {
		return err
	}
	if deletePartial && t.State != domain.StateCompleted && t.TempPath != "" {
		if err := os.Remove(t.TempPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return domain.NewError(domain.KindLocal, "remove temp file: "+err.Error(), "", err)
		}
	}
	if err := e.d.Store.DeleteTask(ctx, id); err != nil {
		return err
	}
	e.d.Logger.Info("task removed", "task", id, "deletePartial", deletePartial)
	return nil
}

// Explain inspects rawURL and returns the decision Run would make for a
// fresh task with r's settings, without creating anything.
func (e *Engine) Explain(ctx context.Context, rawURL string, r AddRequest) (strategy.Decision, strategy.Observations, error) {
	u, err := parseURL(rawURL)
	if err != nil {
		return strategy.Decision{}, strategy.Observations{}, err
	}
	cfg := taskSettings{Connections: r.Connections, RateLimit: r.RateLimit, Conservative: r.Conservative}.apply(e.d.Config)
	client, err := e.clientFor(ctx, u)
	if err != nil {
		return strategy.Decision{}, strategy.Observations{}, err
	}
	in := &strategy.Inspector{Client: client, Cfg: cfg, Clock: e.d.Clock}
	obs, err := in.Inspect(ctx, rawURL, nil)
	if err != nil {
		return strategy.Decision{}, obs, err
	}
	return strategy.Select(obs, cfg, nil), obs, nil
}

// clientFor builds the HTTP client for u, permitting private destinations
// only when u's own host is private (spec §11.2).
func (e *Engine) clientFor(ctx context.Context, u *url.URL) (*http.Client, error) {
	private, err := transport.HostIsPrivate(ctx, e.d.Resolver, u)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, domain.NewError(Classify(err), "resolve host: "+transport.RedactError(err).Error(), "", err)
	}
	return e.d.NewClient(private), nil
}

// Shutdown refuses new runs, pauses every running task and waits for them
// to stop (at most 10 s, or until ctx ends).
func (e *Engine) Shutdown(ctx context.Context) error {
	e.mu.Lock()
	e.shutting = true
	hs := make([]*runHandle, 0, len(e.running))
	for _, h := range e.running {
		if h.cancel != nil {
			h.cancel(domain.ErrPaused)
		}
		hs = append(hs, h)
	}
	e.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, shutdownWait)
	defer cancel()
	for _, h := range hs {
		select {
		case <-h.done:
		case <-ctx.Done():
			return fmt.Errorf("shutdown: runs still stopping: %w", ctx.Err())
		}
	}
	return nil
}
