package downloader

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/raptordnj/swiftfetch/internal/config"
	"github.com/raptordnj/swiftfetch/internal/domain"
	"github.com/raptordnj/swiftfetch/internal/ratelimit"
	"github.com/raptordnj/swiftfetch/internal/storage"
	"github.com/raptordnj/swiftfetch/internal/strategy"
	"github.com/raptordnj/swiftfetch/internal/transport"
	"github.com/raptordnj/swiftfetch/tests/faultserver"
)

const testSeed = 7

// singleFixture is a runEnv wired to a fault server and a real temp file.
type singleFixture struct {
	env      *runEnv
	srv      *faultserver.Server
	mu       sync.Mutex
	attempts []domain.Attempt
	onRetry  func(n int) // optional test hook, called with the retry count
	bytes    atomic.Int64
}

func newSingleFixture(t *testing.T, opts faultserver.Options) *singleFixture {
	t.Helper()
	opts.Seed = testSeed
	srv := faultserver.New(t, opts)
	tf, err := storage.CreateTemp(t.TempDir(), "f.bin", opts.Size, !opts.UnknownLength)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tf.Close() })
	cfg := config.Default()
	cfg.StallTimeout = 200 * time.Millisecond
	f := &singleFixture{srv: srv}
	f.env = &runEnv{
		task: &domain.Task{ID: "t1", OriginalURL: srv.URL + "/file?token=secret"},
		decision: strategy.Decision{
			Strategy: domain.StrategySingle, Size: opts.Size, SizeKnown: !opts.UnknownLength, RangesProven: true,
		},
		client:  transport.NewClient(transport.ClientOptions{Policy: domain.NetAllowDirectPrivate, AllowPrivate: true}),
		file:    tf,
		cfg:     cfg,
		clock:   domain.RealClock{},
		backoff: Backoff{Base: time.Millisecond, Max: 2 * time.Millisecond, Factor: 2, Rand: rand.New(rand.NewPCG(1, 2))}, //nolint:gosec // test seed
		budget:  NewBudget(50),
		onBytes: func(n int64) { f.bytes.Add(n) },
		onRetry: func(a domain.Attempt) {
			f.mu.Lock()
			f.attempts = append(f.attempts, a)
			n := len(f.attempts)
			f.mu.Unlock()
			if f.onRetry != nil {
				f.onRetry(n)
			}
		},
		bufPool: newBufPool(),
	}
	return f
}

func (f *singleFixture) attemptList() []domain.Attempt {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.Attempt(nil), f.attempts...)
}

func (f *singleFixture) checkFile(t *testing.T, size int64) {
	t.Helper()
	data, err := os.ReadFile(f.env.file.Path())
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(data)) != size {
		t.Fatalf("file size %d, want %d", len(data), size)
	}
	if sha256.Sum256(data) != faultserver.ExpectedSHA256(testSeed, size) {
		t.Fatal("file content hash mismatch")
	}
	for _, a := range f.attemptList() {
		if strings.Contains(a.Message, "secret") {
			t.Fatalf("attempt message not redacted: %q", a.Message)
		}
	}
}

func run(t *testing.T, f *singleFixture) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return runSingle(ctx, f.env)
}

func TestSingleDownloadCorrect(t *testing.T) {
	const size = 3 << 20
	f := newSingleFixture(t, faultserver.Options{Size: size})
	if err := run(t, f); err != nil {
		t.Fatal(err)
	}
	f.checkFile(t, size)
	if got := f.bytes.Load(); got != size {
		t.Fatalf("onBytes total %d, want %d", got, size)
	}
	if !f.env.sched.AllDone() {
		t.Fatal("scheduler not done")
	}
}

