package integration

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/raptordnj/goti/internal/config"
	"github.com/raptordnj/goti/internal/domain"
	"github.com/raptordnj/goti/internal/downloader"
	"github.com/raptordnj/goti/internal/strategy"
	"github.com/raptordnj/goti/tests/faultserver"
)

const (
	mib = int64(1) << 20
	// big is above MinMultipartSize, so single-stream outcomes are caused by
	// the fault, not by the size.
	big   = 16 * mib
	small = 2 * mib
)

var lastMod = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// matrixCase is one row of the spec §18.1 matrix.
type matrixCase struct {
	name string
	opts faultserver.Options
	ho   harnessOpts
	req  downloader.AddRequest
	// url overrides the request URL (given the server).
	url func(*faultserver.Server) string
	// during runs concurrently with Run (e.g. to lift a fault after retries).
	// It must return once stop is closed and must not call t.Fatal.
	during  func(h *harness, id string, stop <-chan struct{})
	timeout time.Duration
	long    bool // skipped in -short

	state    domain.State
	kind     domain.ErrorKind // for failed / paused
	strategy domain.Strategy  // "" = not checked
	check    func(t *testing.T, h *harness, task *domain.Task)
	// corrupt marks a completed download whose bytes are expected to differ
	// from the generator (no trusted checksum to catch it).
	corrupt bool
}

