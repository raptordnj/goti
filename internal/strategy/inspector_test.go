package strategy

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/raptordnj/swiftfetch/internal/clocktest"
	"github.com/raptordnj/swiftfetch/internal/config"
	"github.com/raptordnj/swiftfetch/internal/domain"
	"github.com/raptordnj/swiftfetch/internal/transport"
	"github.com/raptordnj/swiftfetch/tests/faultserver"
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
	// The Content-Range total is Size+1 on every response, which is
	// self-consistent across probes 1 and 2 at floor(N/2): spec §4.1 offers
	// no observation that can contradict it. See the task-14 report.
	t.Skip("WrongTotal is undetectable by the spec §4.1 probe sequence; awaiting controller ruling")
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
	for advanced := 0; ; {
		select {
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
	for {
		select {
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
