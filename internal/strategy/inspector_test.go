package strategy

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/raptordnj/goti/internal/clocktest"
	"github.com/raptordnj/goti/internal/config"
	"github.com/raptordnj/goti/internal/domain"
	"github.com/raptordnj/goti/internal/transport"
	"github.com/raptordnj/goti/tests/faultserver"
)

func newInspector(clock domain.Clock) *Inspector {
	if clock == nil {
		clock = domain.RealClock{}
	}
	return &Inspector{
		Client: transport.NewClient(transport.ClientOptions{AllowPrivate: true}),
		Cfg:    config.Default(),
		Clock:  clock,
	}
}

func inspect(t *testing.T, opts faultserver.Options, path string) (Observations, *faultserver.Server) {
	t.Helper()
	srv := faultserver.New(t, opts)
	obs, err := newInspector(nil).Inspect(context.Background(), srv.URL+path, nil)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	return obs, srv
}

func hasAnomaly(obs Observations, sub string) bool {
	for _, a := range obs.Anomalies {
		if strings.Contains(a, sub) {
			return true
		}
	}
	return false
}

func TestInspectRangesProven(t *testing.T) {
	obs, srv := inspect(t, faultserver.Options{Size: 20 << 20, ETag: "v1"}, "/file")
	if !obs.RangesProven || obs.Size != 20<<20 || !obs.SizeKnown {
		t.Fatalf("proven=%v size=%d known=%v anomalies=%v", obs.RangesProven, obs.Size, obs.SizeKnown, obs.Anomalies)
	}
	if len(obs.Probes) != 2 {
		t.Fatalf("probes = %d", len(obs.Probes))
	}
	if want := (domain.ByteRange{Start: 10 << 20, End: 10<<20 + 2}); obs.Probes[1].Requested != want {
		t.Fatalf("probe 2 range = %+v", obs.Probes[1].Requested)
	}
	if !obs.Validators.ETagStrong || obs.Validators.ETag != `"v1"` {
		t.Fatalf("validators = %+v", obs.Validators)
	}
	if reqs := srv.Requests(); reqs[1].IfRange != `"v1"` {
		t.Fatalf("probe 2 If-Range = %q", reqs[1].IfRange)
	}
	if obs.Dynamic || obs.Err != nil {
		t.Fatalf("dynamic=%v err=%v", obs.Dynamic, obs.Err)
	}
}

func TestInspectKnownValidatorsSetIfRange(t *testing.T) {
	srv := faultserver.New(t, faultserver.Options{Size: 20 << 20, ETag: "v1"})
	known := &domain.Validators{ETag: `"v0"`, ETagStrong: true}
	obs, err := newInspector(nil).Inspect(context.Background(), srv.URL+"/file", known)
	if err != nil {
		t.Fatal(err)
	}
	if got := srv.Requests()[1].IfRange; got != `"v0"` {
		t.Fatalf("If-Range = %q", got)
	}
	if obs.RangesProven {
		t.Fatal("ranges proven despite If-Range mismatch")
	}
}

func TestInspectIgnoreRanges(t *testing.T) {
	obs, srv := inspect(t, faultserver.Options{Size: 20 << 20, IgnoreRanges: true}, "/file")
	if obs.RangesProven || !obs.SizeKnown || obs.Size != 20<<20 {
		t.Fatalf("proven=%v size=%d known=%v", obs.RangesProven, obs.Size, obs.SizeKnown)
	}
	if n := len(srv.Requests()); n != 1 {
		t.Fatalf("requests = %d", n)
	}
	if !hasAnomaly(obs, "ignored Range") {
		t.Fatalf("anomalies = %v", obs.Anomalies)
	}
}

func TestInspectMalformedContentRange(t *testing.T) {
	for name, opts := range map[string]faultserver.Options{
		"malformed": {Size: 20 << 20, ETag: "v1", MalformedContentRange: true},
		"longbody":  {Size: 20 << 20, ETag: "v1", LongBodyBy: 1},
	} {
		t.Run(name, func(t *testing.T) {
			obs, _ := inspect(t, opts, "/file")
			if obs.RangesProven || len(obs.Anomalies) == 0 {
				t.Fatalf("proven=%v anomalies=%v", obs.RangesProven, obs.Anomalies)
			}
		})
	}
}