func TestMatrix(t *testing.T) {
	cases := []matrixCase{
		{name: "correct-ranges-50MiB", opts: faultserver.Options{Size: 50 * mib, ETag: "v1"},
			state: domain.StateCompleted, strategy: domain.StrategyMultipart},
		{name: "ignore-ranges", opts: faultserver.Options{Size: big, ETag: "v1", IgnoreRanges: true},
			state: domain.StateCompleted, strategy: domain.StrategySingle},
		{name: "malformed-content-range", opts: faultserver.Options{Size: big, ETag: "v1", MalformedContentRange: true},
			state: domain.StateCompleted, strategy: domain.StrategySingle},
		{name: "wrong-total", opts: faultserver.Options{Size: big, ETag: "v1", WrongTotal: true},
			state: domain.StateCompleted, strategy: domain.StrategySingle},
		{
			// Every response is short until a retry has been observed; then
			// the fault is lifted (a permanently short server can never
			// deliver the final ShortBodyBy bytes of any range).
			name: "short-body", opts: faultserver.Options{Size: small, ETag: "v1", ShortBodyBy: 4096},
			during: func(h *harness, id string, stop <-chan struct{}) {
				for !h.sawRetry(id) {
					select {
					case <-stop:
						return
					case <-time.After(5 * time.Millisecond):
					}
				}
				h.srv.SetOptions(func(o *faultserver.Options) { o.ShortBodyBy = 0 })
			},
			state: domain.StateCompleted,
			check: func(t *testing.T, h *harness, task *domain.Task) {
				if !h.sawRetry(task.ID) {
					t.Error("no retry observed")
				}
			},
		},
		{name: "weak-etag-only", opts: faultserver.Options{Size: big, ETag: "v1", WeakETag: true},
			state: domain.StateCompleted, strategy: domain.StrategySingle},
		{name: "no-validators", opts: faultserver.Options{Size: big, NoValidators: true},
			state: domain.StateCompleted, strategy: domain.StrategySingle},
		{
			name: "last-modified-only-50MiB", opts: faultserver.Options{Size: 50 * mib, LastModified: lastMod},
			state: domain.StateCompleted, strategy: domain.StrategyMultipart,
			check: func(t *testing.T, _ *harness, task *domain.Task) {
				var d strategy.Decision
				if err := json.Unmarshal(task.DecisionJSON, &d); err != nil {
					t.Fatal(err)
				}
				if d.Confidence != strategy.ConfidenceMedium {
					t.Errorf("confidence %s, want medium", d.Confidence)
				}
			},
		},
		{
			// Throttled so the change (after the inspection requests) lands
			// while segments are still being fetched.
			name:  "etag-change-mid-download",
			opts:  faultserver.Options{Size: big, ETag: "v1", ChangeETagAfter: 4, BytesPerSecondPerConn: 2 * mib},
			state: domain.StatePaused, kind: domain.KindRemoteChanged,
			check: func(t *testing.T, h *harness, task *domain.Task) {
				if task.FinalPath != "" {
					t.Errorf("final path %q set", task.FinalPath)
				}
				assertNoFinalFile(t, h.dir)
			},
		},
		{name: "redirect-chain-5", opts: faultserver.Options{Size: small, ETag: "v1", RedirectChain: 5},
			url: func(s *faultserver.Server) string { return s.URL + "/redirect" }, state: domain.StateCompleted},
		redirectToPrivateCase(),
		{
			name:  "expire-after-200ms-throttled",
			opts:  faultserver.Options{Size: 8 * mib, ETag: "v1", ExpireAfter: 200 * time.Millisecond, BytesPerSecondGlobal: 1 * mib},
			state: domain.StateFailed, kind: domain.KindAuth,
		},
		{name: "401", opts: faultserver.Options{Size: small, ETag: "v1", StatusOverride: http.StatusUnauthorized},
			state: domain.StateFailed, kind: domain.KindAuth},
		{name: "403", opts: faultserver.Options{Size: small, ETag: "v1", StatusOverride: http.StatusForbidden},
			state: domain.StateFailed, kind: domain.KindAuth},
		{name: "404", opts: faultserver.Options{Size: small, ETag: "v1", StatusOverride: http.StatusNotFound},
			state: domain.StateFailed, kind: domain.KindPermanent},
		{name: "410", opts: faultserver.Options{Size: small, ETag: "v1", StatusOverride: http.StatusGone},
			state: domain.StateFailed, kind: domain.KindPermanent},
		{name: "416-empty", opts: faultserver.Options{Size: 0, ETag: "v1"}, state: domain.StateCompleted},
		{
			// The first two requests (probe 1 and its retry) get 429; the
			// inspection retries them itself honouring Retry-After (spec
			// §4.1), which the decision records as two 429 probe results.
			name:  "429-retry-after-x2",
			opts:  faultserver.Options{Size: small, ETag: "v1", FailFirstN: 2, FailStatus: http.StatusTooManyRequests, RetryAfter: time.Second},
			state: domain.StateCompleted,
			check: func(t *testing.T, _ *harness, task *domain.Task) {
				var d strategy.Decision
				if err := json.Unmarshal(task.DecisionJSON, &d); err != nil {
					t.Fatal(err)
				}
				n429 := 0
				for _, p := range d.Probes {
					if p.Status == http.StatusTooManyRequests {
						n429++
					}
				}
				if n429 != 2 {
					t.Errorf("%d probes answered 429, want 2", n429)
				}
				if el := task.UpdatedAt.Sub(task.CreatedAt); el < 2*time.Second {
					t.Errorf("finished after %s: Retry-After 1s x2 not honoured", el)
				}
			},
		},
		{
			name:  "503-max-concurrent-2",
			opts:  faultserver.Options{Size: big, ETag: "v1", MaxConcurrent: 2},
			state: domain.StateCompleted,
			check: func(t *testing.T, h *harness, _ *domain.Task) {
				if m := h.srv.ActiveMax(); m > 3 {
					t.Errorf("ActiveMax %d, want <= 3", m)
				}
			},
		},
		{name: "variable-bandwidth-jitter",
			opts:  faultserver.Options{Size: small, ETag: "v1", JitterMax: 2 * time.Millisecond, BytesPerSecondPerConn: 4 * mib},
			state: domain.StateCompleted},
		{name: "reset-every-3rd", opts: faultserver.Options{Size: big, ETag: "v1", ResetEveryNth: 3, ResetAfterBytes: 512 << 10},
			state: domain.StateCompleted},
		// Truncation: every response ends cleanly (FIN) after 1 MiB. Big
		// enough for proven ranges: an unproven download can only restart
		// from zero (spec §5.1), so it would never get past the cut.
		{name: "mid-stream-truncation", opts: faultserver.Options{Size: big, ETag: "v1", CloseAfterBytes: 1 << 20},
			state: domain.StateCompleted},
		{
			name: "corrupted-no-checksum", opts: faultserver.Options{Size: small, ETag: "v1", CorruptAt: []int64{12345}},
			state: domain.StateCompleted, corrupt: true,
			check: func(t *testing.T, _ *harness, task *domain.Task) {
				if !strings.Contains(task.VerifyResult, "no trusted checksum") {
					t.Errorf("VerifyResult %q", task.VerifyResult)
				}
			},
		},
		{name: "corrupted-with-checksum", opts: faultserver.Options{Size: small, ETag: "v1", CorruptAt: []int64{12345}},
			req:   downloader.AddRequest{ExpectedSHA256: hexSum(small)},
			state: domain.StateFailed, kind: domain.KindIntegrity,
			check: func(t *testing.T, h *harness, _ *domain.Task) { assertNoFinalFile(t, h.dir) }},
		// Premature close: every response is aborted (RST) after 1 MiB.
		{name: "premature-close", opts: faultserver.Options{Size: big, ETag: "v1", ResetAfterBytes: 1 << 20},
			state: domain.StateCompleted},
		{name: "unknown-length", opts: faultserver.Options{Size: big, ETag: "v1", UnknownLength: true},
			state: domain.StateCompleted, strategy: domain.StrategySingle},
		{name: "5GiB-sparse", opts: faultserver.Options{Size: 5 << 30, ETag: "v1"}, long: true, timeout: 10 * time.Minute,
			state: domain.StateCompleted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.long && testing.Short() {
				t.Skip("long row skipped in -short")
			}
			runMatrixCase(t, tc)
		})
	}
}

