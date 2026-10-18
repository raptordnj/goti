// This file implements the multipart executor: a supervisor running the
// adaptive concurrency controller over a set of segment workers, with endgame
// splitting and the fill-mode fallback (spec §6.2–§6.4, §8).

package downloader

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/raptordnj/goti/internal/domain"
	"github.com/raptordnj/goti/internal/ratelimit"
	"github.com/raptordnj/goti/internal/strategy"
	"github.com/raptordnj/goti/internal/transport"
)

const (
	// minSplitSize is MinSplitSize of spec §6.2.
	minSplitSize = 1 << 20
	// windowMinBytes is the minimum data per controller window (spec §8).
	windowMinBytes = 256 << 10
	// defaultMinWindow and maxWindow bound a controller window (spec §8).
	defaultMinWindow = 2 * time.Second
	maxWindow        = 10 * time.Second
	// rangeIgnoredLimit is the number of 200 answers to ranged requests that
	// switches the task to fill mode (spec §6.4).
	rangeIgnoredLimit = 3
	// idlePoll is how often an idle worker re-checks for work during the endgame.
	idlePoll = 50 * time.Millisecond
)

// errFillMode makes a worker abort the group so the supervisor can switch to
// fill mode. It never leaves runMultipart.
var errFillMode = errors.New("switch to fill mode")

// workerSlot publishes what one worker is downloading so TrySplit can
// estimate that segment's rate. All fields are atomics.
type workerSlot struct {
	inUse atomic.Bool             // set by the supervisor, cleared by the worker on exit
	seg   atomic.Pointer[Segment] // segment of the request in flight, or nil
	t0    atomic.Int64            // request start, unix nanoseconds
	p0    atomic.Int64            // segment Pos at request start
}

// multipart is the shared state of one runMultipart call.
type multipart struct {
	env   *runEnv // copy of the caller's env with accounting hooks installed
	slots []workerSlot

	backoffMu sync.Mutex // Backoff.Rand is not safe for concurrent use

	active    atomic.Int32 // running workers
	retire    atomic.Int32 // workers asked to exit at their next segment boundary
	notBefore atomic.Int64 // unix nanoseconds before which no request starts

	rangeIgnored atomic.Int32 // 200 answers to ranged requests (task-wide)

	// Window accumulators, reset by the supervisor after each sample.
	useful       atomic.Int64
	errs         atomic.Int32
	throttled    atomic.Int32
	retryAfter   atomic.Int64 // max Retry-After seen, nanoseconds
	timeouts     atomic.Int32
	writeBlocked atomic.Int64 // nanoseconds spent in WriteAt
}

// runMultipart downloads env.task with concurrent ranged requests driven by
// ctl, building env.sched from the decision when nil. It returns nil when all
// segments are done; a RemoteChanged error aborts the whole run at once. After
// rangeIgnoredLimit 200 answers to ranged requests it reports a strategy
// change through onStrategy (may be nil) and finishes in fill mode.
func runMultipart(ctx context.Context, env *runEnv, ctl *strategy.Controller, onStrategy func(from, to domain.Strategy, reason string)) error {
	if env.sched == nil {
		env.sched = NewScheduler(env.decision.Size, env.decision.ChunkSize)
	}
	if env.sched.AllDone() {
		return nil
	}
	m := &multipart{}
	m.slots = make([]workerSlot, max(env.decision.MaxConns, env.cfg.MaxConnections, ctl.Current(), 1))
	e := *env
	m.env = &e
	e.onBytes = func(n int64) {
		m.useful.Add(n)
		env.addBytes(n)
	}
	e.writeAt = func(p []byte, off int64) (int, error) {
		t0 := e.clock.Now()
		n, err := env.write(p, off)
		m.writeBlocked.Add(int64(e.clock.Now().Sub(t0)))
		return n, err
	}

	err := m.run(ctx, ctl)
	if !errors.Is(err, errFillMode) {
		return err
	}
	if c := stopCause(ctx); c != nil {
		return c
	}
	if onStrategy != nil {
		onStrategy(domain.StrategyMultipart, domain.StrategySingle, "server stopped honouring ranges; fill mode")
	}
	return runFill(ctx, m.env)
}

