package faultserver

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

const defaultWritePiece = 16 << 10

// Outage is a window, measured from server start, during which the listener
// is closed so connection attempts are refused. Windows must not overlap.
type Outage struct {
	After, Duration time.Duration
}

// Options configures the served resource and the faults injected. The zero
// value serves an empty resource with correct range support and no faults.
//
// Counters ("after K requests", "every Nth", "on Nth") count requests to /file
// that pass the status gates (ExpireAfter, RequireAuth, StatusOverride,
// FailFirstN, MaxConcurrent); FailFirstN counts every request, redirects
// included. Byte thresholds count body bytes written in one response.
type Options struct {
	Size int64  // resource length in bytes
	Seed uint64 // content seed: byte i is Byte(Seed, i)

	UnknownLength         bool  // no Content-Length (chunked); 206 totals are "*"
	IgnoreRanges          bool  // always 200 with the full body; no Accept-Ranges
	MalformedContentRange bool  // 206 Content-Range lacks the "/total" part
	WrongTotal            bool  // 206 Content-Range total is Size+1
	ShortBodyBy           int64 // body ends this many bytes before Content-Length
	LongBodyBy            int64 // body and Content-Length exceed the requested length by this much

	ETag               string    // opaque tag sent quoted when non-empty
	WeakETag           bool      // send the ETag as W/"..." (never satisfies If-Range)
	LastModified       time.Time // sent when non-zero
	NoValidators       bool      // suppress ETag and Last-Modified
	ChangeETagAfter    int       // after K requests: ETag+"-changed", content ChangedSeed(Seed), LastModified+1h
	ContentDisposition string    // sent verbatim when non-empty

	RedirectChain int    // hop count for /redirect (no explicit N)
	RedirectTo    string // final redirect target instead of /file (verbatim)

	ExpireAfter    time.Duration // 403 once this long has elapsed since server start; 0 disables
	StatusOverride int           // answer every request with this status
	FailFirstN     int           // answer the first N requests with FailStatus
	FailStatus     int           // status for FailFirstN (default 503)
	RetryAfter     time.Duration // Retry-After for StatusOverride/FailFirstN/MaxConcurrent (rounded up to seconds)
	MaxConcurrent  int           // requests beyond this many in flight get 503 (Retry-After default 1 s)
	RequireAuth    string        // required Authorization header value, else 401

	BytesPerSecondPerConn int64         // token bucket per response (one HTTP/1.1 connection at a time)
	BytesPerSecondGlobal  int64         // token bucket shared by all responses
	JitterMax             time.Duration // random delay in [0, JitterMax) before each piece (same RNG as drops)
	WriteLatency          time.Duration // delay once per response before the first body byte (simulated RTT)
	WritePiece            int           // bytes per write+flush (default 16 KiB)

	ResetAfterBytes     int64   // abort the connection with RST after this many body bytes
	ResetEveryNth       int     // apply the reset only to every Nth request (reset at ResetAfterBytes, default 0)
	CorruptAt           []int64 // resource offsets whose byte is inverted on the wire
	CloseAfterBytes     int64   // close the connection (FIN) after this many body bytes
	RangeIgnoredAfter   int     // after K requests, Range is ignored (200)
	DropProbability     float64 // probability per piece of aborting the connection (RNG seeded from Seed and the request number)
	BlackholeAfterBytes int64   // stop writing after this many body bytes and hang until the client goes away
	BlackholeOnNth      int     // apply the blackhole only to the Nth request

	Outages []Outage // listener outage windows; only the value given to New is used
}

// RequestLog records one request as received, with the status it was answered with.
type RequestLog struct {
	Method, Range, IfRange string
	Status                 int
	Path                   string
}

// Server is a running fault server. Serve content from URL+"/file" (any query
// string is ignored) and redirect chains from URL+"/redirect[/N]".
type Server struct {
	URL string

	hs         *httptest.Server
	ln         *outageListener
	start      time.Time
	cancel     context.CancelFunc
	stop       chan struct{}
	scheduler  sync.WaitGroup
	global     *rate.Limiter
	dnsFailing atomic.Bool

	mu           sync.Mutex
	opts         Options
	requests     int
	fileRequests int
	active       int
	activeMax    int
	log          []RequestLog
	conns        map[net.Conn]struct{}
	outageErr    error
}

