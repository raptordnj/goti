package strategy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/raptordnj/goti/internal/config"
	"github.com/raptordnj/goti/internal/domain"
	"github.com/raptordnj/goti/internal/storage"
	"github.com/raptordnj/goti/internal/transport"
)

const (
	maxURLLen           = 8 << 10
	maxProbeAttempts    = 3
	defaultRetryAfter   = time.Second
	maxRetryAfter       = 10 * time.Minute
	fileNameDefault     = "download"
	sourceDisposition   = "content-disposition"
	sourceURL           = "url"
	sourceDefault       = "default"
	anomalyIgnoredRange = "server ignored Range (200)"
)

// Inspector probes a resource with bounded ranged GETs (spec §4.1).
type Inspector struct {
	Client *http.Client
	Cfg    config.Config
	Clock  domain.Clock
}

// probe is the outcome of one probe request after retries.
type probe struct {
	resp    *http.Response // closed; headers only
	body    int64          // bytes read (capped at want+1); -1 when not read
	final   *url.URL
	redirs  []string
	errText string
	errKind domain.ErrorKind // for transport errors (resp == nil or bodyErr set)
	// bodyErr is set when the headers arrived but the connection failed
	// while reading the probe body.
	bodyErr string
	// exhausted is the classified 429/503 status after the last retry; the
	// caller decides whether it fails the inspection (probe 1 only).
	exhausted *domain.Error
}

// Inspect runs spec §4.1 steps 1–7 against rawURL. known, when it holds a
// strong ETag (resume), is sent as If-Range on probe 2. The returned error is
// non-nil only for an invalid URL or ctx cancellation; HTTP-level failures
// land in Observations.Err.
func (in *Inspector) Inspect(ctx context.Context, rawURL string, known *domain.Validators) (Observations, error) {
	u, err := validateURL(rawURL)
	if err != nil {
		return Observations{}, err
	}
	obs := Observations{OriginalURL: transport.Redact(rawURL), SignedURL: transport.IsSignedURL(u)}

	// Step 2: probe 1.
	want1 := domain.ByteRange{Start: 0, End: 1}
	p1, err := in.probe(ctx, &obs, u.String(), want1, "", true)
	if err != nil {
		return obs, err
	}
	finalURL := u
	if p1.final != nil {
		finalURL = p1.final
	}
	obs.FinalURL = transport.Redact(finalURL.String())
	obs.Redirects = p1.redirs
	obs.SignedURL = obs.SignedURL || transport.IsSignedURL(finalURL)
	if p1.resp == nil {
		obs.Err = p1.exhausted
		if obs.Err == nil {
			obs.Err = domain.NewError(p1.errKind, "probe failed: "+p1.errText, "", nil)
		}
		return obs, nil
	}
	resp := p1.resp
	obs.Proto, obs.Status = resp.Proto, resp.StatusCode
	obs.ContentType = resp.Header.Get("Content-Type")
	obs.Validators = transport.ReadValidators(resp.Header)
	in.resolveFileName(&obs, resp, finalURL)

	candidate := false
	switch resp.StatusCode {
	case http.StatusPartialContent:
		if p1.bodyErr != "" {
			in.rangeProbeInterrupted(&obs, "probe 1", p1)
			if cr, err := transport.ParseContentRange(resp.Header.Get("Content-Range")); err == nil && !cr.Unsatisfied && cr.TotalKnown {
				obs.Size, obs.SizeKnown = cr.Total, true
			}
			break
		}
		candidate = in.checkProbe1(&obs, resp, p1.body, want1)
	case http.StatusOK:
		obs.Anomalies = append(obs.Anomalies, anomalyIgnoredRange)
		if resp.ContentLength >= 0 {
			obs.Size, obs.SizeKnown = resp.ContentLength, true
		}
	case http.StatusRequestedRangeNotSatisfiable:
		cr, err := transport.ParseContentRange(resp.Header.Get("Content-Range"))
		if err == nil && cr.Unsatisfied && cr.Total == 0 {
			obs.Size, obs.SizeKnown = 0, true
		} else {
			obs.Err = domain.NewError(domain.KindPermanent, "server rejected range 0-0 for a non-empty resource", "", nil)
		}
	default:
		obs.Err = statusError(resp)
	}

	// Step 3: probe 2.
	if candidate && obs.Size >= in.Cfg.MinMultipartSize {
		obs.RangesProven = in.probe2(ctx, &obs, finalURL.String(), known)
		if ctx.Err() != nil {
			return obs, ctx.Err()
		}
	}

	// Step 7: dynamic heuristics.
	chunked := slices.Contains(resp.TransferEncoding, "chunked") && resp.ContentLength < 0
	noStore := strings.Contains(strings.ToLower(resp.Header.Get("Cache-Control")), "no-store")
	obs.Dynamic = obs.Err == nil && (!obs.SizeKnown || chunked || (noStore && obs.Validators.Kind() == domain.ValidatorNone))
	return obs, nil
}