// run starts the workers and supervises them until they all exit.
func (m *multipart) run(ctx context.Context, ctl *strategy.Controller) error {
	g, gctx := errgroup.WithContext(ctx)
	spawn := func() bool {
		for i := range m.slots {
			if m.slots[i].inUse.CompareAndSwap(false, true) {
				m.active.Add(1)
				g.Go(func() error { return m.worker(gctx, i) })
				return true
			}
		}
		return false
	}
	for range ctl.Current() {
		spawn()
	}
	g.Go(func() error { return m.supervise(gctx, ctl, spawn) })
	err := g.Wait()
	if c := stopCause(ctx); c != nil {
		return c
	}
	if err == nil {
		if !m.env.sched.AllDone() {
			return domain.NewError(domain.KindIntegrity, "multipart run ended with segments missing", "", nil)
		}
		return nil
	}
	return err
}

// supervise closes a controller window whenever it is long and full enough,
// applies the controller's action, and returns once no worker is left.
func (m *multipart) supervise(ctx context.Context, ctl *strategy.Controller, spawn func() bool) error {
	env := m.env
	winMin := env.windowMin
	if winMin <= 0 {
		winMin = defaultMinWindow
	}
	tick := max(winMin/4, 10*time.Millisecond)
	start := env.clock.Now()
	for m.active.Load() > 0 {
		select {
		case <-ctx.Done():
			return nil
		case <-env.clock.After(tick):
		}
		el := env.clock.Now().Sub(start)
		if el < winMin || (m.useful.Load() < windowMinBytes && el < maxWindow) {
			continue
		}
		conns := int(m.active.Load())
		s := strategy.Sample{
			Window:         el,
			UsefulBytes:    m.useful.Swap(0),
			Conns:          conns,
			Errors:         int(m.errs.Swap(0)),
			Throttled:      int(m.throttled.Swap(0)),
			RetryAfter:     time.Duration(m.retryAfter.Swap(0)),
			TimeoutsResets: int(m.timeouts.Swap(0)),
		}
		if conns > 0 {
			s.WriteBlockedFrac = float64(m.writeBlocked.Swap(0)) / (float64(el) * float64(conns))
		}
		start = env.clock.Now()
		m.apply(ctl.Observe(s), spawn)
	}
	return nil
}

// apply carries out one controller action.
func (m *multipart) apply(a strategy.Action, spawn func() bool) {
	switch a.Kind {
	case strategy.Increase:
		if m.retire.Load() > 0 {
			m.retire.Add(-1)
			return
		}
		if m.env.conns != nil {
			// The worker takes its own slot per request; this only checks
			// that the global limit has room for one more connection.
			if !m.env.conns.TryAcquire() {
				return
			}
			m.env.conns.Release()
		}
		spawn()
	case strategy.Backoff:
		m.notBefore.Store(m.env.clock.Now().Add(a.Delay).UnixNano())
		m.retireTo(a.Target)
	case strategy.Decrease, strategy.SwitchConservative:
		m.retireTo(a.Target)
	case strategy.Hold:
	}
}

func (m *multipart) retireTo(target int) {
	t := int32(min(max(target, 1), len(m.slots))) //nolint:gosec // bounded by len(m.slots) <= 64
	m.retire.Store(max(m.active.Load()-t, 0))
}

// shouldRetire consumes one retirement request, never retiring the last worker.
func (m *multipart) shouldRetire() bool {
	for {
		r := m.retire.Load()
		if r <= 0 || m.active.Load() <= 1 {
			return false
		}
		if m.retire.CompareAndSwap(r, r-1) {
			return true
		}
	}
}

// rate estimates seg's bytes/s from the request its owner has in flight.
func (m *multipart) rate(seg *Segment) float64 {
	now := m.env.clock.Now().UnixNano()
	for i := range m.slots {
		sl := &m.slots[i]
		if sl.seg.Load() != seg {
			continue
		}
		el := time.Duration(now - sl.t0.Load()).Seconds()
		if el <= 0 {
			return 0
		}
		return float64(seg.Pos()-sl.p0.Load()) / el
	}
	return 0
}