func TestInspectWrongTotal(t *testing.T) {
	obs, _ := inspect(t, faultserver.Options{Size: 20 << 20, ETag: "v1", WrongTotal: true}, "/file")
	if obs.RangesProven || !hasAnomaly(obs, "total differs") {
		t.Fatalf("proven=%v anomalies=%v", obs.RangesProven, obs.Anomalies)
	}
}

func TestInspectOfflineClosedPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	obs, err := newInspector(nil).Inspect(context.Background(), "http://"+addr+"/file", nil)
	if err != nil || obs.Err == nil || obs.Err.Kind != domain.KindOffline {
		t.Fatalf("err=%v obsErr=%v", err, obs.Err)
	}
}

func TestInspectOfflineDNS(t *testing.T) {
	srv := faultserver.New(t, faultserver.Options{Size: 10})
	srv.SetDNSFailing(true)
	in := newInspector(nil)
	in.Client = transport.NewClient(transport.ClientOptions{AllowPrivate: true, Dialer: srv.Dialer()})
	obs, err := in.Inspect(context.Background(), srv.URL+"/file", nil)
	if err != nil || obs.Err == nil || obs.Err.Kind != domain.KindOffline {
		t.Fatalf("err=%v obsErr=%v", err, obs.Err)
	}
}

func TestInspectRetryAfterCapped(t *testing.T) {
	srv := faultserver.New(t, faultserver.Options{Size: 10, FailFirstN: 1, RetryAfter: time.Hour})
	clock := clocktest.New(time.Unix(0, 0))
	done := make(chan Observations, 1)
	go func() {
		obs, _ := newInspector(clock).Inspect(context.Background(), srv.URL+"/file", nil)
		done <- obs
	}()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("Inspect did not finish")
		case obs := <-done:
			if obs.Err != nil || obs.ProbeRetries != 1 {
				t.Fatalf("err=%v retries=%d", obs.Err, obs.ProbeRetries)
			}
			return
		case <-time.After(time.Millisecond):
			if clock.Waiters() > 0 {
				clock.Advance(10 * time.Minute)
			}
		}
	}
}

func TestInspectSmallFileSkipsProbe2(t *testing.T) {
	obs, srv := inspect(t, faultserver.Options{Size: 1 << 20, ETag: "v1"}, "/file")
	if len(obs.Probes) != 1 || len(srv.Requests()) != 1 {
		t.Fatalf("probes=%d requests=%d", len(obs.Probes), len(srv.Requests()))
	}
	if obs.RangesProven || obs.Size != 1<<20 {
		t.Fatalf("proven=%v size=%d", obs.RangesProven, obs.Size)
	}
}

func TestInspectEmptyFile(t *testing.T) {
	obs, _ := inspect(t, faultserver.Options{Size: 0}, "/file")
	if !obs.SizeKnown || obs.Size != 0 || obs.Err != nil {
		t.Fatalf("known=%v size=%d err=%v", obs.SizeKnown, obs.Size, obs.Err)
	}
}

func TestInspectUnknownLength(t *testing.T) {
	obs, _ := inspect(t, faultserver.Options{Size: 20 << 20, UnknownLength: true}, "/file")
	if obs.SizeKnown || !obs.Dynamic || obs.RangesProven {
		t.Fatalf("known=%v dynamic=%v proven=%v", obs.SizeKnown, obs.Dynamic, obs.RangesProven)
	}
}

func TestInspectAuth(t *testing.T) {
	for status, kind := range map[int]domain.ErrorKind{
		401: domain.KindAuth, 403: domain.KindAuth, 404: domain.KindPermanent, 410: domain.KindPermanent,
	} {
		obs, _ := inspect(t, faultserver.Options{Size: 10, StatusOverride: status}, "/file")
		if obs.Err == nil || obs.Err.Kind != kind || obs.Status != status {
			t.Fatalf("status %d: err=%v status=%d", status, obs.Err, obs.Status)
		}
	}
}