func runMatrixCase(t *testing.T, tc matrixCase) {
	h := newHarness(t, tc.opts, tc.ho)
	req := tc.req
	if tc.url != nil {
		req.URL = tc.url(h.srv)
	}
	task := h.add(t, req)
	stop := make(chan struct{})
	duringDone := make(chan struct{})
	go func() {
		defer close(duringDone)
		if tc.during != nil {
			tc.during(h, task.ID, stop)
		}
	}()
	timeout := tc.timeout
	if timeout == 0 {
		timeout = 2 * time.Minute
	}
	task = h.run(t, task.ID, timeout)
	close(stop)
	<-duringDone

	if task.State != tc.state {
		t.Fatalf("state %s, want %s (kind %s, msg %q)", task.State, tc.state, task.LastErrorKind, task.LastErrorMsg)
	}
	if tc.state != domain.StateCompleted && task.LastErrorKind != tc.kind {
		t.Errorf("kind %s, want %s (msg %q)", task.LastErrorKind, tc.kind, task.LastErrorMsg)
	}
	if tc.strategy != "" && task.Strategy != tc.strategy {
		t.Errorf("strategy %s, want %s", task.Strategy, tc.strategy)
	}
	if tc.state == domain.StateCompleted {
		sum, n := fileSHA256(t, task.FinalPath)
		if n != tc.opts.Size {
			t.Errorf("size %d, want %d", n, tc.opts.Size)
		}
		match := sum == h.expectedSHA256(tc.opts.Size)
		if match == tc.corrupt {
			t.Errorf("SHA-256 match=%v, want %v", match, !tc.corrupt)
		}
	}
	if tc.check != nil {
		tc.check(t, h, task)
	}
}

// redirectToPrivateCase: the request URL names a public address
// (203.0.113.10, TEST-NET-3), so the engine builds the client with
// AllowPrivate=false. The test dialer routes that address to the fault
// server and reports the public address as the connection's remote end, so
// the origin passes the address policy. The server then redirects to its
// real loopback address, which the policy must refuse.
func redirectToPrivateCase() matrixCase {
	const publicIP = "203.0.113.10"
	var srvAddr string
	tc := matrixCase{
		name: "redirect-to-private",
		opts: faultserver.Options{Size: small, ETag: "v1", RedirectChain: 1},
		ho: harnessOpts{
			cfg: func(c *config.Config) { c.NetworkPolicy = domain.NetAllowDirectPrivate },
			dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				var d net.Dialer
				if host != publicIP {
					return d.DialContext(ctx, network, addr)
				}
				_, realPort, _ := net.SplitHostPort(srvAddr)
				if port != realPort {
					return nil, &net.OpError{Op: "dial", Net: network, Err: os.ErrNotExist}
				}
				c, err := d.DialContext(ctx, network, srvAddr)
				if err != nil {
					return nil, err
				}
				p, _ := strconv.Atoi(port)
				return publicConn{Conn: c, remote: &net.TCPAddr{IP: net.ParseIP(publicIP), Port: p}}, nil
			},
		},
		state: domain.StateFailed, kind: domain.KindPolicy,
	}
	tc.url = func(s *faultserver.Server) string {
		srvAddr = strings.TrimPrefix(s.URL, "http://")
		s.SetOptions(func(o *faultserver.Options) { o.RedirectTo = s.URL + "/file" })
		_, port, _ := net.SplitHostPort(srvAddr)
		return "http://" + net.JoinHostPort(publicIP, port) + "/redirect"
	}
	return tc
}

// publicConn reports a substitute remote address.
type publicConn struct {
	net.Conn
	remote net.Addr
}

func (c publicConn) RemoteAddr() net.Addr { return c.remote }

// assertNoFinalFile fails if dir holds anything but partial files.
func assertNoFinalFile(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".goti-part" {
			t.Errorf("unexpected file %s in destination", e.Name())
		}
	}
}