// New starts a fault server on a loopback port and registers its shutdown with t.Cleanup.
func New(t testing.TB, opts Options) *Server {
	t.Helper()
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("faultserver: listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		ln:     newOutageListener(inner),
		start:  time.Now(),
		cancel: cancel,
		stop:   make(chan struct{}),
		global: rate.NewLimiter(rate.Inf, 0),
		opts:   cloneOptions(opts),
		conns:  make(map[net.Conn]struct{}),
	}
	s.syncGlobalLimiter()

	hs := httptest.NewUnstartedServer(http.HandlerFunc(s.handle))
	_ = hs.Listener.Close()
	hs.Listener = s.ln
	hs.Config.BaseContext = func(net.Listener) context.Context { return ctx }
	hs.Config.ConnState = s.trackConn
	hs.Start()
	s.hs = hs
	s.URL = hs.URL

	if len(opts.Outages) > 0 {
		s.scheduler.Add(1)
		go s.runOutages(slices.Clone(opts.Outages))
	}
	t.Cleanup(func() {
		if err := s.close(); err != nil {
			t.Errorf("faultserver: %v", err)
		}
	})
	return s
}

// SetOptions mutates the options; it affects requests that arrive afterwards.
// f runs while the server lock is held, so it must not call other Server
// methods (that would deadlock).
func (s *Server) SetOptions(f func(*Options)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(&s.opts)
	s.opts = cloneOptions(s.opts)
	s.syncGlobalLimiter()
}

// Requests returns a copy of the request log in arrival order.
func (s *Server) Requests() []RequestLog {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.log)
}

// ActiveMax returns the peak number of concurrently in-flight requests,
// including those rejected by MaxConcurrent.
func (s *Server) ActiveMax() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeMax
}

// SetDNSFailing makes dialers returned by Dialer fail name resolution.
func (s *Server) SetDNSFailing(failing bool) { s.dnsFailing.Store(failing) }

// Dialer returns a dial function for http.Transport.DialContext that fails
// with a *net.DNSError while SetDNSFailing(true) is in effect.
func (s *Server) Dialer() func(ctx context.Context, network, addr string) (net.Conn, error) {
	var d net.Dialer
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if s.dnsFailing.Load() {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				host = addr
			}
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		}
		return d.DialContext(ctx, network, addr)
	}
}

func (s *Server) close() error {
	s.cancel() // unblocks blackholed, throttled and sleeping handlers
	// httptest.Server.Close waits for active handlers; one blocked writing to a
	// client that stopped reading only returns once its connection is closed.
	s.closeConns()
	close(s.stop)
	s.scheduler.Wait()
	s.hs.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.outageErr
}

func cloneOptions(o Options) Options {
	o.CorruptAt = slices.Clone(o.CorruptAt)
	o.Outages = slices.Clone(o.Outages)
	return o
}

func writePiece(o *Options) int {
	if o.WritePiece > 0 {
		return o.WritePiece
	}
	return defaultWritePiece
}

// syncGlobalLimiter applies the global bandwidth option; s.mu must be held.
func (s *Server) syncGlobalLimiter() {
	if s.opts.BytesPerSecondGlobal <= 0 {
		s.global.SetLimit(rate.Inf)
		return
	}
	s.global.SetLimit(rate.Limit(s.opts.BytesPerSecondGlobal))
	s.global.SetBurst(writePiece(&s.opts))
}

func (s *Server) trackConn(c net.Conn, state http.ConnState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch state {
	case http.StateNew:
		s.conns[c] = struct{}{}
	case http.StateClosed, http.StateHijacked:
		delete(s.conns, c)
	}
}

func (s *Server) writeHeader(w http.ResponseWriter, idx, status int) {
	s.mu.Lock()
	s.log[idx].Status = status
	s.mu.Unlock()
	w.WriteHeader(status)
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests++
	n := s.requests
	s.active++
	active := s.active
	s.activeMax = max(s.activeMax, active)
	idx := len(s.log)
	s.log = append(s.log, RequestLog{
		Method: r.Method, Range: r.Header.Get("Range"), IfRange: r.Header.Get("If-Range"), Path: r.URL.Path,
	})
	o := cloneOptions(s.opts)
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.active--
		s.mu.Unlock()
	}()

	switch {
	case o.ExpireAfter > 0 && time.Since(s.start) >= o.ExpireAfter:
		s.writeHeader(w, idx, http.StatusForbidden)
	case o.RequireAuth != "" && r.Header.Get("Authorization") != o.RequireAuth:
		w.Header().Set("WWW-Authenticate", `Bearer realm="faultserver"`)
		s.writeHeader(w, idx, http.StatusUnauthorized)
	case o.StatusOverride != 0:
		setRetryAfter(w, o.RetryAfter)
		s.writeHeader(w, idx, o.StatusOverride)
	case n <= o.FailFirstN:
		setRetryAfter(w, o.RetryAfter)
		s.writeHeader(w, idx, cmp.Or(o.FailStatus, http.StatusServiceUnavailable))
	case o.MaxConcurrent > 0 && active > o.MaxConcurrent:
		setRetryAfter(w, cmp.Or(o.RetryAfter, time.Second))
		s.writeHeader(w, idx, http.StatusServiceUnavailable)
	case r.Method != http.MethodGet && r.Method != http.MethodHead:
		s.writeHeader(w, idx, http.StatusMethodNotAllowed)
	case r.URL.Path == "/file":
		s.serveFile(w, r, idx, &o)
	case r.URL.Path == "/redirect" || strings.HasPrefix(r.URL.Path, "/redirect/"):
		s.serveRedirect(w, r, idx, &o)
	default:
		s.writeHeader(w, idx, http.StatusNotFound)
	}
}

