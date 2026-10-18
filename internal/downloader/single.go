// This file implements the single-stream executor (spec §7).

package downloader

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/raptordnj/goti/internal/domain"
	"github.com/raptordnj/goti/internal/transport"
)

// runSingle downloads the task over one connection into env.file. It builds
// env.sched when nil (one segment for a known size, an open segment
// otherwise). Transient errors are retried with backoff under env.budget,
// keeping Pos; pause/cancel causes, Offline and non-retryable errors are
// returned to the caller, as is any case needing a restart from 0.
func runSingle(ctx context.Context, env *runEnv) error {
	if env.sched == nil {
		if env.decision.SizeKnown {
			env.sched = NewScheduler(env.decision.Size, env.decision.Size)
		} else {
			env.sched = NewOpenScheduler()
		}
	}
	seg, ok := env.sched.Acquire(0)
	if !ok {
		return nil // nothing left (empty file or already complete)
	}
	done := false
	defer func() {
		if !done {
			env.sched.Release(seg)
		}
	}()

	lastProgress := seg.Pos()
	for attempt := 0; ; {
		err := fetchSingle(ctx, env, seg)
		if errors.Is(err, io.EOF) && seg.End() == math.MaxInt64 {
			if ferr := env.sched.Finish(seg.Pos()); ferr != nil {
				return ferr
			}
			err = nil
		}
		if err == nil {
			if !env.sched.Complete(seg) {
				return domain.NewError(domain.KindIntegrity, "segment incomplete after download", "", nil)
			}
			done = true
			return nil
		}
		if c := stopCause(ctx); c != nil {
			return c
		}
		if seg.Pos()-lastProgress >= progressBytes {
			lastProgress = seg.Pos()
			attempt = 0
			if env.budget != nil {
				env.budget.Progress()
			}
		}
		kind := Classify(err)
		if !Retryable(kind) || kind == domain.KindOffline {
			return err
		}
		if env.budget != nil && !env.budget.Spend() {
			return domain.NewError(kind, "retry budget exhausted: "+err.Error(), "retry later", err)
		}
		var retryAfter time.Duration
		var de *domain.Error
		if errors.As(err, &de) {
			retryAfter = de.RetryAfter
		}
		delay := env.backoff.Delay(attempt, retryAfter)
		attempt++
		if env.onRetry != nil {
			env.onRetry(domain.Attempt{At: env.clock.Now(), Kind: kind, Message: err.Error(), Delay: delay})
		}
		if serr := env.clock.Sleep(ctx, delay); serr != nil {
			if c := stopCause(ctx); c != nil {
				return c
			}
			return serr
		}
	}
}

// fetchSingle performs one request for [Pos, End) of seg and streams it in.
// All returned error messages are redacted.
func fetchSingle(ctx context.Context, env *runEnv, seg *Segment) error {
	pos := seg.Pos()
	known := seg.End() != math.MaxInt64
	if pos > 0 && !env.decision.RangesProven {
		return domain.NewError(domain.KindRemoteChanged, "cannot resume: server range support not proven", "restart the download", nil)
	}
	reqCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	// Always the original URL: FinalURL is redacted, display-only; the
	// client re-follows redirects.
	target := env.task.OriginalURL
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, target, nil)
	if err != nil {
		return domain.NewError(domain.KindPermanent, "invalid URL "+transport.Redact(target), "", nil)
	}
	v := env.task.Validators
	if pos > 0 {
		req.Header.Set("Range", transport.OpenRangeHeader(pos))
		switch v.Kind() {
		case domain.ValidatorStrongETag:
			req.Header.Set("If-Range", v.ETag)
		case domain.ValidatorLastModified:
			req.Header.Set("If-Range", v.LastModified)
		}
	}
	if env.conns != nil {
		if err := env.conns.Acquire(ctx); err != nil {
			return err
		}
		defer env.conns.Release()
	}
	resp, err := env.client.Do(req) //nolint:gosec // URL comes from the task, guarded by the client's address policy
	if err != nil {
		if errors.Is(context.Cause(reqCtx), errStalled) {
			return newStallError()
		}
		return transport.RedactError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if err := checkSingleResponse(env, resp, pos, known); err != nil {
		return err
	}
	return copySegment(ctx, env, seg, &requestBody{Reader: resp.Body, cancel: cancel})
}

// checkSingleResponse validates the status, range and validators of resp.
func checkSingleResponse(env *runEnv, resp *http.Response, pos int64, known bool) error {
	v := env.task.Validators
	if pos == 0 {
		if resp.StatusCode != http.StatusOK {
			return httpStatusError(resp)
		}
		if known && resp.ContentLength >= 0 && resp.ContentLength != env.decision.Size {
			return domain.NewError(domain.KindRemoteChanged, "resource length changed on the server", "restart the download", nil)
		}
		if v.Kind() != domain.ValidatorNone && !v.Matches(transport.ReadValidators(resp.Header)) {
			return domain.NewError(domain.KindRemoteChanged, "resource validators changed on the server", "restart the download", nil)
		}
		return nil
	}
	switch resp.StatusCode {
	case http.StatusPartialContent:
	case http.StatusOK:
		return domain.NewError(domain.KindRemoteChanged, "server answered 200 to a resume request", "restart the download", nil)
	default:
		return httpStatusError(resp)
	}
	total, want := int64(-1), domain.ByteRange{Start: pos}
	if known {
		total = env.decision.Size
		want.End = total
	} else {
		cr, err := transport.ParseContentRange(resp.Header.Get("Content-Range"))
		if err != nil {
			return domain.NewError(domain.KindIntegrity, "206 response has missing or malformed Content-Range", "", err)
		}
		want.End = cr.Range.End
	}
	return transport.ValidateRangeResponse(resp, want, total, v)
}

// httpStatusError classifies an unexpected status: 401/403 Auth, 429/5xx
// Transient (with Retry-After), anything else Permanent.
func httpStatusError(resp *http.Response) error {
	code := resp.StatusCode
	msg := "server answered " + strconv.Itoa(code)
	switch {
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return domain.NewError(domain.KindAuth, msg, "check credentials or refresh the URL", nil)
	case code == http.StatusTooManyRequests || code >= 500:
		e := domain.NewError(domain.KindTransient, msg, "", nil)
		e.RetryAfter = retryAfter(resp.Header.Get("Retry-After"))
		return e
	default:
		return domain.NewError(domain.KindPermanent, msg, "", nil)
	}
}

// retryAfter parses delta-seconds or an HTTP date; 0 when absent or invalid.
func retryAfter(h string) time.Duration {
	if h == "" {
		return 0
	}
	if s, err := strconv.ParseInt(h, 10, 64); err == nil && s >= 0 {
		return min(time.Duration(min(s, int64(maxRetryAfter/time.Second)))*time.Second, maxRetryAfter)
	}
	if t, err := http.ParseTime(h); err == nil {
		return min(max(time.Until(t), 0), maxRetryAfter)
	}
	return 0
}
