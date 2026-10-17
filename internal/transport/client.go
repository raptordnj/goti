package transport

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"syscall"
	"time"

	"github.com/raptordnj/swiftfetch/internal/domain"
)

const defaultMaxRedirects = 10

// ClientOptions configures NewClient.
type ClientOptions struct {
	Policy domain.NetworkPolicy
	// AllowPrivate permits non-public destinations under NetAllowDirectPrivate.
	// The caller sets it to HostIsPrivate(original URL).
	AllowPrivate bool
	// AllowHTTPSDowngrade permits https -> http redirects.
	AllowHTTPSDowngrade bool
	// MaxRedirects is the maximum number of redirects followed (default 10).
	MaxRedirects int
	// RoundTripper replaces the built transport (and with it the address
	// check). Tests use it to supply a transport that trusts a test CA.
	RoundTripper http.RoundTripper
	// Dialer is a test hook replacing the network dial. The address policy is
	// applied to the connection it returns.
	Dialer func(ctx context.Context, network, addr string) (net.Conn, error)
}

// NewClient builds an http.Client enforcing the address, redirect and
// downgrade policies. It sets no Client.Timeout; callers use contexts.
func NewClient(opts ClientOptions) *http.Client {
	maxRedirects := opts.MaxRedirects
	if maxRedirects <= 0 {
		maxRedirects = defaultMaxRedirects
	}
	rt := opts.RoundTripper
	if rt == nil {
		rt = newTransport(opts)
	}
	return &http.Client{
		Transport: rt,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return checkRedirect(opts, maxRedirects, req, via)
		},
	}
}

func newTransport(opts ClientOptions) *http.Transport {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	d := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 15 * time.Second}
	if opts.Dialer == nil {
		d.Control = func(_, address string, _ syscall.RawConn) error {
			return checkAddr(opts, address)
		}
		tr.DialContext = d.DialContext
	} else {
		dial := opts.Dialer
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := dial(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			if err := checkAddr(opts, conn.RemoteAddr().String()); err != nil {
				_ = conn.Close()
				return nil, err
			}
			return conn, nil
		}
	}
	tr.MaxResponseHeaderBytes = 1 << 20
	tr.ForceAttemptHTTP2 = true
	tr.TLSHandshakeTimeout = 30 * time.Second
	tr.ResponseHeaderTimeout = 60 * time.Second
	tr.HTTP2 = &http.HTTP2Config{
		SendPingTimeout: 30 * time.Second,
		PingTimeout:     15 * time.Second,
	}
	return tr
}

// checkAddr applies the network policy to a resolved "ip:port" address.
func checkAddr(opts ClientOptions, address string) error {
	if opts.Policy == domain.NetAllowAll {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return domain.NewError(domain.KindPolicy, "connection refused by network policy: unparseable address", "", err)
	}
	if !isNonPublic(ip.Unmap()) {
		return nil
	}
	if opts.Policy != domain.NetBlockPrivate && opts.AllowPrivate {
		return nil
	}
	return domain.NewError(domain.KindPolicy,
		"connection to non-public address "+ip.String()+" refused by network policy",
		"use a public URL or change the network policy", nil)
}

func checkRedirect(opts ClientOptions, maxRedirects int, req *http.Request, via []*http.Request) error {
	if len(via) > maxRedirects {
		return domain.NewError(domain.KindPermanent, "stopped after too many redirects", "", nil)
	}
	prev := via[len(via)-1]
	if prev.URL.Scheme == "https" && req.URL.Scheme == "http" && !opts.AllowHTTPSDowngrade {
		return domain.NewError(domain.KindPolicy,
			"refused https to http redirect to "+Redact(req.URL.String()),
			"enable AllowHTTPSDowngrade to permit it", nil)
	}
	if !sameHost(req.URL, prev.URL) || !sameHost(req.URL, via[0].URL) {
		req.Header.Del("Authorization")
		req.URL.User = nil
	}
	return nil
}

func sameHost(a, b *url.URL) bool {
	return a.Scheme == b.Scheme && a.Host == b.Host
}

// HostIsPrivate reports whether u's host is, or resolves to, a non-public
// address. A nil resolver means net.DefaultResolver.
func HostIsPrivate(ctx context.Context, r *net.Resolver, u *url.URL) (bool, error) {
	host := u.Hostname()
	if host == "" {
		return false, errors.New("transport: URL has no host")
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return isNonPublic(ip.Unmap()), nil
	}
	if r == nil {
		r = net.DefaultResolver
	}
	ips, err := r.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return false, err
	}
	for _, ip := range ips {
		if isNonPublic(ip.Unmap()) {
			return true, nil
		}
	}
	return false, nil
}

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// isNonPublic reports whether ip is loopback, private, link-local,
// multicast, unspecified, ULA or CGNAT (including IPv4-mapped forms).
func isNonPublic(ip netip.Addr) bool {
	ip = ip.Unmap()
	return !ip.IsValid() ||
		ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() || cgnat.Contains(ip)
}