func setRetryAfter(w http.ResponseWriter, d time.Duration) {
	if d > 0 {
		w.Header().Set("Retry-After", strconv.FormatInt(int64(math.Ceil(d.Seconds())), 10))
	}
}

func (s *Server) serveRedirect(w http.ResponseWriter, r *http.Request, idx int, o *Options) {
	hops := o.RedirectChain
	if rest, ok := strings.CutPrefix(r.URL.Path, "/redirect/"); ok {
		n, err := strconv.Atoi(rest)
		if err != nil {
			hops = 0
		} else {
			hops = n
		}
	}
	if hops < 1 {
		s.writeHeader(w, idx, http.StatusNotFound)
		return
	}
	q := ""
	if r.URL.RawQuery != "" {
		q = "?" + r.URL.RawQuery
	}
	loc := "/file" + q
	switch {
	case hops > 1:
		loc = fmt.Sprintf("/redirect/%d%s", hops-1, q)
	case o.RedirectTo != "":
		loc = o.RedirectTo
	}
	w.Header().Set("Location", loc)
	s.writeHeader(w, idx, http.StatusFound)
}

func (s *Server) serveFile(w http.ResponseWriter, r *http.Request, idx int, o *Options) {
	s.mu.Lock()
	s.fileRequests++
	fn := s.fileRequests
	s.mu.Unlock()

	seed, etag, lm := o.Seed, o.ETag, o.LastModified
	if o.ChangeETagAfter > 0 && fn > o.ChangeETagAfter {
		seed = ChangedSeed(seed)
		if etag != "" {
			etag += "-changed"
		}
		if !lm.IsZero() {
			lm = lm.Add(time.Hour)
		}
	}
	h := w.Header()
	etagHdr := ""
	if o.NoValidators {
		lm = time.Time{}
	} else {
		if etag != "" {
			etagHdr = `"` + etag + `"`
			if o.WeakETag {
				etagHdr = "W/" + etagHdr
			}
			h.Set("ETag", etagHdr)
		}
		if !lm.IsZero() {
			h.Set("Last-Modified", lm.UTC().Format(http.TimeFormat))
		}
	}
	if !o.IgnoreRanges {
		h.Set("Accept-Ranges", "bytes")
	}
	h.Set("Content-Type", "application/octet-stream")
	if o.ContentDisposition != "" {
		h.Set("Content-Disposition", o.ContentDisposition)
	}

	start, end, status := int64(0), o.Size, http.StatusOK
	rangesOn := !o.IgnoreRanges && (o.RangeIgnoredAfter <= 0 || fn <= o.RangeIgnoredAfter)
	if rh := r.Header.Get("Range"); rh != "" && rangesOn && ifRangeMatches(r.Header.Get("If-Range"), etagHdr, o.WeakETag, lm) {
		if rs, re, ok, satisfiable := parseRange(rh, o.Size); ok {
			if !satisfiable {
				if !o.UnknownLength {
					h.Set("Content-Range", fmt.Sprintf("bytes */%d", o.Size))
				}
				s.writeHeader(w, idx, http.StatusRequestedRangeNotSatisfiable)
				return
			}
			start, end, status = rs, re, http.StatusPartialContent
			total := strconv.FormatInt(o.Size, 10)
			switch {
			case o.UnknownLength:
				total = "*"
			case o.WrongTotal:
				total = strconv.FormatInt(o.Size+1, 10)
			}
			cr := fmt.Sprintf("bytes %d-%d/%s", start, end-1, total)
			if o.MalformedContentRange {
				cr = fmt.Sprintf("bytes %d-%d", start, end-1)
			}
			h.Set("Content-Range", cr)
		}
	}

	declared := end - start + o.LongBodyBy
	if !o.UnknownLength {
		h.Set("Content-Length", strconv.FormatInt(declared, 10))
	}
	s.writeHeader(w, idx, status)
	if r.Method == http.MethodHead {
		return
	}
	s.writeBody(r.Context(), w, o, fn, seed, start, max(0, declared-o.ShortBodyBy))
}

