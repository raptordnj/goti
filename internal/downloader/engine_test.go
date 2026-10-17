package downloader

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/raptordnj/swiftfetch/internal/config"
	"github.com/raptordnj/swiftfetch/internal/domain"
	"github.com/raptordnj/swiftfetch/internal/events"
	"github.com/raptordnj/swiftfetch/internal/ratelimit"
	"github.com/raptordnj/swiftfetch/internal/transport"
	"github.com/raptordnj/swiftfetch/tests/faultserver"
)

const engSeed = 11

// engFixture is an Engine over a memStore and one fault server.
type engFixture struct {
	e     *Engine
	store *memStore
	srv   *faultserver.Server
	dir   string
	cfg   config.Config

	mu     sync.Mutex
	evs    []events.Event
	wrapRT func(http.RoundTripper) http.RoundTripper
}

func newEngFixture(t *testing.T, opts faultserver.Options, mod func(*config.Config), depMods ...func(*EngineDeps)) *engFixture {
	t.Helper()
	opts.Seed = engSeed
	if opts.ETag == "" && !opts.NoValidators {
		opts.ETag = "v1"
	}
	f := &engFixture{store: newMemStore(), srv: faultserver.New(t, opts), dir: t.TempDir()}
	cfg := config.Default()
	cfg.DownloadDir = f.dir
	cfg.DataDir = t.TempDir()
	cfg.RetryBaseDelay, cfg.RetryMaxDelay = 5*time.Millisecond, 20*time.Millisecond
	cfg.StallTimeout = 5 * time.Second
	cfg.CheckpointInterval = 100 * time.Millisecond
	cfg.CheckpointBytes = 1 << 20
	cfg.ProgressHz = 20
	if mod != nil {
		mod(&cfg)
	}
	f.cfg = cfg
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
			f.mu.Lock()
			f.evs = append(f.evs, ev)
			f.mu.Unlock()
		}
	}()
	t.Cleanup(func() { cancel(); <-done })
	deps := EngineDeps{
		Store:  f.store,
		Config: cfg,
		Bus:    bus,
		Clock:  domain.RealClock{},
		Global: ratelimit.NewBandwidth(0),
		Conns:  ratelimit.NewSemaphore(cfg.GlobalMaxConnections),
		NewClient: func(allowPrivate bool) *http.Client {
			c := transport.NewClient(transport.ClientOptions{
				Policy: cfg.NetworkPolicy, AllowPrivate: allowPrivate, Dialer: f.srv.Dialer(),
			})
			f.mu.Lock()
			w := f.wrapRT
			f.mu.Unlock()
			if w != nil {
				c.Transport = w(c.Transport)
			}
			return c
		},
		OfflineBaseDelay: 100 * time.Millisecond,
	}
	for _, m := range depMods {
		m(&deps)
	}
	f.e = NewEngine(deps)
	return f
}

func (f *engFixture) url() string { return f.srv.URL + "/file?token=secret" }

