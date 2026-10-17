package downloader

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/raptordnj/swiftfetch/internal/domain"
	"github.com/raptordnj/swiftfetch/internal/strategy"
	"github.com/raptordnj/swiftfetch/tests/faultserver"
)

const testETag = "v1"

// mpFixture is a singleFixture configured for multipart.
type mpFixture struct {
	*singleFixture
	ctl        *strategy.Controller
	smu        sync.Mutex
	strategies []string
}

func newMPFixture(t *testing.T, opts faultserver.Options, initial, maxConns int, chunk int64) *mpFixture {
	t.Helper()
	if opts.ETag == "" {
		opts.ETag = testETag
	}
	f := &mpFixture{singleFixture: newSingleFixture(t, opts)}
	f.env.task.Validators = domain.Validators{ETag: `"` + opts.ETag + `"`, ETagStrong: true}
	f.env.decision.Strategy = domain.StrategyMultipart
	f.env.decision.InitialConns, f.env.decision.MaxConns, f.env.decision.ChunkSize = initial, maxConns, chunk
	f.env.cfg.MaxConnections = maxConns
	f.env.windowMin = 200 * time.Millisecond
	f.ctl = strategy.NewController(strategy.ControllerConfig{Initial: initial, Max: maxConns, Adaptive: true})
	return f
}

func (f *mpFixture) onStrategy(from, to domain.Strategy, reason string) {
	f.smu.Lock()
	f.strategies = append(f.strategies, string(from)+">"+string(to)+": "+reason)
	f.smu.Unlock()
}

func (f *mpFixture) run(t *testing.T) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return runMultipart(ctx, f.env, f.ctl, f.onStrategy)
}