// worker is the spec §6.3 loop: acquire (or split off) a segment, fetch it,
// retry failures under the budget, exit when nothing is left or retired.
func (m *multipart) worker(ctx context.Context, id int) error {
	env := m.env
	slot := &m.slots[id]
	defer func() {
		m.active.Add(-1)
		slot.inUse.Store(false)
	}()
	attempt := 0
	for {
		if c := stopCause(ctx); c != nil {
			return nil
		}
		if m.shouldRetire() {
			return nil
		}
		seg, ok := env.sched.Acquire(id)
		if !ok {
			seg, ok = env.sched.TrySplit(id, minSplitSize, bufSize, m.rate)
		}
		if !ok {
			if env.sched.AllDone() {
				return nil
			}
			if err := env.clock.Sleep(ctx, idlePoll); err != nil {
				return nil
			}
			continue
		}
		before := seg.Pos()
		err := m.fetch(ctx, slot, seg)
		if err == nil {
			if !env.sched.Complete(seg) {
				env.sched.Release(seg)
				return domain.NewError(domain.KindIntegrity, "segment incomplete after download", "", nil)
			}
			attempt = 0
			if env.budget != nil {
				env.budget.Progress()
			}
			continue
		}
		env.sched.Release(seg)
		if stopCause(ctx) != nil {
			return nil
		}
		if errors.Is(err, errFillMode) {
			return err
		}
		if seg.Pos()-before >= progressBytes {
			attempt = 0
			if env.budget != nil {
				env.budget.Progress()
			}
		}
		if err := m.retry(ctx, err, &attempt); err != nil {
			return err
		}
	}
}

// retry accounts a failed request and sleeps the backoff; a non-nil result
// aborts the run.
func (m *multipart) retry(ctx context.Context, err error, attempt *int) error {
	env := m.env
	kind := Classify(err)
	var de *domain.Error
	isDomain := errors.As(err, &de)
	m.errs.Add(1)
	var ra time.Duration
	switch {
	case isDomain && isThrottleStatus(de):
		m.throttled.Add(1)
		ra = de.RetryAfter
		for {
			cur := m.retryAfter.Load()
			if int64(ra) <= cur || m.retryAfter.CompareAndSwap(cur, int64(ra)) {
				break
			}
		}
	case !isDomain || errors.Is(err, errStalled):
		m.timeouts.Add(1) // resets, unexpected EOFs, stalls
	case isDomain:
		ra = de.RetryAfter
	}
	// A Content-Range interval mismatch is retried for that segment (§6.4).
	if kind == domain.KindIntegrity {
		kind = domain.KindTransient
	}
	if !Retryable(kind) || kind == domain.KindOffline {
		return err
	}
	if env.budget != nil && !env.budget.Spend() {
		return domain.NewError(kind, "retry budget exhausted: "+err.Error(), "retry later", err)
	}
	m.backoffMu.Lock()
	delay := env.backoff.Delay(*attempt, ra)
	m.backoffMu.Unlock()
	*attempt++
	if env.onRetry != nil {
		env.onRetry(domain.Attempt{At: env.clock.Now(), Kind: kind, Message: err.Error(), Delay: delay})
	}
	if serr := env.clock.Sleep(ctx, delay); serr != nil {
		return nil // the group is ending; its cause is reported by run
	}
	return nil
}

// isThrottleStatus reports whether e is a 429 or 503 answer (spec §8).
func isThrottleStatus(e *domain.Error) bool {
	return e.Msg == "server answered 429" || e.Msg == "server answered 503"
}

// fetch performs one ranged request for [Pos, End) of seg and streams it in.
func (m *multipart) fetch(ctx context.Context, slot *workerSlot, seg *Segment) error {
	env := m.env
	if nb := m.notBefore.Load(); nb > 0 {
		if d := time.Duration(nb - env.clock.Now().UnixNano()); d > 0 {
			if err := env.clock.Sleep(ctx, d); err != nil {
				return err
			}
		}
	}
	want := domain.ByteRange{Start: seg.Pos(), End: seg.End()}
	reqCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	// Always the original URL: FinalURL is redacted, display-only; the
	// client re-follows redirects.
	target := env.task.OriginalURL
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, target, nil)
	if err != nil {
		return domain.NewError(domain.KindPermanent, "invalid URL "+transport.Redact(target), "", nil)
	}
	req.Header.Set("Range", transport.RangeHeader(want))
	v := env.task.Validators
	if v.Kind() == domain.ValidatorStrongETag {
		req.Header.Set("If-Range", v.ETag)
	}
	if env.conns != nil {
		if err := env.conns.Acquire(ctx); err != nil {
			return err
		}
		defer env.conns.Release()
	}
	slot.t0.Store(env.clock.Now().UnixNano())
	slot.p0.Store(want.Start)
	slot.seg.Store(seg)
	defer slot.seg.Store(nil)
	resp, err := env.client.Do(req) //nolint:gosec // URL comes from the task, guarded by the client's address policy
	if err != nil {
		if errors.Is(context.Cause(reqCtx), errStalled) {
			return newStallError()
		}
		return transport.RedactError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusPartialContent:
	case http.StatusOK:
		if v.Kind() != domain.ValidatorNone && !v.Matches(transport.ReadValidators(resp.Header)) {
			return domain.NewError(domain.KindRemoteChanged, "resource validators changed on the server", "restart the download", nil)
		}
		if m.rangeIgnored.Add(1) >= rangeIgnoredLimit {
			return errFillMode
		}
		return domain.NewError(domain.KindTransient, "server ignored the Range header", "", nil)
	default:
		return httpStatusError(resp)
	}
	if err := transport.ValidateRangeResponse(resp, want, env.sched.Total(), v); err != nil {
		return err
	}
	err = copySegment(ctx, env, seg, &requestBody{Reader: resp.Body, cancel: cancel})
	if err != nil && errors.Is(context.Cause(reqCtx), errStalled) {
		return newStallError()
	}
	return err
}

