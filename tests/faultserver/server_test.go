package faultserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"
)

// newClient returns a client whose idle connections are closed at test end so
// no transport goroutines outlive the test.
func newClient(t *testing.T) *http.Client {
	t.Helper()
	tr := &http.Transport{}
	t.Cleanup(tr.CloseIdleConnections)
	return &http.Client{
		Transport: tr,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func do(t *testing.T, c *http.Client, url string, hdr map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func readAll(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return b
}

func generated(seed uint64, start, end int64) []byte {
	b := make([]byte, 0, end-start)
	for i := start; i < end; i++ {
		b = append(b, Byte(seed, i))
	}
	return b
}

func TestByteDeterministic(t *testing.T) {
	// Golden values pin the generator (splitmix64(0) = 0xe220a8397b1dcdaf).
	if Byte(0, 0) != 0xaf || Byte(7, 12345) != 0x59 || Byte(1<<40, 5<<30-1) != 0x45 {
		t.Fatal("Byte changed: generated content is no longer stable")
	}
	if bytes.Equal(generated(1, 0, 64), generated(2, 0, 64)) {
		t.Fatal("different seeds produced identical content")
	}
	want := sha256.Sum256(generated(9, 0, 200_000))
	if got := ExpectedSHA256(9, 200_000); got != want {
		t.Fatal("ExpectedSHA256 disagrees with Byte")
	}
}

func TestFullBodyMatchesGenerator(t *testing.T) {
	s := New(t, Options{Size: 100_000, Seed: 42})
	resp := do(t, newClient(t), s.URL+"/file", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if resp.ContentLength != 100_000 {
		t.Fatalf("Content-Length = %d", resp.ContentLength)
	}
	if got := sha256.Sum256(readAll(t, resp)); got != ExpectedSHA256(42, 100_000) {
		t.Fatal("body hash mismatch")
	}
	if got := resp.Header.Get("Accept-Ranges"); got != "bytes" {
		t.Fatalf("Accept-Ranges = %q", got)
	}
}

func TestRangeResponse(t *testing.T) {
	s := New(t, Options{Size: 100_000, Seed: 3})
	resp := do(t, newClient(t), s.URL+"/file?X-Amz-Signature=s", map[string]string{"Range": "bytes=10-19"})
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Range"); got != "bytes 10-19/100000" {
		t.Fatalf("Content-Range = %q", got)
	}
	if got := readAll(t, resp); !bytes.Equal(got, generated(3, 10, 20)) {
		t.Fatalf("body = %x", got)
	}
}

func TestRangeForms(t *testing.T) {
	s := New(t, Options{Size: 1000, Seed: 1})
	c := newClient(t)
	tests := []struct {
		rng, cr    string
		status     int
		start, end int64
	}{
		{"bytes=990-", "bytes 990-999/1000", 206, 990, 1000},
		{"bytes=-5", "bytes 995-999/1000", 206, 995, 1000},
		{"bytes=500-5000", "bytes 500-999/1000", 206, 500, 1000},
		{"bytes=1000-1001", "bytes */1000", 416, 0, 0},
		{"bytes=junk", "", 200, 0, 1000},
		{"bytes=0-1,5-6", "", 200, 0, 1000},
	}
	for _, tc := range tests {
		resp := do(t, c, s.URL+"/file", map[string]string{"Range": tc.rng})
		if resp.StatusCode != tc.status || resp.Header.Get("Content-Range") != tc.cr {
			t.Errorf("%s: status %d Content-Range %q", tc.rng, resp.StatusCode, resp.Header.Get("Content-Range"))
			continue
		}
		if tc.status == 416 {
			continue
		}
		if got := readAll(t, resp); !bytes.Equal(got, generated(1, tc.start, tc.end)) {
			t.Errorf("%s: body mismatch", tc.rng)
		}
	}
}

func TestEmptyFile(t *testing.T) {
	s := New(t, Options{Size: 0})
	resp := do(t, newClient(t), s.URL+"/file", map[string]string{"Range": "bytes=0-0"})
	if resp.StatusCode != http.StatusRequestedRangeNotSatisfiable || resp.Header.Get("Content-Range") != "bytes */0" {
		t.Fatalf("status %d Content-Range %q", resp.StatusCode, resp.Header.Get("Content-Range"))
	}
}

func TestIgnoreRanges(t *testing.T) {
	s := New(t, Options{Size: 1000, IgnoreRanges: true})
	resp := do(t, newClient(t), s.URL+"/file", map[string]string{"Range": "bytes=10-19"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if n := len(readAll(t, resp)); n != 1000 {
		t.Fatalf("body len = %d", n)
	}
}

func TestRangeIgnoredAfter(t *testing.T) {
	s := New(t, Options{Size: 1000, RangeIgnoredAfter: 1})
	c := newClient(t)
	h := map[string]string{"Range": "bytes=0-0"}
	if st := do(t, c, s.URL+"/file", h).StatusCode; st != 206 {
		t.Fatalf("first status = %d", st)
	}
	if st := do(t, c, s.URL+"/file", h).StatusCode; st != 200 {
		t.Fatalf("second status = %d", st)
	}
}

func TestValidators(t *testing.T) {
	lm := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	tests := []struct {
		name     string
		opts     Options
		etag, lm string
	}{
		{"none", Options{}, "", ""},
		{"strong", Options{ETag: "abc"}, `"abc"`, ""},
		{"weak", Options{ETag: "abc", WeakETag: true}, `W/"abc"`, ""},
		{"lm-only", Options{LastModified: lm}, "", "Fri, 02 Jan 2026 03:04:05 GMT"},
		{"suppressed", Options{ETag: "abc", LastModified: lm, NoValidators: true}, "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.opts.Size = 10
			tc.opts.ContentDisposition = `attachment; filename="a.bin"`
			s := New(t, tc.opts)
			resp := do(t, newClient(t), s.URL+"/file", nil)
			if got := resp.Header.Get("ETag"); got != tc.etag {
				t.Errorf("ETag = %q, want %q", got, tc.etag)
			}
			if got := resp.Header.Get("Last-Modified"); got != tc.lm {
				t.Errorf("Last-Modified = %q, want %q", got, tc.lm)
			}
			if got := resp.Header.Get("Content-Disposition"); got != `attachment; filename="a.bin"` {
				t.Errorf("Content-Disposition = %q", got)
			}
		})
	}
}

func TestIfRange(t *testing.T) {
	lm := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	tests := []struct {
		name    string
		opts    Options
		ifRange string
		status  int
	}{
		{"strong match", Options{ETag: "v1"}, `"v1"`, 206},
		{"strong mismatch", Options{ETag: "v1"}, `"v0"`, 200},
		{"weak never matches", Options{ETag: "v1", WeakETag: true}, `W/"v1"`, 200},
		{"date match", Options{LastModified: lm}, lm.Format(http.TimeFormat), 206},
		{"date mismatch", Options{LastModified: lm}, lm.Add(time.Hour).Format(http.TimeFormat), 200},
		{"no validators", Options{ETag: "v1", NoValidators: true}, `"v1"`, 200},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.opts.Size = 100
			s := New(t, tc.opts)
			resp := do(t, newClient(t), s.URL+"/file", map[string]string{"Range": "bytes=0-9", "If-Range": tc.ifRange})
			if resp.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.status)
			}
			got := s.Requests()
			if len(got) != 1 || got[0].IfRange != tc.ifRange || got[0].Range != "bytes=0-9" || got[0].Status != tc.status || got[0].Method != "GET" {
				t.Fatalf("log = %+v", got)
			}
		})
	}
}

