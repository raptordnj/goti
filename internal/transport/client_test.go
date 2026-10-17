package transport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/raptordnj/swiftfetch/internal/domain"
	"github.com/raptordnj/swiftfetch/tests/faultserver"
)

func TestIsNonPublic(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1":       true,
		"10.1.2.3":        true,
		"192.168.0.1":     true,
		"169.254.1.1":     true,
		"::1":             true,
		"fd00::1":         true,
		"100.64.0.1":      true,
		"::ffff:10.0.0.1": true,
		"0.0.0.0":         true,
		"224.0.0.1":       true,
		"fe80::1":         true,
		"8.8.8.8":         false,
		"2606:4700::1":    false,
		"::ffff:8.8.8.8":  false,
	}
	for s, want := range cases {
		if got := isNonPublic(netip.MustParseAddr(s)); got != want {
			t.Errorf("isNonPublic(%s)=%v want %v", s, got, want)
		}
	}
}

func get(t *testing.T, c *http.Client, url string, hdr map[string]string) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return c.Do(req)
}

func TestHostIsPrivate(t *testing.T) {
	for u, want := range map[string]bool{
		"http://127.0.0.1/x": true,
		"http://[::1]:80/x":  true,
		"http://localhost/x": true,
		"http://8.8.8.8/x":   false,
	} {
		pu := mustURL(t, u)
		got, err := HostIsPrivate(context.Background(), nil, pu)
		if err != nil || got != want {
			t.Errorf("HostIsPrivate(%s)=%v,%v want %v", u, got, err, want)
		}
	}
}

func TestDialBlocksPrivateWhenNotAllowed(t *testing.T) {
	srv := faultserver.New(t, faultserver.Options{Size: 10})
	c := NewClient(ClientOptions{Policy: domain.NetAllowDirectPrivate, AllowPrivate: false})
	resp, err := get(t, c, srv.URL+"/file", nil)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected error")
	}
	if domain.KindOf(err) != domain.KindPolicy {
		t.Fatalf("kind=%v err=%v", domain.KindOf(err), err)
	}
}

func TestPolicySemantics(t *testing.T) {
	srv := faultserver.New(t, faultserver.Options{Size: 10})
	cases := []struct {
		name string
		opts ClientOptions
		ok   bool
	}{
		{"allow-all", ClientOptions{Policy: domain.NetAllowAll}, true},
		{"block-private ignores AllowPrivate", ClientOptions{Policy: domain.NetBlockPrivate, AllowPrivate: true}, false},
		{"direct private allowed", ClientOptions{Policy: domain.NetAllowDirectPrivate, AllowPrivate: true}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := get(t, NewClient(tc.opts), srv.URL+"/file", nil)
			if tc.ok {
				if err != nil {
					t.Fatal(err)
				}
				_ = resp.Body.Close()
				return
			}
			if err == nil || domain.KindOf(err) != domain.KindPolicy {
				t.Fatalf("want policy error, got %v", err)
			}
		})
	}
}