func TestInspectRetryAfter(t *testing.T) {
	srv := faultserver.New(t, faultserver.Options{Size: 1 << 20, FailFirstN: 2, FailStatus: 503, RetryAfter: time.Second})
	clock := clocktest.New(time.Unix(0, 0))
	type result struct {
		obs Observations
		err error
	}
	done := make(chan result, 1)
	go func() {
		obs, err := newInspector(clock).Inspect(context.Background(), srv.URL+"/file", nil)
		done <- result{obs, err}
	}()
	deadline := time.After(5 * time.Second)
	for advanced := 0; ; {
		select {
		case <-deadline:
			t.Fatal("Inspect did not finish")
		case r := <-done:
			if r.err != nil || r.obs.Err != nil {
				t.Fatalf("err=%v obsErr=%v", r.err, r.obs.Err)
			}
			if r.obs.ProbeRetries != 2 || advanced != 2 || r.obs.Size != 1<<20 {
				t.Fatalf("retries=%d advanced=%d size=%d", r.obs.ProbeRetries, advanced, r.obs.Size)
			}
			return
		case <-time.After(time.Millisecond):
			if clock.Waiters() > 0 {
				clock.Advance(time.Second)
				advanced++
			}
		}
	}
}

func TestInspectRetryExhausted(t *testing.T) {
	srv := faultserver.New(t, faultserver.Options{Size: 10, StatusOverride: 429})
	clock := clocktest.New(time.Unix(0, 0))
	done := make(chan Observations, 1)
	go func() {
		obs, _ := newInspector(clock).Inspect(context.Background(), srv.URL+"/file", nil)
		done <- obs
	}()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("Inspect did not finish")
		case obs := <-done:
			if obs.Err == nil || obs.Err.Kind != domain.KindTransient || obs.ProbeRetries != 2 {
				t.Fatalf("err=%v retries=%d", obs.Err, obs.ProbeRetries)
			}
			return
		case <-time.After(time.Millisecond):
			if clock.Waiters() > 0 {
				clock.Advance(time.Minute)
			}
		}
	}
}

func TestInspectRedirects(t *testing.T) {
	obs, _ := inspect(t, faultserver.Options{Size: 10}, "/redirect/3")
	if !strings.HasSuffix(obs.FinalURL, "/file") || len(obs.Redirects) != 3 {
		t.Fatalf("final=%q redirects=%v", obs.FinalURL, obs.Redirects)
	}
}

func TestInspectFilename(t *testing.T) {
	obs, _ := inspect(t, faultserver.Options{Size: 10, ContentDisposition: `attachment; filename="plain.bin"; filename*=UTF-8''r%C3%A9sum%C3%A9.pdf`}, "/file")
	if obs.FileName != "résumé.pdf" || obs.FileNameSource != "content-disposition" {
		t.Fatalf("name=%q source=%q", obs.FileName, obs.FileNameSource)
	}
	obs, _ = inspect(t, faultserver.Options{Size: 10}, "/file")
	if obs.FileName != "file" || obs.FileNameSource != "url" {
		t.Fatalf("name=%q source=%q", obs.FileName, obs.FileNameSource)
	}
	obs, _ = inspect(t, faultserver.Options{Size: 10, RedirectTo: "/"}, "/redirect/1")
	if obs.FileName != "download" || obs.FileNameSource != "default" {
		t.Fatalf("name=%q source=%q", obs.FileName, obs.FileNameSource)
	}
}

func TestInspectSignedURL(t *testing.T) {
	obs, _ := inspect(t, faultserver.Options{Size: 10}, "/redirect/2?X-Amz-Signature=s")
	if !obs.SignedURL {
		t.Fatal("SignedURL not set")
	}
	for _, u := range append([]string{obs.FinalURL, obs.OriginalURL}, obs.Redirects...) {
		if strings.Contains(u, "=s") {
			t.Fatalf("unredacted URL %q", u)
		}
	}
}

func TestInspectInconsistentETag(t *testing.T) {
	obs, _ := inspect(t, faultserver.Options{Size: 20 << 20, ETag: "v1", ChangeETagAfter: 1}, "/file")
	if obs.RangesProven || !hasAnomaly(obs, "validator changed between probes") {
		t.Fatalf("proven=%v anomalies=%v", obs.RangesProven, obs.Anomalies)
	}
}