func TestChangeETagAfter(t *testing.T) {
	s := New(t, Options{Size: 1000, Seed: 5, ETag: "abc", ChangeETagAfter: 2})
	c := newClient(t)
	var tags []string
	var bodies [][]byte
	for range 3 {
		resp := do(t, c, s.URL+"/file", nil)
		tags = append(tags, resp.Header.Get("ETag"))
		bodies = append(bodies, readAll(t, resp))
	}
	if tags[0] != `"abc"` || tags[1] != tags[0] || tags[2] == tags[0] {
		t.Fatalf("etags = %q", tags)
	}
	if sha256.Sum256(bodies[1]) != ExpectedSHA256(5, 1000) || sha256.Sum256(bodies[2]) != ExpectedSHA256(ChangedSeed(5), 1000) {
		t.Fatal("content did not switch to seed+1 with the ETag")
	}
}

func TestMaxConcurrent(t *testing.T) {
	s := New(t, Options{Size: 100, MaxConcurrent: 2, WriteLatency: 400 * time.Millisecond, RetryAfter: 2 * time.Second})
	c := newClient(t)
	var wg sync.WaitGroup
	statuses := make([]int, 3)
	retryAfter := make([]string, 3)
	for i := range 3 {
		wg.Go(func() {
			req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, s.URL+"/file", nil)
			resp, err := c.Do(req)
			if err != nil {
				t.Error(err)
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			statuses[i], retryAfter[i] = resp.StatusCode, resp.Header.Get("Retry-After")
		})
	}
	wg.Wait()
	n503 := 0
	for i, st := range statuses {
		if st == http.StatusServiceUnavailable {
			n503++
			if retryAfter[i] != "2" {
				t.Errorf("Retry-After = %q", retryAfter[i])
			}
		}
	}
	if n503 != 1 {
		t.Fatalf("statuses = %v, want exactly one 503", statuses)
	}
	if s.ActiveMax() != 3 {
		t.Fatalf("ActiveMax = %d", s.ActiveMax())
	}
}