func (f *engFixture) add(t *testing.T, r AddRequest) *domain.Task {
	t.Helper()
	if r.URL == "" {
		r.URL = f.url()
	}
	task, err := f.e.Add(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func (f *engFixture) run(t *testing.T, id string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return f.e.Run(ctx, id)
}

func (f *engFixture) task(t *testing.T, id string) *domain.Task {
	t.Helper()
	task, err := f.store.GetTask(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func (f *engFixture) events() []events.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]events.Event(nil), f.evs...)
}

// states returns the To state of every StateChanged event for id.
func (f *engFixture) states(id string) []string {
	var out []string
	for _, ev := range f.events() {
		if sc, ok := ev.(events.StateChanged); ok && sc.ID == id {
			out = append(out, string(sc.To))
		}
	}
	return out
}

// downloaded returns the latest Progress.Downloaded for id.
func (f *engFixture) downloaded(id string) int64 {
	var n int64
	for _, ev := range f.events() {
		if p, ok := ev.(events.Progress); ok && p.ID == id {
			n = p.Downloaded
		}
	}
	return n
}

// waitFor polls cond every 5 ms until it holds or the deadline passes.
func waitFor(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func checkContent(t *testing.T, path string, seed uint64, size int64) {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // test path
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(data)) != size || sha256.Sum256(data) != faultserver.ExpectedSHA256(seed, size) {
		t.Fatalf("%s: wrong content (len %d, want %d)", path, len(data), size)
	}
}

func partFiles(t *testing.T, dir string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(dir, "*.swiftfetch-part"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func hexSum(seed uint64, size int64) string {
	s := faultserver.ExpectedSHA256(seed, size)
	return hex.EncodeToString(s[:])
}

func wantKind(t *testing.T, err error, k domain.ErrorKind) *domain.Error {
	t.Helper()
	var de *domain.Error
	if !errors.As(err, &de) || de.Kind != k {
		t.Fatalf("error %v, want kind %s", err, k)
	}
	return de
}

// startRun runs id in the background; the result arrives on the channel.
func (f *engFixture) startRun(id string) <-chan error {
	ch := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		ch <- f.e.Run(ctx, id)
	}()
	return ch
}

func recvErr(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(30 * time.Second):
		t.Fatal("Run did not return")
		return nil
	}
}

// pauseAt starts id, pauses once Progress reports at least n bytes and
// returns Run's result.
func (f *engFixture) pauseAt(t *testing.T, id string, n int64) error {
	t.Helper()
	ch := f.startRun(id)
	waitFor(t, 20*time.Second, "progress", func() bool { return f.downloaded(id) >= n })
	if !f.e.Pause(id) {
		t.Fatal("Pause found no running task")
	}
	return recvErr(t, ch)
}

func TestRunCompletes(t *testing.T) {
	const size = 20 << 20
	f := newEngFixture(t, faultserver.Options{Size: size}, nil)
	task := f.add(t, AddRequest{})
	if err := f.run(t, task.ID); err != nil {
		t.Fatal(err)
	}
	got := f.task(t, task.ID)
	if got.State != domain.StateCompleted {
		t.Fatalf("state %s", got.State)
	}
	checkContent(t, got.FinalPath, engSeed, size)
	if got.VerifyResult != "segment-consistency verified; no trusted checksum supplied" {
		t.Fatalf("verify result %q", got.VerifyResult)
	}
	if p := partFiles(t, f.dir); len(p) != 0 {
		t.Fatalf("temp files left: %v", p)
	}
	if segs, _ := f.store.LoadSegments(context.Background(), task.ID); len(segs) != 0 {
		t.Fatalf("segments not cleared: %d", len(segs))
	}
	waitFor(t, 2*time.Second, "completed event", func() bool {
		s := f.states(task.ID)
		return len(s) > 0 && s[len(s)-1] == "completed"
	})
	if got, want := strings.Join(f.states(task.ID), ">"), "queued>inspecting>downloading>verifying>completed"; got != want {
		t.Fatalf("states %s, want %s", got, want)
	}
	decided := false
	for _, ev := range f.events() {
		if d, ok := ev.(events.DecisionMade); ok && d.ID == task.ID {
			decided = true
			if strings.Contains(d.Summary, "secret") || strings.Contains(string(d.DecisionJSON), "secret") {
				t.Fatal("decision not redacted")
			}
		}
	}
	if !decided {
		t.Fatal("no DecisionMade event")
	}
	if strings.Contains(got.FinalURL, "secret") {
		t.Fatalf("FinalURL not redacted: %s", got.FinalURL)
	}
}

func TestRunChecksumMismatch(t *testing.T) {
	const size = 1 << 20
	f := newEngFixture(t, faultserver.Options{Size: size, CorruptAt: []int64{12345}}, nil)
	task := f.add(t, AddRequest{ExpectedSHA256: hexSum(engSeed, size)})
	_ = wantKind(t, f.run(t, task.ID), domain.KindIntegrity)
	got := f.task(t, task.ID)
	if got.State != domain.StateFailed || got.LastErrorKind != domain.KindIntegrity {
		t.Fatalf("state %s kind %s", got.State, got.LastErrorKind)
	}
	if _, err := os.Stat(got.TempPath); err != nil {
		t.Fatalf("temp file not kept: %v", err)
	}
}

func TestRunChecksumMatch(t *testing.T) {
	const size = 1 << 20
	f := newEngFixture(t, faultserver.Options{Size: size}, nil)
	task := f.add(t, AddRequest{ExpectedSHA256: strings.ToUpper(hexSum(engSeed, size))})
	if err := f.run(t, task.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.task(t, task.ID); got.VerifyResult != "sha256 verified" {
		t.Fatalf("verify result %q", got.VerifyResult)
	}
}

// countingRT counts response body bytes delivered to the client.
type countingRT struct {
	rt http.RoundTripper
	n  *atomic.Int64
}

type countingBody struct {
	io.ReadCloser
	n *atomic.Int64
}

func (b countingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.n.Add(int64(n))
	return n, err
}

func (c countingRT) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := c.rt.RoundTrip(r)
	if err == nil {
		resp.Body = countingBody{resp.Body, c.n}
	}
	return resp, err
}

func TestPauseResume(t *testing.T) {
	const size = 10 << 20
	f := newEngFixture(t, faultserver.Options{Size: size, BytesPerSecondGlobal: 10 << 20}, nil)
	var served atomic.Int64
	f.wrapRT = func(rt http.RoundTripper) http.RoundTripper { return countingRT{rt, &served} }
	task := f.add(t, AddRequest{})
	if err := f.pauseAt(t, task.ID, 3<<20); !errors.Is(err, domain.ErrPaused) {
		t.Fatalf("Run after Pause: %v", err)
	}
	if got := f.task(t, task.ID); got.State != domain.StatePaused {
		t.Fatalf("state %s", got.State)
	}
	nReq := len(f.srv.Requests())
	f.srv.SetOptions(func(o *faultserver.Options) { o.BytesPerSecondGlobal = 0 })
	if err := f.run(t, task.ID); err != nil {
		t.Fatal(err)
	}
	got := f.task(t, task.ID)
	checkContent(t, got.FinalPath, engSeed, size)
	resumed := false
	for _, r := range f.srv.Requests()[nReq:] {
		if r.Range != "" && !strings.HasPrefix(r.Range, "bytes=0-") && r.Range != "bytes=5242880-5242881" {
			resumed = true
		}
	}
	if !resumed {
		t.Fatal("no resumed ranges after pause")
	}
	if n := served.Load(); n >= size*14/10 {
		t.Fatalf("served %d bytes, want < 1.4 x %d", n, size)
	}
}

func TestRunGuard(t *testing.T) {
	const size = 2 << 20
	f := newEngFixture(t, faultserver.Options{Size: size, BytesPerSecondGlobal: 4 << 20}, nil)
	task := f.add(t, AddRequest{})
	a, b := f.startRun(task.ID), f.startRun(task.ID)
	ea, eb := recvErr(t, a), recvErr(t, b)
	if (ea == nil) == (eb == nil) {
		t.Fatalf("want exactly one success: %v, %v", ea, eb)
	}
	if !errors.Is(ea, ErrAlreadyRunning) && !errors.Is(eb, ErrAlreadyRunning) {
		t.Fatalf("no ErrAlreadyRunning: %v, %v", ea, eb)
	}
}

func TestPauseResumeRace(t *testing.T) {
	const size = 8 << 20
	f := newEngFixture(t, faultserver.Options{Size: size, BytesPerSecondGlobal: 64 << 20}, func(c *config.Config) {
		c.MinMultipartSize = 1 << 20
		c.MaxConnections = 4
	})
	task := f.add(t, AddRequest{})
	rng := rand.New(rand.NewPCG(3, 4)) //nolint:gosec // test
	done := false
	for i := 0; i < 50 && !done; i++ {
		ch := f.startRun(task.ID)
		time.Sleep(time.Duration(rng.IntN(50)) * time.Millisecond)
		f.e.Pause(task.ID)
		err := recvErr(t, ch)
		switch {
		case err == nil:
			done = true
		case errors.Is(err, domain.ErrPaused):
		default:
			t.Fatalf("iteration %d: %v", i, err)
		}
	}
	if !done {
		if err := f.run(t, task.ID); err != nil {
			t.Fatal(err)
		}
	}
	checkContent(t, f.task(t, task.ID).FinalPath, engSeed, size)
	if m := f.srv.ActiveMax(); m > f.cfg.MaxConnections {
		t.Fatalf("ActiveMax %d > MaxConnections %d", m, f.cfg.MaxConnections)
	}
}

func TestRemoteChangedOnResume(t *testing.T) {
	const size = 10 << 20
	for _, restart := range []bool{false, true} {
		t.Run(map[bool]string{false: "pause", true: "restart"}[restart], func(t *testing.T) {
			f := newEngFixture(t, faultserver.Options{Size: size, BytesPerSecondGlobal: 10 << 20}, func(c *config.Config) {
				c.RestartOnRemoteChange = restart
			})
			task := f.add(t, AddRequest{})
			if err := f.pauseAt(t, task.ID, 2<<20); !errors.Is(err, domain.ErrPaused) {
				t.Fatal(err)
			}
			f.srv.SetOptions(func(o *faultserver.Options) {
				o.ETag, o.Seed, o.BytesPerSecondGlobal = "v2", faultserver.ChangedSeed(engSeed), 0
			})
			err := f.run(t, task.ID)
			if !restart {
				_ = wantKind(t, err, domain.KindRemoteChanged)
				if got := f.task(t, task.ID); got.State != domain.StatePaused {
					t.Fatalf("state %s", got.State)
				}
				if segs, _ := f.store.LoadSegments(context.Background(), task.ID); len(segs) == 0 {
					t.Fatal("segments discarded")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			checkContent(t, f.task(t, task.ID).FinalPath, faultserver.ChangedSeed(engSeed), size)
		})
	}
}

func TestCancel(t *testing.T) {
	const size = 10 << 20
	f := newEngFixture(t, faultserver.Options{Size: size, BytesPerSecondGlobal: 5 << 20}, nil)
	task := f.add(t, AddRequest{})
	ch := f.startRun(task.ID)
	waitFor(t, 10*time.Second, "progress", func() bool { return f.downloaded(task.ID) >= 1<<20 })
	if err := f.e.Cancel(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	if err := recvErr(t, ch); !errors.Is(err, domain.ErrCancelled) {
		t.Fatalf("Run: %v", err)
	}
	got := f.task(t, task.ID)
	if got.State != domain.StateCancelled {
		t.Fatalf("state %s", got.State)
	}
	if _, err := os.Stat(got.TempPath); err != nil {
		t.Fatalf("temp file removed: %v", err)
	}
	if err := f.e.Remove(context.Background(), task.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(got.TempPath); !os.IsNotExist(err) {
		t.Fatalf("temp file still present: %v", err)
	}
	if _, err := f.store.GetTask(context.Background(), task.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("task not removed: %v", err)
	}
}

func TestCancelNotRunning(t *testing.T) {
	f := newEngFixture(t, faultserver.Options{Size: 1 << 20}, nil)
	task := f.add(t, AddRequest{})
	if err := f.e.Cancel(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.task(t, task.ID); got.State != domain.StateCancelled {
		t.Fatalf("state %s", got.State)
	}
}

func TestRemoveNeverDeletesCompleted(t *testing.T) {
	const size = 1 << 20
	f := newEngFixture(t, faultserver.Options{Size: size}, nil)
	task := f.add(t, AddRequest{})
	if err := f.run(t, task.ID); err != nil {
		t.Fatal(err)
	}
	final := f.task(t, task.ID).FinalPath
	if err := f.e.Remove(context.Background(), task.ID, true); err != nil {
		t.Fatal(err)
	}
	checkContent(t, final, engSeed, size)
}

func TestExistingDestinationPreserved(t *testing.T) {
	const size = 1 << 20
	f := newEngFixture(t, faultserver.Options{Size: size}, nil)
	orig := filepath.Join(f.dir, "file.bin")
	if err := os.WriteFile(orig, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	task := f.add(t, AddRequest{FileName: "file.bin"})
	if err := f.run(t, task.ID); err != nil {
		t.Fatal(err)
	}
	got := f.task(t, task.ID)
	if got.FinalPath != filepath.Join(f.dir, "file (1).bin") {
		t.Fatalf("final path %s", got.FinalPath)
	}
	checkContent(t, got.FinalPath, engSeed, size)
	if b, _ := os.ReadFile(orig); string(b) != "original" { //nolint:gosec // test path
		t.Fatalf("original modified: %q", b)
	}
}

func TestShutdown(t *testing.T) {
	const size = 10 << 20
	f := newEngFixture(t, faultserver.Options{Size: size, BytesPerSecondGlobal: 4 << 20}, nil)
	a, b := f.add(t, AddRequest{}), f.add(t, AddRequest{})
	ca, cb := f.startRun(a.ID), f.startRun(b.ID)
	waitFor(t, 10*time.Second, "both downloading", func() bool {
		return f.task(t, a.ID).State == domain.StateDownloading && f.task(t, b.ID).State == domain.StateDownloading
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := f.e.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	for _, ch := range []<-chan error{ca, cb} {
		if err := recvErr(t, ch); !errors.Is(err, domain.ErrPaused) {
			t.Fatalf("Run: %v", err)
		}
	}
	for _, id := range []string{a.ID, b.ID} {
		if s := f.task(t, id).State; s != domain.StatePaused {
			t.Fatalf("state %s", s)
		}
	}
	if err := f.run(t, a.ID); !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("Run after Shutdown: %v", err)
	}
}

func TestResumeExpiredSignedURL(t *testing.T) {
	const size = 10 << 20
	f := newEngFixture(t, faultserver.Options{Size: size, BytesPerSecondGlobal: 10 << 20}, nil)
	task := f.add(t, AddRequest{})
	if err := f.pauseAt(t, task.ID, 2<<20); !errors.Is(err, domain.ErrPaused) {
		t.Fatal(err)
	}
	f.srv.SetOptions(func(o *faultserver.Options) { o.ExpireAfter = time.Nanosecond })
	de := wantKind(t, f.run(t, task.ID), domain.KindAuth)
	if !strings.Contains(de.Action, "expired") {
		t.Fatalf("action %q", de.Action)
	}
	got := f.task(t, task.ID)
	if got.State != domain.StateFailed || strings.Contains(got.LastErrorMsg, "secret") {
		t.Fatalf("state %s msg %q", got.State, got.LastErrorMsg)
	}
	if segs, _ := f.store.LoadSegments(context.Background(), task.ID); len(segs) == 0 {
		t.Fatal("segments discarded")
	}
}

func TestPauseDuringInspecting(t *testing.T) {
	const size = 10 << 20
	f := newEngFixture(t, faultserver.Options{Size: size, JitterMax: 500 * time.Millisecond}, nil)
	task := f.add(t, AddRequest{})
	ch := f.startRun(task.ID)
	waitFor(t, 5*time.Second, "inspecting", func() bool { return f.task(t, task.ID).State == domain.StateInspecting })
	f.e.Pause(task.ID)
	if err := recvErr(t, ch); !errors.Is(err, domain.ErrPaused) {
		t.Fatalf("Run: %v", err)
	}
	if s := f.task(t, task.ID).State; s != domain.StatePaused {
		t.Fatalf("state %s", s)
	}
	f.srv.SetOptions(func(o *faultserver.Options) { o.JitterMax = 0 })
	if err := f.run(t, task.ID); err != nil {
		t.Fatal(err)
	}
	for _, ev := range f.events() {
		if w, ok := ev.(events.Warning); ok {
			t.Fatalf("unexpected warning: %s", w.Msg)
		}
	}
	checkContent(t, f.task(t, task.ID).FinalPath, engSeed, size)
}

func TestAddMissingDestDir(t *testing.T) {
	f := newEngFixture(t, faultserver.Options{Size: 1 << 20}, nil)
	dir := filepath.Join(f.dir, "missing")
	_, err := f.e.Add(context.Background(), AddRequest{URL: f.url(), DestDir: dir})
	de := wantKind(t, err, domain.KindLocal)
	if !strings.Contains(de.Msg, dir) {
		t.Fatalf("message %q lacks dir", de.Msg)
	}
	if l, _ := f.store.ListTasks(context.Background()); len(l) != 0 {
		t.Fatalf("%d tasks stored", len(l))
	}
}

func TestAddReadOnlyDestDir(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	f := newEngFixture(t, faultserver.Options{Size: 1 << 20}, nil)
	dir := filepath.Join(f.dir, "ro")
	if err := os.Mkdir(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) //nolint:gosec // test dir
	_, err := f.e.Add(context.Background(), AddRequest{URL: f.url(), DestDir: dir})
	de := wantKind(t, err, domain.KindLocal)
	if !strings.Contains(de.Msg, dir) {
		t.Fatalf("message %q lacks dir", de.Msg)
	}
	if l, _ := f.store.ListTasks(context.Background()); len(l) != 0 {
		t.Fatalf("%d tasks stored", len(l))
	}
}

func TestAddValidation(t *testing.T) {
	f := newEngFixture(t, faultserver.Options{Size: 1 << 20}, nil)
	for _, r := range []AddRequest{
		{URL: "ftp://x/y"},
		{URL: f.url(), ExpectedSHA256: "abc"},
		{URL: f.url(), ExistingPolicy: "bogus"},
		{URL: f.url(), Connections: 99},
		{URL: f.url(), RateLimit: -1},
	} {
		if _, err := f.e.Add(context.Background(), r); err == nil {
			t.Fatalf("Add(%+v) accepted", r)
		}
	}
}

func TestSameNameConcurrentTasks(t *testing.T) {
	const size = 4 << 20
	f := newEngFixture(t, faultserver.Options{Size: size, BytesPerSecondGlobal: 8 << 20}, nil)
	a, b := f.add(t, AddRequest{}), f.add(t, AddRequest{})
	ca, cb := f.startRun(a.ID), f.startRun(b.ID)
	waitFor(t, 10*time.Second, "both temp files", func() bool {
		return f.task(t, a.ID).TempPath != "" && f.task(t, b.ID).TempPath != ""
	})
	if ta, tb := f.task(t, a.ID).TempPath, f.task(t, b.ID).TempPath; ta == tb {
		t.Fatalf("shared temp file %s", ta)
	}
	if err := recvErr(t, ca); err != nil {
		t.Fatal(err)
	}
	if err := recvErr(t, cb); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, id := range []string{a.ID, b.ID} {
		p := f.task(t, id).FinalPath
		checkContent(t, p, engSeed, size)
		names[filepath.Base(p)] = true
	}
	if !names["file"] || !names["file (1)"] {
		t.Fatalf("final names %v", names)
	}
}

func TestOfflineWaitThenResume(t *testing.T) {
	const size = 8 << 20 // ranges proven (probe 2), so interruptions resume
	// Two outages with a task budget of 1: if offline waits spent budget,
	// the second wait would exhaust it and the task would fail.
	f := newEngFixture(t, faultserver.Options{
		Size: size, BytesPerSecondGlobal: 2 << 20,
		Outages: []faultserver.Outage{
			{After: 300 * time.Millisecond, Duration: time.Second},
			{After: 2300 * time.Millisecond, Duration: time.Second},
		},
	}, func(c *config.Config) { c.TaskRetryBudget = 1 })
	task := f.add(t, AddRequest{})
	if err := f.run(t, task.ID); err != nil {
		t.Fatal(err)
	}
	checkContent(t, f.task(t, task.ID).FinalPath, engSeed, size)
	waits := 0
	for _, ev := range f.events() {
		if sc, ok := ev.(events.StateChanged); ok && sc.To == domain.StateRetrying && sc.Reason == "waiting for network" {
			waits++
		}
	}
	if waits < 2 {
		t.Fatalf("%d offline waits, want >= 2: %v", waits, f.states(task.ID))
	}
}

func TestOfflineDuringAdd(t *testing.T) {
	const size = 1 << 20
	f := newEngFixture(t, faultserver.Options{Size: size}, nil)
	f.srv.SetDNSFailing(true)
	task := f.add(t, AddRequest{})
	go func() {
		time.Sleep(time.Second)
		f.srv.SetDNSFailing(false)
	}()
	if err := f.run(t, task.ID); err != nil {
		t.Fatal(err)
	}
	checkContent(t, f.task(t, task.ID).FinalPath, engSeed, size)
}

func TestProgressResetsBudget(t *testing.T) {
	const size = 20 << 20
	f := newEngFixture(t, faultserver.Options{Size: size, ResetAfterBytes: 2 << 20, ResetEveryNth: 1}, func(c *config.Config) {
		c.TaskRetryBudget, c.MaxSegmentAttempts = 3, 3
		c.MaxConnections = 2
		c.ChunkSizeMin = 8 << 20 // segments larger than the reset point
	})
	task := f.add(t, AddRequest{})
	if err := f.run(t, task.ID); err != nil {
		t.Fatal(err)
	}
	checkContent(t, f.task(t, task.ID).FinalPath, engSeed, size)
	if n, _ := f.store.ListAttempts(context.Background(), task.ID); len(n) <= 3 {
		t.Fatalf("only %d resets recorded", len(n))
	}
}

func TestRetryingStateOnTransient(t *testing.T) {
	const size = 1 << 20 // single stream: one probe, then the download
	f := newEngFixture(t, faultserver.Options{Size: size}, func(c *config.Config) { c.MaxSegmentAttempts = 1 })
	var once sync.Once
	f.wrapRT = func(rt http.RoundTripper) http.RoundTripper {
		return rtFunc(func(r *http.Request) (*http.Response, error) {
			resp, err := rt.RoundTrip(r)
			// After the first probe, fail the next two (download) requests.
			once.Do(func() {
				f.srv.SetOptions(func(o *faultserver.Options) { o.FailFirstN, o.FailStatus = 3, http.StatusServiceUnavailable })
			})
			return resp, err
		})
	}
	task := f.add(t, AddRequest{})
	if err := f.run(t, task.ID); err != nil {
		t.Fatal(err)
	}
	checkContent(t, f.task(t, task.ID).FinalPath, engSeed, size)
	if s := strings.Join(f.states(task.ID), ">"); !strings.Contains(s, "downloading>retrying>inspecting") {
		t.Fatalf("states %s", s)
	}
	atts, _ := f.store.ListAttempts(context.Background(), task.ID)
	if len(atts) == 0 {
		t.Fatal("no attempts recorded")
	}
	for _, a := range atts {
		if strings.Contains(a.Message, "secret") {
			t.Fatalf("attempt not redacted: %q", a.Message)
		}
	}
}

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestExplain(t *testing.T) {
	f := newEngFixture(t, faultserver.Options{Size: 20 << 20}, nil)
	d, obs, err := f.e.Explain(context.Background(), f.url(), AddRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if d.Strategy != domain.StrategyMultipart || !obs.RangesProven {
		t.Fatalf("decision %s", d.Summary())
	}
}

// fakeDNS returns a pure-Go resolver whose queries fail while failing is set
// and otherwise answer every A query with 127.0.0.1 (no AAAA records).
func fakeDNS(failing *atomic.Bool) *net.Resolver {
	return &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
		if failing.Load() {
			return nil, errors.New("fake dns: unreachable")
		}
		c, s := net.Pipe()
		go serveFakeDNS(s)
		return c, nil
	}}
}

// serveFakeDNS answers TCP-framed DNS queries on c (net.Pipe is not a
// PacketConn, so the resolver uses stream framing).
func serveFakeDNS(c net.Conn) {
	defer func() { _ = c.Close() }()
	for {
		var l [2]byte
		if _, err := io.ReadFull(c, l[:]); err != nil {
			return
		}
		q := make([]byte, int(l[0])<<8|int(l[1]))
		if _, err := io.ReadFull(c, q); err != nil || len(q) < 12 {
			return
		}
		end := 12
		for end < len(q) && q[end] != 0 {
			end += int(q[end]) + 1
		}
		end += 5 // root label, QTYPE, QCLASS
		if end > len(q) {
			return
		}
		qtype := int(q[end-4])<<8 | int(q[end-3])
		resp := append([]byte(nil), q[:end]...)
		resp[2], resp[3] = 0x81, 0x80 // response, RD, RA, NOERROR
		resp[6], resp[7], resp[8], resp[9], resp[10], resp[11] = 0, 0, 0, 0, 0, 0
		if qtype == 1 {
			resp[7] = 1
			resp = append(resp, 0xC0, 12, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4, 127, 0, 0, 1)
		}
		framed := binary.BigEndian.AppendUint16(nil, uint16(len(resp))) //nolint:gosec // DNS message < 64 KiB
		framed = append(framed, resp...)
		if _, err := c.Write(framed); err != nil {
			return
		}
	}
}

func TestOfflineHostResolution(t *testing.T) {
	const size = 1 << 20
	var failing atomic.Bool
	failing.Store(true)
	res := fakeDNS(&failing)
	f := newEngFixture(t, faultserver.Options{Size: size}, nil, func(d *EngineDeps) {
		d.Resolver = res
		cfg := d.Config
		d.NewClient = func(allowPrivate bool) *http.Client {
			dialer := &net.Dialer{Resolver: res}
			return transport.NewClient(transport.ClientOptions{
				Policy: cfg.NetworkPolicy, AllowPrivate: allowPrivate, Dialer: dialer.DialContext,
			})
		}
	})
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(f.srv.URL, "http://"))
	task := f.add(t, AddRequest{URL: "http://swiftfetch-test.invalid:" + port + "/file"})
	go func() {
		waitFor(t, 10*time.Second, "waiting for network", func() bool {
			for _, ev := range f.events() {
				if sc, ok := ev.(events.StateChanged); ok && sc.Reason == "waiting for network" {
					return true
				}
			}
			return false
		})
		failing.Store(false)
	}()
	if err := f.run(t, task.ID); err != nil {
		t.Fatal(err)
	}
	checkContent(t, f.task(t, task.ID).FinalPath, engSeed, size)
}

func TestCancelCompleted(t *testing.T) {
	f := newEngFixture(t, faultserver.Options{Size: 1 << 20}, nil)
	task := f.add(t, AddRequest{})
	if err := f.run(t, task.ID); err != nil {
		t.Fatal(err)
	}
	_ = wantKind(t, f.e.Cancel(context.Background(), task.ID), domain.KindPermanent)
	if s := f.task(t, task.ID).State; s != domain.StateCompleted {
		t.Fatalf("state %s", s)
	}
}