// ifRangeMatches reports whether a ranged response may be sent given the
// request's If-Range value and the current validators. Entity tags use strong
// comparison; dates must equal Last-Modified to the second.
func ifRangeMatches(ifRange, etagHdr string, weak bool, lm time.Time) bool {
	if ifRange == "" {
		return true
	}
	if strings.HasPrefix(ifRange, `"`) || strings.HasPrefix(ifRange, "W/") {
		return etagHdr != "" && !weak && ifRange == etagHdr
	}
	t, err := http.ParseTime(ifRange)
	return err == nil && !lm.IsZero() && t.Equal(lm.Truncate(time.Second))
}

// parseRange parses a single-range "bytes=" header against size, returning the
// half-open interval. ok is false for syntax the server ignores (including
// multiple ranges); satisfiable is false when the range lies beyond size.
func parseRange(h string, size int64) (start, end int64, ok, satisfiable bool) {
	spec, found := strings.CutPrefix(h, "bytes=")
	if !found || strings.Contains(spec, ",") {
		return 0, 0, false, false
	}
	first, last, found := strings.Cut(strings.TrimSpace(spec), "-")
	if !found {
		return 0, 0, false, false
	}
	if first == "" {
		n, ok := parseDigits(last)
		if !ok {
			return 0, 0, false, false
		}
		if n == 0 || size == 0 {
			return 0, 0, true, false
		}
		return max(0, size-n), size, true, true
	}
	s, ok := parseDigits(first)
	if !ok {
		return 0, 0, false, false
	}
	e := int64(math.MaxInt64)
	if last != "" {
		if e, ok = parseDigits(last); !ok || e < s {
			return 0, 0, false, false
		}
	}
	if s >= size {
		return 0, 0, true, false
	}
	return s, min(e, size-1) + 1, true, true
}