func TestStatusGates(t *testing.T) {
	c := newClient(t)
	t.Run("override", func(t *testing.T) {
		s := New(t, Options{Size: 10, StatusOverride: http.StatusGone})
		if st := do(t, c, s.URL+"/file", nil).StatusCode; st != http.StatusGone {
			t.Fatalf("status = %d", st)
		}
	})
	t.Run("fail first n", func(t *testing.T) {
		s := New(t, Options{Size: 10, FailFirstN: 2, FailStatus: http.StatusTooManyRequests, RetryAfter: 1500 * time.Millisecond})
		for i, want := range []int{429, 429, 200} {
			resp := do(t, c, s.URL+"/file", nil)
			if resp.StatusCode != want {
				t.Fatalf("request %d status = %d", i, resp.StatusCode)
			}
			if want == 429 && resp.Header.Get("Retry-After") != "2" {
				t.Fatalf("Retry-After = %q", resp.Header.Get("Retry-After"))
			}
		}
	})
	t.Run("auth", func(t *testing.T) {
		s := New(t, Options{Size: 10, RequireAuth: "Bearer x"})
		if st := do(t, c, s.URL+"/file", nil).StatusCode; st != 401 {
			t.Fatalf("status = %d", st)
		}
		if st := do(t, c, s.URL+"/file", map[string]string{"Authorization": "Bearer x"}).StatusCode; st != 200 {
			t.Fatalf("authorized status = %d", st)
		}
	})
	t.Run("expire", func(t *testing.T) {
		s := New(t, Options{Size: 10, ExpireAfter: time.Nanosecond})
		time.Sleep(time.Millisecond)
		if st := do(t, c, s.URL+"/file", nil).StatusCode; st != 403 {
			t.Fatalf("status = %d", st)
		}
	})
	t.Run("not found", func(t *testing.T) {
		s := New(t, Options{Size: 10})
		if st := do(t, c, s.URL+"/nope", nil).StatusCode; st != 404 {
			t.Fatalf("status = %d", st)
		}
	})
	t.Run("set options", func(t *testing.T) {
		s := New(t, Options{Size: 10})
		s.SetOptions(func(o *Options) { o.StatusOverride = 404 })
		if st := do(t, c, s.URL+"/file", nil).StatusCode; st != 404 {
			t.Fatalf("status = %d", st)
		}
	})
}

func TestRedirectChain(t *testing.T) {
	s := New(t, Options{Size: 10, RedirectChain: 2})
	c := newClient(t)
	want := []string{"/redirect/1?a=b", "/file?a=b"}
	resp := do(t, c, s.URL+"/redirect?a=b", nil)
	for _, loc := range want {
		if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != loc {
			t.Fatalf("status %d Location %q, want %q", resp.StatusCode, resp.Header.Get("Location"), loc)
		}
		resp = do(t, c, s.URL+loc, nil)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("final status = %d", resp.StatusCode)
	}

	s.SetOptions(func(o *Options) { o.RedirectTo = "http://10.0.0.1/x" })
	resp = do(t, c, s.URL+"/redirect/1", nil)
	if resp.Header.Get("Location") != "http://10.0.0.1/x" {
		t.Fatalf("Location = %q", resp.Header.Get("Location"))
	}
	if got := s.Requests(); got[0].Path != "/redirect" || got[0].Status != http.StatusFound {
		t.Fatalf("log[0] = %+v", got[0])
	}
}