func TestDialAllowsDirectPrivate(t *testing.T) {
	srv := faultserver.New(t, faultserver.Options{Size: 10})
	c := NewClient(ClientOptions{Policy: domain.NetAllowDirectPrivate, AllowPrivate: true})
	resp, err := get(t, c, srv.URL+"/file", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestDialerHookIsPolicyChecked(t *testing.T) {
	srv := faultserver.New(t, faultserver.Options{Size: 10})
	c := NewClient(ClientOptions{Policy: domain.NetBlockPrivate, Dialer: srv.Dialer()})
	resp, err := get(t, c, srv.URL+"/file", nil)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected error")
	}
	if domain.KindOf(err) != domain.KindPolicy {
		t.Fatalf("kind=%v err=%v", domain.KindOf(err), err)
	}
}

func TestRedirectLimit(t *testing.T) {
	srv := faultserver.New(t, faultserver.Options{Size: 10, RedirectChain: 11})
	c := NewClient(ClientOptions{Policy: domain.NetAllowAll})
	if resp, err := get(t, c, srv.URL+"/redirect", nil); err == nil {
		_ = resp.Body.Close()
		t.Fatal("11 redirects should fail")
	}
	srv.SetOptions(func(o *faultserver.Options) { o.RedirectChain = 10 })
	resp, err := get(t, c, srv.URL+"/redirect", nil)
	if err != nil {
		t.Fatalf("10 redirects: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestHTTPSDowngradeBlocked(t *testing.T) {
	plain := faultserver.New(t, faultserver.Options{Size: 10})
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+"/file?token=secret", http.StatusFound)
	}))
	defer ts.Close()

	// ts.Client() trusts the test certificate; verification stays enabled.
	c := NewClient(ClientOptions{Policy: domain.NetAllowAll, RoundTripper: ts.Client().Transport})
	resp, err := get(t, c, ts.URL, nil)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected downgrade refusal")
	}
	if domain.KindOf(err) != domain.KindPolicy {
		t.Fatalf("kind=%v err=%v", domain.KindOf(err), err)
	}
	// net/http's *url.Error prints the raw URL; the policy error itself must not.
	var de *domain.Error
	if !errors.As(err, &de) || strings.Contains(de.Msg, "secret") {
		t.Errorf("policy error leaks query: %v", de)
	}

	c = NewClient(ClientOptions{Policy: domain.NetAllowAll, AllowHTTPSDowngrade: true, RoundTripper: ts.Client().Transport})
	resp, err = get(t, c, ts.URL, nil)
	if err != nil {
		t.Fatalf("downgrade allowed: %v", err)
	}
	_ = resp.Body.Close()
}

func TestAuthorizationStrippedCrossHost(t *testing.T) {
	gotAuth := make(chan string, 4)
	b := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotAuth <- r.Header.Get("Authorization")
	}))
	defer b.Close()
	port := b.Listener.Addr().String()[strings.LastIndex(b.Listener.Addr().String(), ":")+1:]
	a := faultserver.New(t, faultserver.Options{Size: 10, RedirectChain: 1, RedirectTo: "http://localhost:" + port + "/x"})

	c := NewClient(ClientOptions{Policy: domain.NetAllowAll})
	resp, err := get(t, c, a.URL+"/redirect", map[string]string{"Authorization": "Bearer tok"})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if v := <-gotAuth; v != "" {
		t.Fatalf("Authorization leaked cross-host: %q", v)
	}
}

func TestAuthorizationKeptSameHost(t *testing.T) {
	a := faultserver.New(t, faultserver.Options{Size: 10, RedirectChain: 1, RequireAuth: "Bearer tok"})
	c := NewClient(ClientOptions{Policy: domain.NetAllowAll})
	resp, err := get(t, c, a.URL+"/redirect", map[string]string{"Authorization": "Bearer tok"})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d (Authorization dropped on same host?)", resp.StatusCode)
	}
}

func TestClientTimeouts(t *testing.T) {
	c := NewClient(ClientOptions{})
	if c.Timeout != 0 {
		t.Fatalf("Client.Timeout=%v", c.Timeout)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport %T", c.Transport)
	}
	if tr.TLSHandshakeTimeout != 30*time.Second || tr.ResponseHeaderTimeout != 60*time.Second {
		t.Errorf("tls=%v header=%v", tr.TLSHandshakeTimeout, tr.ResponseHeaderTimeout)
	}
	if tr.MaxResponseHeaderBytes != 1<<20 || !tr.ForceAttemptHTTP2 {
		t.Errorf("maxhdr=%d h2=%v", tr.MaxResponseHeaderBytes, tr.ForceAttemptHTTP2)
	}
	if tr.HTTP2 == nil || tr.HTTP2.SendPingTimeout != 30*time.Second || tr.HTTP2.PingTimeout != 15*time.Second {
		t.Errorf("http2 config %+v", tr.HTTP2)
	}
	if tr.TLSClientConfig != nil && tr.TLSClientConfig.InsecureSkipVerify {
		t.Error("TLS verification disabled")
	}
	if tr.DialContext == nil {
		t.Error("no DialContext")
	}
}

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