func TestInspectInvalidURL(t *testing.T) {
	in := newInspector(nil)
	for _, u := range []string{"ftp://x/y", "http:///nohost", "http://h/" + strings.Repeat("a", 8<<10)} {
		if _, err := in.Inspect(context.Background(), u, nil); err == nil {
			t.Fatalf("%q: no error", u)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := in.Inspect(ctx, "http://127.0.0.1:1/file", nil); err == nil {
		t.Fatal("cancelled ctx: no error")
	}
}

func TestInspectProbe2RetryExhausted(t *testing.T) {
	const size = 20 << 20
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		if r.Header.Get("Range") != "bytes=0-0" {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Range", "bytes 0-0/"+strconv.Itoa(size))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte{0})
	}))
	t.Cleanup(srv.Close)
	clock := clocktest.New(time.Unix(0, 0))
	done := make(chan Observations, 1)
	go func() {
		obs, _ := newInspector(clock).Inspect(context.Background(), srv.URL+"/file", nil)
		done <- obs
	}()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("Inspect did not finish")
		case obs := <-done:
			if obs.Err != nil || obs.RangesProven || obs.Size != size || obs.ProbeRetries != 2 || obs.Dynamic {
				t.Fatalf("err=%v proven=%v size=%d retries=%d dynamic=%v", obs.Err, obs.RangesProven, obs.Size, obs.ProbeRetries, obs.Dynamic)
			}
			if !hasAnomaly(obs, "probe 2 failed: server answered 503") {
				t.Fatalf("anomalies = %v", obs.Anomalies)
			}
			return
		case <-time.After(time.Millisecond):
			if clock.Waiters() > 0 {
				clock.Advance(time.Second)
			}
		}
	}
}

// A redirect refused by the client's address policy is a Policy error, not
// a transient one (otherwise the engine retries it until the budget runs out).
func TestInspectRedirectToPrivatePolicy(t *testing.T) {
	srv := faultserver.New(t, faultserver.Options{Size: 1 << 10, ETag: "v1", RedirectChain: 1})
	srv.SetOptions(func(o *faultserver.Options) { o.RedirectTo = srv.URL + "/file" })
	in := newInspector(nil)
	// Redirects to a non-public address are refused by the redirect check;
	// the first hop is allowed.
	in.Client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return domain.NewError(domain.KindPolicy, "connection to non-public address refused by network policy", "", nil)
	}
	obs, err := in.Inspect(context.Background(), srv.URL+"/redirect", nil)
	if err != nil {
		t.Fatal(err)
	}
	if obs.Err == nil || obs.Err.Kind != domain.KindPolicy {
		t.Fatalf("obs.Err = %v, want kind policy", obs.Err)
	}
}

// A connection lost while reading a probe body is a network failure, not
// evidence about range support: the inspection reports it as transient so
// the engine retries instead of downgrading to single-stream.
func TestInspectProbe1BodyResetIsTransient(t *testing.T) {
	obs, _ := inspect(t, faultserver.Options{Size: 20 << 20, ETag: "v1", ResetEveryNth: 1}, "/file")
	assertRangeProbeInterrupted(t, obs)
}

func TestInspectProbe2ResetIsTransient(t *testing.T) {
	obs, _ := inspect(t, faultserver.Options{Size: 20 << 20, ETag: "v1", ResetEveryNth: 2}, "/file")
	assertRangeProbeInterrupted(t, obs)
}

func assertRangeProbeInterrupted(t *testing.T, obs Observations) {
	t.Helper()
	if obs.Err == nil || obs.Err.Kind != domain.KindTransient || !obs.RangeProbeInterrupted || obs.RangesProven {
		t.Fatalf("err=%v interrupted=%v proven=%v anomalies=%v", obs.Err, obs.RangeProbeInterrupted, obs.RangesProven, obs.Anomalies)
	}
	if obs.Size != 20<<20 || !obs.SizeKnown {
		t.Fatalf("size %d known %v", obs.Size, obs.SizeKnown)
	}
	if hasAnomaly(obs, "body length") {
		t.Fatalf("interruption reported as a body-length anomaly: %v", obs.Anomalies)
	}
}

func TestInspectOfflineIsNotRangeProbeInterrupted(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	obs, _ := newInspector(nil).Inspect(context.Background(), "http://"+addr+"/file", nil)
	if obs.RangeProbeInterrupted {
		t.Fatal("connection refused before any response flagged as range-probe interruption")
	}
}