func parseDigits(v string) (int64, bool) {
	if v == "" || strings.Trim(v, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.ParseInt(v, 10, 64)
	return n, err == nil
}

type bodyEnd int

const (
	endComplete bodyEnd = iota
	endReset
	endClose
	endBlackhole
)

// writeBody streams n generated bytes starting at resource offset off in
// pieces, pacing, corrupting and cutting the stream as the options dictate.
func (s *Server) writeBody(ctx context.Context, w http.ResponseWriter, o *Options, fn int, seed uint64, off, n int64) {
	rc := http.NewResponseController(w)
	if rc.Flush() != nil {
		return
	}
	stop, how := n, endComplete
	cut := func(enabled bool, at int64, kind bodyEnd) {
		if enabled && at < stop {
			stop, how = at, kind
		}
	}
	cut((o.ResetAfterBytes > 0 || o.ResetEveryNth > 0) && (o.ResetEveryNth <= 0 || fn%o.ResetEveryNth == 0), o.ResetAfterBytes, endReset)
	cut(o.CloseAfterBytes > 0, o.CloseAfterBytes, endClose)
	cut((o.BlackholeAfterBytes > 0 || o.BlackholeOnNth > 0) && (o.BlackholeOnNth <= 0 || fn == o.BlackholeOnNth), o.BlackholeAfterBytes, endBlackhole)

	// Drops and jitter come from a per-response RNG keyed by (Seed, request
	// number), so a seed reproduces the same per-request pattern regardless of
	// how concurrent requests are scheduled.
	rng := rand.New(rand.NewPCG(o.Seed, uint64(fn))) //nolint:gosec // deterministic fault injection; fn is positive
	piece := writePiece(o)
	buf := make([]byte, min(int64(piece), max(stop, 0)))
	var perConn *rate.Limiter
	if o.BytesPerSecondPerConn > 0 {
		perConn = rate.NewLimiter(rate.Limit(o.BytesPerSecondPerConn), piece)
	}
	if stop > 0 && o.WriteLatency > 0 && !sleep(ctx, o.WriteLatency) {
		return
	}
	for written := int64(0); written < stop; {
		if o.DropProbability > 0 && rng.Float64() < o.DropProbability {
			abort(rc, true)
			return
		}
		k := min(int64(piece), stop-written)
		if !s.pace(ctx, o, rng, perConn, int(k)) {
			return
		}
		chunk := buf[:k]
		fill(chunk, seed, off+written)
		for _, c := range o.CorruptAt {
			if c >= off+written && c < off+written+k {
				chunk[c-off-written] ^= 0xFF
			}
		}
		if _, err := w.Write(chunk); err != nil {
			return
		}
		if rc.Flush() != nil {
			return
		}
		written += k
	}
	switch how {
	case endReset:
		abort(rc, true)
	case endClose:
		abort(rc, false)
	case endBlackhole:
		<-ctx.Done()
	}
}

// pace applies per-piece jitter and bandwidth limits; it reports false when
// the request context ended.
func (s *Server) pace(ctx context.Context, o *Options, rng *rand.Rand, perConn *rate.Limiter, k int) bool {
	if o.JitterMax > 0 {
		if !sleep(ctx, time.Duration(rng.Int64N(int64(o.JitterMax)))) {
			return false
		}
	}
	if perConn != nil && waitN(ctx, perConn, k) != nil {
		return false
	}
	if o.BytesPerSecondGlobal > 0 && waitN(ctx, s.global, k) != nil {
		return false
	}
	return true
}

// waitN takes k tokens in burst-sized steps so k may exceed the burst.
func waitN(ctx context.Context, lim *rate.Limiter, k int) error {
	for k > 0 {
		step := min(k, max(lim.Burst(), 1))
		if err := lim.WaitN(ctx, step); err != nil {
			return err
		}
		k -= step
	}
	return nil
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// abort hijacks and closes the connection, with an RST when reset is true.
func abort(rc *http.ResponseController, reset bool) {
	conn, _, err := rc.Hijack()
	if err != nil {
		panic(http.ErrAbortHandler)
	}
	if tc, ok := conn.(*net.TCPConn); ok && reset {
		_ = tc.SetLinger(0)
	}
	_ = conn.Close()
}

func (s *Server) runOutages(outages []Outage) {
	defer s.scheduler.Done()
	slices.SortFunc(outages, func(a, b Outage) int { return cmp.Compare(a.After, b.After) })
	for _, o := range outages {
		if !s.sleepUntil(s.start.Add(o.After)) {
			return
		}
		s.ln.down()
		s.closeConns()
		if !s.sleepUntil(s.start.Add(o.After + o.Duration)) {
			return
		}
		if err := s.ln.up(s.stop); err != nil {
			s.mu.Lock()
			s.outageErr = fmt.Errorf("re-listen after outage: %w", err)
			s.mu.Unlock()
			return
		}
	}
}

func (s *Server) sleepUntil(at time.Time) bool {
	t := time.NewTimer(time.Until(at))
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-s.stop:
		return false
	}
}

// closeConns drops every open connection so an outage is total.
func (s *Server) closeConns() {
	s.mu.Lock()
	conns := make([]net.Conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}

// outageListener wraps a TCP listener that can be closed and re-opened on the
// same address while http.Server keeps calling Accept.
type outageListener struct {
	addr net.Addr
	done chan struct{}

	mu       sync.Mutex
	cur      net.Listener  // nil during an outage
	reopened chan struct{} // closed when cur is restored
	closed   bool
}

func newOutageListener(l net.Listener) *outageListener {
	return &outageListener{addr: l.Addr(), done: make(chan struct{}), cur: l}
}

func (l *outageListener) Accept() (net.Conn, error) {
	for {
		l.mu.Lock()
		cur, reopened, closed := l.cur, l.reopened, l.closed
		l.mu.Unlock()
		if closed {
			return nil, net.ErrClosed
		}
		if cur == nil {
			select {
			case <-reopened:
				continue
			case <-l.done:
				return nil, net.ErrClosed
			}
		}
		c, err := cur.Accept()
		if err == nil {
			return c, nil
		}
		l.mu.Lock()
		replaced, closed := l.cur != cur, l.closed
		l.mu.Unlock()
		if closed {
			return nil, net.ErrClosed
		}
		if !replaced {
			return nil, err
		}
	}
}

func (l *outageListener) down() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.cur == nil {
		return
	}
	_ = l.cur.Close()
	l.cur = nil
	l.reopened = make(chan struct{})
}

// up re-listens on the original address, giving up quietly if stop closes.
func (l *outageListener) up(stop <-chan struct{}) error {
	var (
		inner net.Listener
		err   error
	)
	for range 20 {
		if inner, err = net.Listen("tcp", l.addr.String()); err == nil {
			break
		}
		select {
		case <-time.After(50 * time.Millisecond):
		case <-stop:
			return nil
		case <-l.done:
			return nil
		}
	}
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.cur != nil {
		_ = inner.Close()
		return nil
	}
	l.cur = inner
	close(l.reopened)
	return nil
}

func (l *outageListener) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	close(l.done)
	if l.cur != nil {
		return l.cur.Close()
	}
	return nil
}

func (l *outageListener) Addr() net.Addr { return l.addr }