func TestContentRangeLies(t *testing.T) {
	c := newClient(t)
	h := map[string]string{"Range": "bytes=10-19"}
	s := New(t, Options{Size: 100, WrongTotal: true})
	if got := do(t, c, s.URL+"/file", h).Header.Get("Content-Range"); got != "bytes 10-19/101" {
		t.Errorf("wrong total: %q", got)
	}
	s = New(t, Options{Size: 100, MalformedContentRange: true})
	if got := do(t, c, s.URL+"/file", h).Header.Get("Content-Range"); got != "bytes 10-19" {
		t.Errorf("malformed: %q", got)
	}
	s = New(t, Options{Size: 100, LongBodyBy: 5})
	resp := do(t, c, s.URL+"/file", h)
	if got := readAll(t, resp); !bytes.Equal(got, generated(0, 10, 25)) || resp.Header.Get("Content-Range") != "bytes 10-19/100" {
		t.Errorf("long body: %d bytes, %q", len(got), resp.Header.Get("Content-Range"))
	}
}

func TestBodyFaults(t *testing.T) {
	c := newClient(t)
	readErr := func(t *testing.T, opts Options) ([]byte, error) {
		t.Helper()
		s := New(t, opts)
		resp := do(t, c, s.URL+"/file", nil)
		return io.ReadAll(resp.Body)
	}
	t.Run("short", func(t *testing.T) {
		b, err := readErr(t, Options{Size: 1000, ShortBodyBy: 100})
		if !errors.Is(err, io.ErrUnexpectedEOF) || len(b) != 900 {
			t.Fatalf("len %d err %v", len(b), err)
		}
	})
	for _, opts := range []Options{
		{Size: 100_000, ResetAfterBytes: 5000},
		{Size: 100_000, CloseAfterBytes: 5000},
		{Size: 100_000, CloseAfterBytes: 5000, UnknownLength: true},
	} {
		t.Run(fmt.Sprintf("cut %+v", opts), func(t *testing.T) {
			b, err := readErr(t, opts)
			if err == nil || len(b) > 5000 || !bytes.Equal(b, generated(0, 0, int64(len(b)))) {
				t.Fatalf("len %d err %v", len(b), err)
			}
		})
	}
	t.Run("reset every nth", func(t *testing.T) {
		s := New(t, Options{Size: 100_000, ResetAfterBytes: 10, ResetEveryNth: 2})
		for i, wantErr := range []bool{false, true, false, true} {
			resp := do(t, c, s.URL+"/file", nil)
			if _, err := io.ReadAll(resp.Body); (err != nil) != wantErr {
				t.Fatalf("request %d err = %v", i+1, err)
			}
		}
	})
	t.Run("corrupt", func(t *testing.T) {
		b, err := readErr(t, Options{Size: 100, CorruptAt: []int64{3, 50}})
		want := generated(0, 0, 100)
		want[3] ^= 0xFF
		want[50] ^= 0xFF
		if err != nil || !bytes.Equal(b, want) {
			t.Fatalf("err %v, corrupt bytes not applied", err)
		}
	})
	t.Run("unknown length", func(t *testing.T) {
		s := New(t, Options{Size: 50_000, UnknownLength: true, WritePiece: 1000})
		resp := do(t, c, s.URL+"/file", nil)
		if resp.ContentLength != -1 {
			t.Fatalf("ContentLength = %d", resp.ContentLength)
		}
		if sha256.Sum256(readAll(t, resp)) != ExpectedSHA256(0, 50_000) {
			t.Fatal("body mismatch")
		}
		resp = do(t, c, s.URL+"/file", map[string]string{"Range": "bytes=0-0"})
		if resp.StatusCode != 206 || resp.Header.Get("Content-Range") != "bytes 0-0/*" {
			t.Fatalf("status %d Content-Range %q", resp.StatusCode, resp.Header.Get("Content-Range"))
		}
	})
}