// parsedRanges returns every logged closed Range as [start,end), sorted.
func parsedRanges(t *testing.T, logs []faultserver.RequestLog) []domain.ByteRange {
	t.Helper()
	var out []domain.ByteRange
	for _, r := range logs {
		var a, b int64
		if _, err := fmt.Sscanf(r.Range, "bytes=%d-%d", &a, &b); err != nil {
			t.Fatalf("unparsable Range %q", r.Range)
		}
		out = append(out, domain.ByteRange{Start: a, End: b + 1})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out
}

func TestMultipartCorrect(t *testing.T) {
	const size = 40 << 20
	f := newMPFixture(t, faultserver.Options{Size: size}, 2, 8, 4<<20)
	if err := f.run(t); err != nil {
		t.Fatal(err)
	}
	f.checkFile(t, size)
	logs := f.srv.Requests()
	for _, r := range logs {
		if r.Status != http.StatusPartialContent {
			t.Fatalf("status %d for %q, want only 206", r.Status, r.Range)
		}
		if r.IfRange != `"v1"` {
			t.Fatalf("If-Range %q", r.IfRange)
		}
	}
	rs := parsedRanges(t, logs)
	for i := 1; i < len(rs); i++ {
		if rs[i].Start < rs[i-1].End {
			t.Fatalf("overlapping ranges %v and %v", rs[i-1], rs[i])
		}
	}
	if got := f.bytes.Load(); got != size {
		t.Fatalf("onBytes %d, want %d", got, size)
	}
}

func TestMultipartScalesOnPerConnCap(t *testing.T) {
	const size = 64 << 20
	f := newMPFixture(t, faultserver.Options{Size: size, BytesPerSecondPerConn: 2 << 20}, 2, 8, 2<<20)
	if err := f.run(t); err != nil {
		t.Fatal(err)
	}
	f.checkFile(t, size)
	if got := f.srv.ActiveMax(); got < 4 {
		t.Fatalf("ActiveMax %d, want >= 4", got)
	}
}

func TestMultipartHoldsOnGlobalCap(t *testing.T) {
	const size = 24 << 20
	f := newMPFixture(t, faultserver.Options{Size: size, BytesPerSecondGlobal: 4 << 20}, 2, 8, 2<<20)
	if err := f.run(t); err != nil {
		t.Fatal(err)
	}
	f.checkFile(t, size)
	if got := f.srv.ActiveMax(); got > 3 {
		t.Fatalf("ActiveMax %d, want <= 3", got)
	}
}

func TestMultipart429Storm(t *testing.T) {
	const size = 16 << 20
	f := newMPFixture(t, faultserver.Options{Size: size, MaxConcurrent: 3, RetryAfter: time.Second, BytesPerSecondPerConn: 8 << 20}, 6, 8, 1<<20)
	if err := f.run(t); err != nil {
		t.Fatal(err)
	}
	f.checkFile(t, size)
	throttled := false
	for _, a := range f.attemptList() {
		if strings.Contains(a.Message, "503") || strings.Contains(a.Message, "429") {
			throttled = true
		}
	}
	if !throttled {
		t.Fatal("no 503/429 retry observed")
	}
}

func TestMultipartETagChangeAborts(t *testing.T) {
	const size = 32 << 20
	f := newMPFixture(t, faultserver.Options{Size: size, ChangeETagAfter: 5}, 2, 4, 1<<20)
	err := f.run(t)
	var de *domain.Error
	if !errors.As(err, &de) || de.Kind != domain.KindRemoteChanged {
		t.Fatalf("err = %v, want RemoteChanged", err)
	}
	if f.env.sched.AllDone() {
		t.Fatal("completed despite ETag change")
	}
	data := make([]byte, 0, 1<<20)
	for _, r := range f.env.sched.Records() {
		if !r.Done {
			continue
		}
		data = data[:r.End-r.Start]
		if _, err := f.env.file.ReadAt(data, r.Start); err != nil {
			t.Fatal(err)
		}
		for i := range data {
			if data[i] != faultserver.Byte(testSeed, r.Start+int64(i)) {
				t.Fatalf("segment %d has bytes of the changed resource", r.Start)
			}
		}
		if sum := sha256.Sum256(data); string(sum[:]) != string(r.Hash) {
			t.Fatalf("segment %d hash mismatch", r.Start)
		}
	}
}

func TestMultipartRangeIgnoredFillMode(t *testing.T) {
	const size = 16 << 20
	f := newMPFixture(t, faultserver.Options{Size: size, RangeIgnoredAfter: 4}, 2, 4, 1<<20)
	if err := f.run(t); err != nil {
		t.Fatal(err)
	}
	f.checkFile(t, size)
	f.smu.Lock()
	defer f.smu.Unlock()
	if len(f.strategies) != 1 || !strings.Contains(f.strategies[0], "fill mode") {
		t.Fatalf("strategies %q", f.strategies)
	}
}

func TestMultipartResets(t *testing.T) {
	const size = 24 << 20
	f := newMPFixture(t, faultserver.Options{Size: size, ResetAfterBytes: 3 << 20, ResetEveryNth: 3}, 2, 8, 4<<20)
	if err := f.run(t); err != nil {
		t.Fatal(err)
	}
	f.checkFile(t, size)
}

// slowTransport slows the response to the request whose Range starts at slowStart.
type slowTransport struct {
	base      http.RoundTripper
	slowStart string
}

func (s slowTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := s.base.RoundTrip(r)
	if err == nil && strings.HasPrefix(r.Header.Get("Range"), s.slowStart) {
		resp.Body = &slowBody{ReadCloser: resp.Body}
	}
	return resp, err
}

type slowBody struct{ io.ReadCloser }

func (b *slowBody) Read(p []byte) (int, error) {
	time.Sleep(10 * time.Millisecond)
	return b.ReadCloser.Read(p[:min(len(p), 16<<10)])
}

func TestMultipartEndgameSplit(t *testing.T) {
	const size, chunk = 8 << 20, 4 << 20
	f := newMPFixture(t, faultserver.Options{Size: size}, 2, 2, chunk)
	f.env.client.Transport = slowTransport{base: f.env.client.Transport, slowStart: fmt.Sprintf("bytes=%d-", chunk)}
	if err := f.run(t); err != nil {
		t.Fatal(err)
	}
	f.checkFile(t, size)
	split := false
	for _, r := range parsedRanges(t, f.srv.Requests()) {
		if r.Start%chunk != 0 {
			split = true
		}
	}
	if !split {
		t.Fatal("no request started mid-segment")
	}
}

func TestMultipartNoGoroutineLeak(t *testing.T) {
	const size = 64 << 20
	f := newMPFixture(t, faultserver.Options{Size: size, BytesPerSecondPerConn: 4 << 20}, 4, 8, 2<<20)
	base := runtime.NumGoroutine()
	ctx, cancel := context.WithCancelCause(context.Background())
	time.AfterFunc(300*time.Millisecond, func() { cancel(domain.ErrCancelled) })
	err := runMultipart(ctx, f.env, f.ctl, f.onStrategy)
	if !errors.Is(err, domain.ErrCancelled) {
		t.Fatalf("err = %v, want ErrCancelled", err)
	}
	f.env.client.CloseIdleConnections()
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > base+2 {
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<20)
			t.Fatalf("goroutines %d, baseline %d\n%s", runtime.NumGoroutine(), base, buf[:runtime.Stack(buf, true)])
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestMultipart5GiB(t *testing.T) {
	if testing.Short() {
		t.Skip("5 GiB download skipped in -short mode")
	}
	const size = 5 << 30
	f := newMPFixture(t, faultserver.Options{Size: size}, 4, 8, 64<<20)
	f.env.windowMin = 2 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := runMultipart(ctx, f.env, f.ctl, f.onStrategy); err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	if _, err := io.Copy(h, io.NewSectionReader(f.env.file, 0, size)); err != nil {
		t.Fatal(err)
	}
	if want := faultserver.ExpectedSHA256(testSeed, size); string(h.Sum(nil)) != string(want[:]) {
		t.Fatal("5 GiB content hash mismatch")
	}
}