// checkProbe1 classifies a 206 answer to bytes=0-0 and reports whether it is
// a range candidate.
func (in *Inspector) checkProbe1(obs *Observations, resp *http.Response, body int64, want domain.ByteRange) bool {
	cr, err := transport.ParseContentRange(resp.Header.Get("Content-Range"))
	if err != nil || cr.Unsatisfied {
		obs.Anomalies = append(obs.Anomalies, "probe 1: malformed Content-Range")
		return false
	}
	ok := true
	if cr.TotalKnown {
		obs.Size, obs.SizeKnown = cr.Total, true
	} else {
		obs.Anomalies = append(obs.Anomalies, "probe 1: unknown total in Content-Range")
		ok = false
	}
	if cr.Range != want {
		obs.Anomalies = append(obs.Anomalies, "probe 1: Content-Range does not match the requested range")
		ok = false
	}
	if body != want.Len() {
		obs.Anomalies = append(obs.Anomalies, fmt.Sprintf("probe 1: body length %d, want %d", body, want.Len()))
		ok = false
	}
	return ok
}

// rangeProbeInterrupted records a range probe lost to a transport failure:
// range support is unknown, so the inspection fails retryably instead of
// settling for single-stream. The engine bounds the retries
// (Observations.RangeProbeInterrupted).
func (in *Inspector) rangeProbeInterrupted(obs *Observations, name string, p probe) {
	msg := name + " failed: " + p.bodyErr
	obs.Anomalies = append(obs.Anomalies, msg)
	obs.Err = domain.NewError(p.errKind, msg, "", nil)
	obs.RangeProbeInterrupted = true
}

// probe2 requests two bytes at floor(N/2) and reports whether ranges are proven.
func (in *Inspector) probe2(ctx context.Context, obs *Observations, target string, known *domain.Validators) bool {
	n := obs.Size
	want := domain.ByteRange{Start: n / 2, End: n/2 + 2}
	ifRange := ""
	switch {
	case known != nil && known.Kind() == domain.ValidatorStrongETag:
		ifRange = known.ETag
	case obs.Validators.Kind() == domain.ValidatorStrongETag:
		ifRange = obs.Validators.ETag
	}
	p2, err := in.probe(ctx, obs, target, want, ifRange, false)
	if err != nil || p2.resp == nil {
		if p2.errText != "" && (err != nil || p2.exhausted != nil) {
			obs.Anomalies = append(obs.Anomalies, "probe 2 failed: "+p2.errText)
		}
		if err == nil && p2.exhausted == nil && p2.errText != "" {
			p2.bodyErr = p2.errText
			in.rangeProbeInterrupted(obs, "probe 2", p2)
		}
		return false
	}
	if p2.bodyErr != "" {
		in.rangeProbeInterrupted(obs, "probe 2", p2)
		return false
	}
	resp := p2.resp
	v := transport.ReadValidators(resp.Header)
	if v.ETag != obs.Validators.ETag || v.LastModified != obs.Validators.LastModified {
		obs.Anomalies = append(obs.Anomalies, "validator changed between probes")
		return false
	}
	if resp.StatusCode != http.StatusPartialContent {
		obs.Anomalies = append(obs.Anomalies, "probe 2: status "+strconv.Itoa(resp.StatusCode)+", want 206")
		return false
	}
	cr, err := transport.ParseContentRange(resp.Header.Get("Content-Range"))
	switch {
	case err != nil || cr.Unsatisfied || cr.Range != want:
		obs.Anomalies = append(obs.Anomalies, "probe 2: Content-Range does not match the requested range")
	case !cr.TotalKnown || cr.Total != n:
		obs.Anomalies = append(obs.Anomalies, "probe 2: Content-Range total differs from probe 1")
	case p2.body != want.Len():
		obs.Anomalies = append(obs.Anomalies, fmt.Sprintf("probe 2: body length %d, want %d", p2.body, want.Len()))
	default:
		return true
	}
	return false
}

// probe performs one ranged GET with retries on 429/503, appending a
// ProbeResult per attempt. It never sets obs.Err. The error is non-nil only when ctx is done.
func (in *Inspector) probe(ctx context.Context, obs *Observations, target string, want domain.ByteRange, ifRange string, trackRedirects bool) (probe, error) {
	for attempt := 1; ; attempt++ {
		p, retryAfter := in.attempt(ctx, obs, target, want, ifRange, trackRedirects)
		if err := ctx.Err(); err != nil {
			return p, err
		}
		if p.resp == nil || retryAfter < 0 || attempt == maxProbeAttempts {
			if p.resp != nil && retryAfter >= 0 {
				e := statusError(p.resp)
				e.RetryAfter = retryAfter
				p.exhausted = e
				p.resp = nil
				p.errText = e.Msg
			}
			return p, nil
		}
		obs.ProbeRetries++
		if err := in.Clock.Sleep(ctx, retryAfter); err != nil {
			return p, err
		}
	}
}