func TestThrottle(t *testing.T) {
	s := New(t, Options{Size: 400_000, BytesPerSecondPerConn: 200_000})
	start := time.Now()
	resp := do(t, newClient(t), s.URL+"/file", nil)
	if n := len(readAll(t, resp)); n != 400_000 {
		t.Fatalf("len = %d", n)
	}
	if d := time.Since(start); d < 1500*time.Millisecond {
		t.Fatalf("took %v, want >= 1.5s", d)
	}
}

func TestGlobalThrottleShared(t *testing.T) {
	s := New(t, Options{Size: 100_000, BytesPerSecondGlobal: 200_000, WritePiece: 4096})
	c := newClient(t)
	start := time.Now()
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, s.URL+"/file", nil)
			resp, err := c.Do(req)
			if err != nil {
				t.Error(err)
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		})
	}
	wg.Wait()
	// 300 KB through a shared 200 KB/s bucket must take well over 1 s.
	if d := time.Since(start); d < time.Second {
		t.Fatalf("took %v, want >= 1s", d)
	}
}

func TestDrops(t *testing.T) {
	s := New(t, Options{Size: 100_000, DropProbability: 0.5})
	c := newClient(t)
	early := 0
	for range 10 {
		resp := do(t, c, s.URL+"/file", nil)
		b, err := io.ReadAll(resp.Body)
		if err != nil || len(b) < 100_000 {
			early++
		}
	}
	if early == 0 {
		t.Fatal("no request ended early")
	}
}

func TestDropsDeterministicPerRequest(t *testing.T) {
	pattern := func() []int {
		s := New(t, Options{Size: 100_000, Seed: 77, DropProbability: 0.1, JitterMax: time.Millisecond})
		c := newClient(t)
		var early []int
		for i := range 20 {
			b, err := io.ReadAll(do(t, c, s.URL+"/file", nil).Body)
			if err != nil || len(b) < 100_000 {
				early = append(early, i)
			}
		}
		return early
	}
	a, b := pattern(), pattern()
	if len(a) == 0 || len(a) == 20 {
		t.Fatalf("degenerate drop pattern %v", a)
	}
	if !slices.Equal(a, b) {
		t.Fatalf("same seed gave different early-ended requests: %v vs %v", a, b)
	}
}

func TestWriteLatencyOncePerResponse(t *testing.T) {
	s := New(t, Options{Size: 100_000, WritePiece: 10_000, WriteLatency: 300 * time.Millisecond})
	start := time.Now()
	if n := len(readAll(t, do(t, newClient(t), s.URL+"/file", nil))); n != 100_000 {
		t.Fatalf("len = %d", n)
	}
	// Ten pieces: once-per-response latency is ~300 ms, per-piece would be ~3 s.
	if d := time.Since(start); d < 300*time.Millisecond || d > 900*time.Millisecond {
		t.Fatalf("took %v, want about 300ms", d)
	}
}

func TestCleanupWithStalledReader(t *testing.T) {
	var resp *http.Response
	done := make(chan time.Duration, 1)
	go func() {
		var cleanupStart time.Time
		t.Run("server", func(t *testing.T) {
			s := New(t, Options{Size: 1 << 30})
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, s.URL+"/file", nil)
			var err error
			resp, err = newClient(t).Do(req)
			if err != nil {
				t.Error(err)
				return
			}
			// Never read the body: let the handler block in Write once the
			// socket buffers fill, then measure how long cleanup takes.
			time.Sleep(200 * time.Millisecond)
			cleanupStart = time.Now()
		})
		done <- time.Since(cleanupStart)
	}()
	select {
	case d := <-done:
		if d > time.Second {
			t.Fatalf("cleanup took %v", d)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cleanup hung with a client that stopped reading")
	}
	if resp != nil {
		_ = resp.Body.Close()
	}
}

