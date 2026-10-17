package downloader

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"math/rand/v2"
	"net"
	"sync"
	"syscall"
	"time"

	"github.com/raptordnj/swiftfetch/internal/domain"
)

const maxRetryAfter = 10 * time.Minute

// Backoff computes jittered exponential retry delays.
type Backoff struct {
	Base, Max time.Duration
	Factor    float64
	Rand      *rand.Rand // seeded in tests; nil uses the global generator
}

func (b Backoff) int64n(n int64) int64 {
	if n <= 0 {
		return 0
	}
	if b.Rand != nil {
		return b.Rand.Int64N(n)
	}
	return rand.Int64N(n) //nolint:gosec // jitter, not security
}

// Delay returns the wait before retry number attempt (0-based). A positive
// retryAfter overrides the schedule: it is capped at 10 minutes and gets up to
// 10% extra jitter. Otherwise full jitter over [0, min(Max, Base*Factor^attempt)].
func (b Backoff) Delay(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		d := min(retryAfter, maxRetryAfter)
		return d + time.Duration(b.int64n(int64(d)/10+1))
	}
	ceil := float64(b.Base)
	for i := 0; i < attempt && ceil < float64(b.Max); i++ {
		ceil *= b.Factor
	}
	ceil = min(ceil, float64(b.Max))
	return time.Duration(b.int64n(int64(ceil) + 1))
}

// Classify maps err to an ErrorKind. Explicit *domain.Error values and context
// cancellation win; otherwise syscall, net and x509 causes are inspected
// through any wrapping. Unknown errors are transient.
func Classify(err error) domain.ErrorKind {
	var de *domain.Error
	if errors.As(err, &de) {
		return de.Kind
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, domain.ErrCancelled) {
		return domain.KindCancelled
	}
	for _, e := range []syscall.Errno{syscall.ENOSPC, syscall.EACCES, syscall.EROFS} {
		if errors.Is(err, e) {
			return domain.KindLocal
		}
	}
	var (
		unknownAuth x509.UnknownAuthorityError
		hostname    x509.HostnameError
		invalid     x509.CertificateInvalidError
		verify      *tls.CertificateVerificationError
	)
	if errors.As(err, &unknownAuth) || errors.As(err, &hostname) ||
		errors.As(err, &invalid) || errors.As(err, &verify) {
		return domain.KindPermanent
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return domain.KindOffline
	}
	for _, e := range []syscall.Errno{syscall.ENETUNREACH, syscall.EHOSTUNREACH, syscall.ECONNREFUSED} {
		if errors.Is(err, e) {
			return domain.KindOffline
		}
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" && op.Timeout() {
		return domain.KindOffline
	}
	// ECONNRESET, io.ErrUnexpectedEOF, net timeouts and anything unknown.
	return domain.KindTransient
}

// Retryable reports whether failures of kind k may be retried.
func Retryable(k domain.ErrorKind) bool {
	return k == domain.KindTransient || k == domain.KindOffline
}

// Budget is a progress-aware retry budget. Callers Spend on each non-offline
// failure and call Progress after new verified bytes or a completed segment.
type Budget struct {
	mu   sync.Mutex
	full int
	left int
}

// NewBudget returns a budget allowing n retries without progress.
func NewBudget(n int) *Budget { return &Budget{full: n, left: n} }

// Spend consumes one retry, reporting false when none remain.
func (b *Budget) Spend() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.left <= 0 {
		return false
	}
	b.left--
	return true
}

// Progress restores the budget to full.
func (b *Budget) Progress() {
	b.mu.Lock()
	b.left = b.full
	b.mu.Unlock()
}

// OfflineWaiter polls connectivity while the network is down.
type OfflineWaiter struct {
	Clock domain.Clock
	Limit time.Duration // 0 waits forever
	Check func(ctx context.Context) error
}

const (
	offlineStart = 5 * time.Second
	offlineMax   = 60 * time.Second
)

// Wait runs Check until it succeeds (nil), ctx ends (ctx error), or Limit of
// continuous failure elapses (KindOffline). onCheck, if non-nil, is called after
// each failed check with the attempt number and the delay before the next one.
func (w *OfflineWaiter) Wait(ctx context.Context, onCheck func(attempt int, next time.Duration)) error {
	start := w.Clock.Now()
	next := offlineStart
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		cerr := w.Check(ctx)
		if cerr == nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if w.Limit > 0 && w.Clock.Now().Sub(start) >= w.Limit {
			return domain.NewError(domain.KindOffline, "network unavailable",
				"network unavailable — resume when connected", cerr)
		}
		if onCheck != nil {
			onCheck(attempt, next)
		}
		if err := w.Clock.Sleep(ctx, next); err != nil {
			return err
		}
		next = min(next*2, offlineMax)
	}
}