// attempt sends one probe request. retryAfter is >= 0 when the response was
// 429/503 (retryable), else -1.
func (in *Inspector) attempt(ctx context.Context, obs *Observations, target string, want domain.ByteRange, ifRange string, trackRedirects bool) (probe, time.Duration) {
	var p probe
	rctx, cancel := context.WithTimeout(ctx, in.Cfg.ProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, target, nil)
	if err != nil {
		p.errText = "invalid request"
		return p, -1
	}
	req.Header.Set("Range", transport.RangeHeader(want))
	if ifRange != "" {
		req.Header.Set("If-Range", ifRange)
	}
	client := *in.Client
	if trackRedirects {
		check := in.Client.CheckRedirect
		client.CheckRedirect = func(r *http.Request, via []*http.Request) error {
			if check != nil {
				if err := check(r, via); err != nil {
					return err
				}
			}
			p.redirs = append(p.redirs, transport.Redact(r.URL.String()))
			return nil
		}
	}
	pr := ProbeResult{Requested: want}
	start := in.Clock.Now()
	resp, err := client.Do(req)
	pr.RTT = in.Clock.Now().Sub(start)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			obs.ProbeTimeouts++
		}
		p.errText = transport.RedactError(err).Error()
		p.errKind = transportErrorKind(err)
		pr.Err = p.errText
		obs.Probes = append(obs.Probes, pr)
		return p, -1
	}
	p.final = resp.Request.URL
	p.body = -1
	if resp.StatusCode == http.StatusPartialContent {
		p.body, err = io.Copy(io.Discard, io.LimitReader(resp.Body, want.Len()+1))
	}
	_ = resp.Body.Close()
	if err != nil {
		// The connection failed mid-body: a network failure, not evidence
		// about range support (spec §8.1).
		p.bodyErr = transport.RedactError(err).Error()
		p.errKind = transportErrorKind(err)
		pr.Err = p.bodyErr
	}
	p.resp = resp
	pr.Status = resp.StatusCode
	pr.ContentRange = resp.Header.Get("Content-Range")
	pr.BodyLen = p.body
	pr.Validators = transport.ReadValidators(resp.Header)
	obs.Probes = append(obs.Probes, pr)
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
		return p, min(parseRetryAfter(resp.Header.Get("Retry-After"), in.Clock.Now()), maxRetryAfter)
	}
	return p, -1
}

// resolveFileName implements spec §4.1 step 6.
func (in *Inspector) resolveFileName(obs *Observations, resp *http.Response, final *url.URL) {
	if name, ok := transport.ParseContentDisposition(resp.Header.Get("Content-Disposition")); ok {
		obs.FileName, obs.FileNameSource = storage.SanitizeFilename(name), sourceDisposition
		return
	}
	if seg := path.Base(final.Path); seg != "/" && seg != "." && seg != "" {
		obs.FileName, obs.FileNameSource = storage.SanitizeFilename(seg), sourceURL
		return
	}
	obs.FileName, obs.FileNameSource = fileNameDefault, sourceDefault
}

// validateURL implements spec §4.1 step 1.
func validateURL(raw string) (*url.URL, error) {
	if len(raw) > maxURLLen {
		return nil, domain.NewError(domain.KindPermanent, "URL longer than 8 KiB", "", nil)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, domain.NewError(domain.KindPermanent, "malformed URL", "", nil)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, domain.NewError(domain.KindPermanent, "URL scheme must be http or https", "", nil)
	}
	if u.Host == "" || u.Hostname() == "" {
		return nil, domain.NewError(domain.KindPermanent, "URL has no host", "", nil)
	}
	return u, nil
}

// statusError classifies a non-success probe status.
func statusError(resp *http.Response) *domain.Error {
	code := resp.StatusCode
	msg := "server answered " + strconv.Itoa(code)
	switch {
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return domain.NewError(domain.KindAuth, msg, "check credentials or refresh the URL", nil)
	case code == http.StatusTooManyRequests || code >= 500:
		return domain.NewError(domain.KindTransient, msg, "", nil)
	default:
		return domain.NewError(domain.KindPermanent, msg, "", nil)
	}
}

// parseRetryAfter parses delta-seconds or an HTTP date, defaulting to 1 s.
func parseRetryAfter(v string, now time.Time) time.Duration {
	if v == "" {
		return defaultRetryAfter
	}
	if s, err := strconv.Atoi(v); err == nil && s >= 0 {
		return time.Duration(s) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(t.Sub(now), 0)
	}
	return defaultRetryAfter
}

// transportErrorKind classifies a failed request: network-unreachable
// failures are KindOffline, errors already classified by the client (address
// and redirect policy) keep their kind, everything else is KindTransient.
func transportErrorKind(err error) domain.ErrorKind {
	var de *domain.Error
	if errors.As(err, &de) {
		return de.Kind
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) ||
		errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENETUNREACH) || errors.Is(err, syscall.EHOSTUNREACH) {
		return domain.KindOffline
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" && opErr.Timeout() {
		return domain.KindOffline
	}
	return domain.KindTransient
}