func TestSingleResumesAfterReset(t *testing.T) {
	const size = 3 << 20
	f := newSingleFixture(t, faultserver.Options{Size: size, ETag: "v1", ResetAfterBytes: 1 << 20, ResetEveryNth: 1})
	f.env.task.Validators = domain.Validators{ETag: `"v1"`, ETagStrong: true}
	f.onRetry = func(n int) {
		if n == 2 {
			f.srv.SetOptions(func(o *faultserver.Options) { o.ResetAfterBytes, o.ResetEveryNth = 0, 0 })
		}
	}
	if err := run(t, f); err != nil {
		t.Fatal(err)
	}
	f.checkFile(t, size)
	found := false
	for _, r := range f.srv.Requests() {
		var start int64
		if _, err := fmt.Sscanf(r.Range, "bytes=%d-", &start); err == nil && start >= 1<<20 && r.IfRange == `"v1"` {
			found = true
		}
	}
	if !found {
		t.Fatalf("no resumed request with Range >= 1 MiB and If-Range: %+v", f.srv.Requests())
	}
	if n := len(f.attemptList()); n != 2 {
		t.Fatalf("attempts = %d, want 2", n)
	}
}

func TestSingleTruncationRetried(t *testing.T) {
	const size = 2 << 20
	f := newSingleFixture(t, faultserver.Options{Size: size, CloseAfterBytes: 700 << 10})
	f.onRetry = func(n int) {
		if n == 2 {
			f.srv.SetOptions(func(o *faultserver.Options) { o.CloseAfterBytes = 0 })
		}
	}
	if err := run(t, f); err != nil {
		t.Fatal(err)
	}
	f.checkFile(t, size)
	if len(f.attemptList()) == 0 {
		t.Fatal("expected at least one retry")
	}
}

func TestSingleUnknownLength(t *testing.T) {
	const size = 1<<20 + 12345
	f := newSingleFixture(t, faultserver.Options{Size: size, UnknownLength: true})
	f.env.decision.Size = 0
	if err := run(t, f); err != nil {
		t.Fatal(err)
	}
	f.checkFile(t, size)
	if got := f.env.sched.Total(); got != size {
		t.Fatalf("Total = %d, want %d", got, size)
	}
}

func TestSingleEmptyFile(t *testing.T) {
	f := newSingleFixture(t, faultserver.Options{Size: 0})
	if err := run(t, f); err != nil {
		t.Fatal(err)
	}
	f.checkFile(t, 0)
	if len(f.attemptList()) != 0 {
		t.Fatalf("unexpected retries: %+v", f.attemptList())
	}
}

func TestSingleStall(t *testing.T) {
	const size = 512 << 10
	f := newSingleFixture(t, faultserver.Options{Size: size, JitterMax: time.Hour})
	f.onRetry = func(int) {
		f.srv.SetOptions(func(o *faultserver.Options) { o.JitterMax = 0 })
	}
	if err := run(t, f); err != nil {
		t.Fatal(err)
	}
	f.checkFile(t, size)
	stalled := 0
	for _, a := range f.attemptList() {
		if a.Message == "stalled" && a.Kind == domain.KindTransient {
			stalled++
		}
	}
	if stalled != 1 {
		t.Fatalf("stalled attempts = %d, want 1 (%+v)", stalled, f.attemptList())
	}
}

func TestSingleSlowButProgressingNotStalled(t *testing.T) {
	const size = 300_000
	f := newSingleFixture(t, faultserver.Options{Size: size, BytesPerSecondPerConn: 50_000, WritePiece: 4096})
	if err := run(t, f); err != nil {
		t.Fatal(err)
	}
	f.checkFile(t, size)
	for _, a := range f.attemptList() {
		if a.Message == "stalled" {
			t.Fatalf("slow transfer classified as stalled: %+v", f.attemptList())
		}
	}
}

func TestSingleBudgetExhausted(t *testing.T) {
	f := newSingleFixture(t, faultserver.Options{Size: 1 << 20, FailFirstN: 100, FailStatus: 503})
	f.env.budget = NewBudget(3)
	err := run(t, f)
	if err == nil {
		t.Fatal("expected error")
	}
	if k := domain.KindOf(err); k != domain.KindTransient {
		t.Fatalf("kind = %v, want transient (%v)", k, err)
	}
	if n := len(f.attemptList()); n != 3 {
		t.Fatalf("onRetry called %d times, want 3", n)
	}
}