// runFill implements fill mode (spec §6.4): one GET without Range whose body
// fills every missing byte of every non-done segment in stream order; bytes
// already written are discarded. Validators and length must match the task.
func runFill(ctx context.Context, env *runEnv) error {
	var segs []*Segment
	for {
		g, ok := env.sched.Acquire(0)
		if !ok {
			break
		}
		segs = append(segs, g)
	}
	idx := 0
	defer func() {
		for _, g := range segs[idx:] {
			env.sched.Release(g)
		}
	}()
	if len(segs) == 0 {
		return nil
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
	if env.conns != nil {
		if err := env.conns.Acquire(ctx); err != nil {
			return err
		}
		defer env.conns.Release()
	}
	resp, err := env.client.Do(req) //nolint:gosec // URL comes from the task, guarded by the client's address policy
	if err != nil {
		return transport.RedactError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return httpStatusError(resp)
	}
	v := env.task.Validators
	if v.Kind() != domain.ValidatorNone && !transport.ReadValidators(resp.Header).Matches(v) {
		return domain.NewError(domain.KindRemoteChanged, "resource validators changed on the server", "restart the download", nil)
	}
	if resp.ContentLength != env.sched.Total() {
		return domain.NewError(domain.KindRemoteChanged, "resource length changed on the server", "restart the download", nil)
	}

	bp, _ := env.bufPool.Get().(*[]byte)
	defer env.bufPool.Put(bp)
	buf := (*bp)[:bufSize]
	stall := env.cfg.StallTimeout
	if stall <= 0 {
		stall = defaultStallTimeout
	}
	var stalled atomic.Bool
	watchdog := time.AfterFunc(math.MaxInt64, func() {
		stalled.Store(true)
		cancel(errStalled)
	})
	defer watchdog.Stop()

	var off int64 // stream offset of buf[0]
	for idx < len(segs) {
		watchdog.Reset(stall)
		n, rerr := resp.Body.Read(buf)
		watchdog.Stop()
		if stalled.Load() {
			return newStallError()
		}
		if n > 0 {
			if err := ratelimit.WaitAll(ctx, n, env.limits...); err != nil {
				return err
			}
			if err := fillChunk(env, segs, &idx, buf[:n], off); err != nil {
				return err
			}
			off += int64(n)
		}
		if rerr != nil && idx < len(segs) {
			if errors.Is(rerr, io.EOF) {
				return io.ErrUnexpectedEOF
			}
			return transport.RedactError(rerr)
		}
	}
	return nil
}

// fillChunk writes the parts of chunk (stream bytes [off, off+len)) that
// continue segs[*idx:] at their Pos, completing segments as they fill.
func fillChunk(env *runEnv, segs []*Segment, idx *int, chunk []byte, off int64) error {
	end := off + int64(len(chunk))
	for *idx < len(segs) {
		g := segs[*idx]
		a := g.Pos()
		if a >= end {
			return nil // this segment's missing bytes come later in the stream
		}
		if a < off {
			return domain.NewError(domain.KindIntegrity, "fill mode stream passed a segment's position", "", nil)
		}
		p := chunk[a-off : min(g.End(), end)-off]
		w, err := env.write(p, a)
		if err == nil && w != len(p) {
			err = io.ErrShortWrite
		}
		if err != nil {
			return err
		}
		g.Write(p)
		env.addBytes(int64(len(p)))
		if g.Pos() < g.End() {
			return nil
		}
		if !env.sched.Complete(g) {
			return domain.NewError(domain.KindIntegrity, "segment incomplete after fill", "", nil)
		}
		*idx++
	}
	return nil
}
