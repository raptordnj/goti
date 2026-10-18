// Package integration exercises the public download engine end to end
// against the fault server and a real SQLite store.
package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/raptordnj/goti/internal/config"
	"github.com/raptordnj/goti/internal/domain"
	"github.com/raptordnj/goti/internal/downloader"
	"github.com/raptordnj/goti/internal/events"
	"github.com/raptordnj/goti/internal/persistence"
	"github.com/raptordnj/goti/internal/ratelimit"
	"github.com/raptordnj/goti/internal/transport"
	"github.com/raptordnj/goti/tests/faultserver"
)

const seed = 25

// harness is an Engine wired like cmd/goti-cli (real SQLite, event
// bus, transport.NewClient) over one fault server.
type harness struct {
	eng  *downloader.Engine
	db   *persistence.DB
	srv  *faultserver.Server
	dir  string
	seed uint64

	mu  sync.Mutex
	evs []events.Event
}

type harnessOpts struct {
	cfg func(*config.Config)
	// dial replaces the client's network dial (nil: the server's Dialer).
	dial func(ctx context.Context, network, addr string) (net.Conn, error)
	// offlineBaseDelay replaces the engine's OfflineBaseDelay (0: 100 ms).
	offlineBaseDelay time.Duration
}

func newHarness(t *testing.T, opts faultserver.Options, ho harnessOpts) *harness {
	t.Helper()
	if opts.Seed == 0 {
		opts.Seed = seed
	}
	h := &harness{srv: faultserver.New(t, opts), dir: t.TempDir(), seed: opts.Seed}
	db, err := persistence.Open(filepath.Join(t.TempDir(), "goti.db"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.DownloadDir = h.dir
	cfg.DataDir = t.TempDir()
	cfg.RetryBaseDelay, cfg.RetryMaxDelay = 5*time.Millisecond, 50*time.Millisecond
	cfg.StallTimeout = 10 * time.Second
	cfg.CheckpointInterval = 200 * time.Millisecond
	cfg.ProgressHz = 20
	if ho.cfg != nil {
		ho.cfg(&cfg)
	}
	offlineBase := ho.offlineBaseDelay
	if offlineBase == 0 {
		offlineBase = 100 * time.Millisecond
	}
	dial := ho.dial
	if dial == nil {
		dial = h.srv.Dialer()
	}
	bus := events.NewBus(cfg.ProgressHz, domain.RealClock{})
	sub := bus.Subscribe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			ev, err := sub.Next(ctx)
			if err != nil {
				return
			}
			h.mu.Lock()
			h.evs = append(h.evs, ev)
			h.mu.Unlock()
		}
	}()
	h.db = db
	h.eng = downloader.NewEngine(downloader.EngineDeps{
		Store: db, Config: cfg, Bus: bus, Clock: domain.RealClock{},
		Global: ratelimit.NewBandwidth(cfg.GlobalRateLimit),
		Conns:  ratelimit.NewSemaphore(cfg.GlobalMaxConnections),
		NewClient: func(allowPrivate bool) *http.Client {
			return transport.NewClient(transport.ClientOptions{
				Policy: cfg.NetworkPolicy, AllowPrivate: allowPrivate,
				AllowHTTPSDowngrade: cfg.AllowHTTPSDowngrade, Dialer: dial,
			})
		},
		OfflineBaseDelay: offlineBase,
	})
	t.Cleanup(func() {
		sctx, scancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer scancel()
		if err := h.eng.Shutdown(sctx); err != nil {
			t.Errorf("shutdown: %v", err)
		}
		cancel()
		<-done
		if err := db.Close(); err != nil {
			t.Errorf("close db: %v", err)
		}
	})
	return h
}

// expectedSHA256 is the generator's digest for this harness's seed.
func (h *harness) expectedSHA256(size int64) [32]byte {
	return faultserver.ExpectedSHA256(h.seed, size)
}

func (h *harness) fileURL() string { return h.srv.URL + "/file" }

func (h *harness) add(t *testing.T, r downloader.AddRequest) *domain.Task {
	t.Helper()
	if r.URL == "" {
		r.URL = h.fileURL()
	}
	task, err := h.eng.Add(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

// run runs id to its end and returns the stored task.
func (h *harness) run(t *testing.T, id string, timeout time.Duration) *domain.Task {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	err := h.eng.Run(ctx, id)
	task := h.task(t, id)
	t.Logf("run: err=%v state=%s strategy=%s kind=%s msg=%q", err, task.State, task.Strategy, task.LastErrorKind, task.LastErrorMsg)
	return task
}

func (h *harness) task(t *testing.T, id string) *domain.Task {
	t.Helper()
	task, err := h.db.GetTask(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func (h *harness) events() []events.Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]events.Event(nil), h.evs...)
}

// sawRetry reports whether id entered retrying or scheduled a retry.
func (h *harness) sawRetry(id string) bool {
	for _, ev := range h.events() {
		switch e := ev.(type) {
		case events.RetryScheduled:
			if e.ID == id {
				return true
			}
		case events.StateChanged:
			if e.ID == id && e.To == domain.StateRetrying {
				return true
			}
		}
	}
	return false
}

func fileSHA256(t *testing.T, path string) ([32]byte, int64) {
	t.Helper()
	f, err := os.Open(path) //nolint:gosec // test path
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	hs := sha256.New()
	n, err := io.Copy(hs, f)
	if err != nil {
		t.Fatal(err)
	}
	var sum [32]byte
	copy(sum[:], hs.Sum(nil))
	return sum, n
}

func hexSum(size int64) string {
	s := faultserver.ExpectedSHA256(seed, size)
	return hex.EncodeToString(s[:])
}