func TestSingleRateLimited(t *testing.T) {
	const size = 2 << 20
	f := newSingleFixture(t, faultserver.Options{Size: size})
	f.env.limits = []*ratelimit.Bandwidth{ratelimit.NewBandwidth(1 << 20), nil}
	start := time.Now()
	if err := run(t, f); err != nil {
		t.Fatal(err)
	}
	if el := time.Since(start); el < 1800*time.Millisecond {
		t.Fatalf("2 MiB at 1 MiB/s took %v, want >= 1.8s", el)
	}
	f.checkFile(t, size)
}

func TestSingleCancelNotRetried(t *testing.T) {
	f := newSingleFixture(t, faultserver.Options{Size: 1 << 20, BytesPerSecondPerConn: 100_000})
	ctx, cancel := context.WithCancelCause(context.Background())
	time.AfterFunc(100*time.Millisecond, func() { cancel(domain.ErrPaused) })
	err := runSingle(ctx, f.env)
	if err != domain.ErrPaused { //nolint:errorlint // the cause must be returned as-is
		t.Fatalf("err = %v, want ErrPaused", err)
	}
	if n := len(f.attemptList()); n != 0 {
		t.Fatalf("pause was retried %d times", n)
	}
}

func TestCopySegmentZeroAllocs(t *testing.T) {
	tf, err := storage.CreateTemp(t.TempDir(), "z.bin", 2<<20, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tf.Close() }()
	cfg := config.Default()
	env := &runEnv{file: tf, cfg: cfg, clock: domain.RealClock{}, bufPool: newBufPool(), onBytes: func(int64) {}}
	env.writeAt = tf.WriteAt
	src := make([]byte, 2<<20)
	ctx := context.Background()
	rd := bytes.NewReader(nil)
	allocs := func(n int64) float64 {
		return testing.AllocsPerRun(20, func() {
			s := NewScheduler(n, n)
			seg, _ := s.Acquire(0)
			rd.Reset(src[:n])
			if err := copySegment(ctx, env, seg, rd); err != nil {
				t.Fatal(err)
			}
		})
	}
	small, large := allocs(1<<20), allocs(2<<20)
	// 1 MiB more data is 16 more 64 KiB reads; they must not allocate.
	if large != small {
		t.Fatalf("allocs: 1 MiB=%v 2 MiB=%v; hot path allocates per read", small, large)
	}
	if small > 16 {
		t.Fatalf("allocs per 1 MiB copy = %v, want a small constant", small)
	}
}

func TestCopySegmentShortBody(t *testing.T) {
	tf, err := storage.CreateTemp(t.TempDir(), "s.bin", 100, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tf.Close() }()
	env := &runEnv{file: tf, cfg: config.Default(), clock: domain.RealClock{}, bufPool: newBufPool()}
	seg, _ := NewScheduler(100, 100).Acquire(0)
	if err := copySegment(context.Background(), env, seg, io.LimitReader(bytes.NewReader(make([]byte, 100)), 60)); err != io.ErrUnexpectedEOF { //nolint:errorlint // sentinel
		t.Fatalf("err = %v, want ErrUnexpectedEOF", err)
	}
	if seg.Pos() != 60 {
		t.Fatalf("Pos = %d, want 60", seg.Pos())
	}
}

// redactedFinalURL is what Task.FinalURL holds after inspection: a display
// string that must never be used as a request target.
const redactedFinalURL = "http://redacted.invalid/file?token=REDACTED"

func TestSingleRequestsOriginalURLNotFinalURL(t *testing.T) {
	const size = 1 << 20
	f := newSingleFixture(t, faultserver.Options{Size: size})
	f.env.task.FinalURL = redactedFinalURL
	if err := run(t, f); err != nil {
		t.Fatal(err)
	}
	f.checkFile(t, size)
}