func TestBlackhole(t *testing.T) {
	s := New(t, Options{Size: 100_000, BlackholeAfterBytes: 1000})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.URL+"/file", nil)
	resp, err := newClient(t).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	buf := make([]byte, 1000)
	if _, err := io.ReadFull(resp.Body, buf); err != nil || !bytes.Equal(buf, generated(0, 0, 1000)) {
		t.Fatalf("prefix read: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := resp.Body.Read(make([]byte, 1))
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("read returned during blackhole: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("read succeeded after cancel")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("read still blocked after cancel")
	}
}

func TestBlackholeOnNth(t *testing.T) {
	s := New(t, Options{Size: 10, BlackholeOnNth: 2})
	c := newClient(t)
	_ = readAll(t, do(t, c, s.URL+"/file", nil))
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.URL+"/file", nil)
	resp, err := c.Do(req)
	if err == nil {
		_, err = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second request err = %v, want deadline exceeded", err)
	}
	_ = readAll(t, do(t, c, s.URL+"/file", nil))
}

func TestOutageWindow(t *testing.T) {
	s := New(t, Options{Size: 10, Outages: []Outage{{After: 100 * time.Millisecond, Duration: 600 * time.Millisecond}}})
	addr := s.URL[len("http://"):]
	c := newClient(t)
	_ = readAll(t, do(t, c, s.URL+"/file", nil))

	dial := func() error {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			_ = conn.Close()
		}
		return err
	}
	deadline := time.Now().Add(600 * time.Millisecond)
	for dial() == nil {
		if time.Now().After(deadline) {
			t.Fatal("dial never refused during outage window")
		}
		time.Sleep(10 * time.Millisecond)
	}
	deadline = time.Now().Add(3 * time.Second)
	for dial() != nil {
		if time.Now().After(deadline) {
			t.Fatal("dial still refused after outage window")
		}
		time.Sleep(20 * time.Millisecond)
	}
	c.CloseIdleConnections()
	if got := readAll(t, do(t, c, s.URL+"/file", nil)); len(got) != 10 {
		t.Fatalf("len = %d after outage", len(got))
	}
}

func TestDialerDNSFailure(t *testing.T) {
	s := New(t, Options{Size: 10})
	dial := s.Dialer()
	addr := s.URL[len("http://"):]
	s.SetDNSFailing(true)
	_, err := dial(t.Context(), "tcp", addr)
	var dnsErr *net.DNSError
	if !errors.As(err, &dnsErr) {
		t.Fatalf("err = %v, want *net.DNSError", err)
	}
	s.SetDNSFailing(false)
	conn, err := dial(t.Context(), "tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
}

func TestFiveGiBSparse(t *testing.T) {
	const size = 5 << 30
	s := New(t, Options{Size: size, Seed: 11})
	c := newClient(t)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	resp := do(t, c, s.URL+"/file", map[string]string{"Range": fmt.Sprintf("bytes=%d-", size-10)})
	got := readAll(t, resp)
	runtime.ReadMemStats(&after)
	if resp.StatusCode != 206 || resp.Header.Get("Content-Range") != fmt.Sprintf("bytes %d-%d/%d", size-10, size-1, size) {
		t.Fatalf("status %d Content-Range %q", resp.StatusCode, resp.Header.Get("Content-Range"))
	}
	if !bytes.Equal(got, generated(11, size-10, size)) {
		t.Fatalf("body = %x", got)
	}
	if d := after.TotalAlloc - before.TotalAlloc; d > 4<<20 {
		t.Fatalf("allocated %d bytes serving a 10-byte range", d)
	}
}

func TestNoGoroutineLeak(t *testing.T) {
	base := runtime.NumGoroutine()
	var resp *http.Response
	t.Run("server", func(t *testing.T) {
		s := New(t, Options{
			Size: 100_000, BlackholeAfterBytes: 100, BytesPerSecondGlobal: 50_000,
			Outages: []Outage{{After: time.Hour, Duration: time.Second}},
		})
		// Background context: only the server's cleanup may end this request.
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, s.URL+"/file", nil)
		var err error
		resp, err = newClient(t).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		// The body is left open: the server's cleanup alone must unblock the
		// blackholed handler and stop the outage scheduler.
	})
	if resp != nil {
		_ = resp.Body.Close()
	}
	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > base {
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<16)
			t.Fatalf("goroutines %d > baseline %d\n%s", runtime.NumGoroutine(), base, buf[:runtime.Stack(buf, true)])
		}
		time.Sleep(20 * time.Millisecond)
	}
}
