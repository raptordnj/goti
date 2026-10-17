// This file implements one Engine.Run: the task lifecycle of spec §3 —
// inspection and revalidation, temp file recovery, execution, retry and
// offline waiting (§7, §8.1), and verification/finalization (§10).

package downloader

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync/atomic"
	"time"

	"github.com/raptordnj/swiftfetch/internal/config"
	"github.com/raptordnj/swiftfetch/internal/domain"
	"github.com/raptordnj/swiftfetch/internal/events"
	"github.com/raptordnj/swiftfetch/internal/ratelimit"
	"github.com/raptordnj/swiftfetch/internal/storage"
	"github.com/raptordnj/swiftfetch/internal/strategy"
	"github.com/raptordnj/swiftfetch/internal/transport"
)

const (
	reasonWaitingForNetwork = "waiting for network"
	verifiedSHA256          = "sha256 verified"
	verifiedSegments        = "segment-consistency verified; no trusted checksum supplied"
	expiredAction           = "the signed URL has probably expired; obtain a fresh link and add the download again"
	// offlineCheckTimeout bounds one connectivity check dial.
	offlineCheckTimeout = 10 * time.Second
	// etaMinElapsed is how long speed must be measured before an ETA is shown.
	etaMinElapsed = 3 * time.Second
	// speedAlpha is the EWMA weight of the newest speed sample.
	speedAlpha = 0.3
)

// taskRun is the state of one Run. Only the Run goroutine touches t.
type taskRun struct {
	e   *Engine
	t   *domain.Task
	cfg config.Config
	log *slog.Logger

	client   *http.Client
	budget   *Budget // task-level retries (TaskRetryBudget)
	backoff  Backoff
	attempt  int // task-level retry count, reset on progress
	decision strategy.Decision

	// prevRangesProven is whether the decision that produced the stored
	// segments had proven ranges (their progress is resumable).
	prevRangesProven bool
	// probeInterrupts counts consecutive inspections whose only failure was
	// a transport loss on a range probe (Observations.RangeProbeInterrupted).
	probeInterrupts int

	ckDownloaded atomic.Int64 // Downloaded as last checkpointed (T17)
	live         atomic.Int64 // bytes written in the current run, for progress
	written      atomic.Int64 // bytes written across all passes of this Run
	failBytes    int64        // written at the previous task-level failure
	retries      atomic.Int32 // attempts reported, for RetryScheduled.Attempt
}

const (
	// maxRangeProbeInterrupts is the consecutive range-probe interruptions
	// after which the inspection falls back to "ranges not proven" (R31).
	maxRangeProbeInterrupts = 3
	reasonProbesInterrupted = "range probes repeatedly interrupted; falling back to single stream"
)

func newTaskRun(e *Engine, t *domain.Task) *taskRun {
	var s taskSettings
	_ = json.Unmarshal(t.SettingsJSON, &s)
	cfg := s.apply(e.d.Config)
	r := &taskRun{
		e: e, t: t, cfg: cfg,
		log:     e.d.Logger.With("task", t.ID),
		budget:  NewBudget(cfg.TaskRetryBudget),
		backoff: Backoff{Base: cfg.RetryBaseDelay, Max: cfg.RetryMaxDelay, Factor: cfg.RetryFactor},
	}
	var prev struct {
		RangesProven bool `json:"ranges_proven"`
	}
	_ = json.Unmarshal(t.DecisionJSON, &prev)
	r.prevRangesProven = prev.RangesProven
	r.ckDownloaded.Store(t.Downloaded)
	return r
}

// ckStore records the downloaded total of every saved checkpoint so later
// UpdateTask calls never write back stale progress.
type ckStore struct {
	Store
	r *taskRun
}

func (s ckStore) SaveCheckpoint(ctx context.Context, id string, cp domain.Checkpoint) error {
	if err := s.Store.SaveCheckpoint(ctx, id, cp); err != nil {
		return err
	}
	s.r.ckDownloaded.Store(cp.Downloaded)
	return nil
}

// setState applies, persists, logs and publishes a transition.
func (r *taskRun) setState(ctx context.Context, to domain.State, reason string) error {
	from := r.t.State
	if err := domain.Transition(from, to); err != nil {
		return err
	}
	r.t.State = to
	r.t.UpdatedAt = r.e.d.Clock.Now()
	r.t.Downloaded = r.ckDownloaded.Load()
	if err := r.e.d.Store.UpdateTask(context.WithoutCancel(ctx), r.t); err != nil {
		r.t.State = from
		return err
	}
	r.log.Info("state", "from", from, "to", to, "reason", reason)
	r.e.publish(events.StateChanged{ID: r.t.ID, From: from, To: to, Reason: reason})
	return nil
}

func (r *taskRun) persist(ctx context.Context) error {
	r.t.UpdatedAt = r.e.d.Clock.Now()
	r.t.Downloaded = r.ckDownloaded.Load()
	return r.e.d.Store.UpdateTask(context.WithoutCancel(ctx), r.t)
}

func (r *taskRun) warn(msg string) {
	r.log.Warn(msg)
	r.e.publish(events.Warning{ID: r.t.ID, Msg: msg})
}

// run is the lifecycle loop: one inspect→download→finalize pass per
// iteration, with retry, offline waiting, pause and cancel in between.
func (r *taskRun) run(ctx context.Context) error {
	if err := r.enter(ctx); err != nil {
		if c := stopCause(ctx); c != nil {
			return r.stopped(ctx, c)
		}
		return err
	}
	for {
		err := r.once(ctx)
		if err == nil {
			return nil
		}
		if c := stopCause(ctx); c != nil && r.t.State != domain.StateVerifying {
			return r.stopped(ctx, c)
		}
		again, err := r.handleFailure(ctx, err)
		if !again {
			return err
		}
	}
}

// enter moves the stored task to queued.
func (r *taskRun) enter(ctx context.Context) error {
	switch r.t.State {
	case domain.StateQueued:
		return nil
	case domain.StatePaused, domain.StateFailed:
	case domain.StateInspecting, domain.StateDownloading, domain.StateRetrying:
		// Interrupted by a crash: settle in paused first.
		if err := r.setState(ctx, domain.StatePaused, "recovering interrupted run"); err != nil {
			return err
		}
	case domain.StateVerifying:
		if err := r.setState(ctx, domain.StateFailed, "recovering interrupted verification"); err != nil {
			return err
		}
	default:
		return domain.NewError(domain.KindPermanent, "task is "+string(r.t.State), "",
			fmt.Errorf("%w: %s", domain.ErrInvalidTransition, r.t.State))
	}
	return r.setState(ctx, domain.StateQueued, "run")
}

// once performs one pass from inspecting to completed. A non-nil error leaves
// the task in inspecting, downloading or verifying for handleFailure.
func (r *taskRun) once(ctx context.Context) error {
	if err := r.setState(ctx, domain.StateInspecting, ""); err != nil {
		return err
	}
	if r.client == nil {
		u, err := parseURL(r.t.OriginalURL)
		if err != nil {
			return err
		}
		c, err := r.e.clientFor(ctx, u)
		if err != nil {
			return err
		}
		r.client = c
	}
	segs, err := r.e.d.Store.LoadSegments(ctx, r.t.ID)
	if err != nil {
		return err
	}
	var resume *strategy.ResumeState
	var known *domain.Validators
	if len(segs) > 0 {
		if r.prevRangesProven && r.t.SizeKnown && r.t.Validators.Kind() != domain.ValidatorNone {
			v := r.t.Validators
			known = &v
			resume = &strategy.ResumeState{Validators: v, Size: r.t.Size, SizeKnown: r.t.SizeKnown, Previous: r.t.Strategy}
		} else {
			r.log.Info("discarding unresumable progress: ranges or validators were never proven")
			if err := r.discardProgress(ctx); err != nil {
				return err
			}
			segs = nil
		}
	}

	in := &strategy.Inspector{Client: r.client, Cfg: r.cfg, Clock: r.e.d.Clock}
	obs, err := in.Inspect(ctx, r.t.OriginalURL, known)
	if err != nil {
		return err
	}
	fallback := false
	switch {
	case obs.Err != nil && obs.RangeProbeInterrupted:
		r.probeInterrupts++
		if r.probeInterrupts < maxRangeProbeInterrupts {
			return obs.Err
		}
		// Persistent interruption (e.g. a proxy resetting ranged bodies):
		// stop waiting for range proof rather than fail the task.
		obs.Err, obs.RangesProven = nil, false
		obs.Anomalies = append(obs.Anomalies, reasonProbesInterrupted)
		fallback = true
	case obs.Err != nil:
		return obs.Err
	}
	r.probeInterrupts = 0
	d := strategy.Select(obs, r.cfg, resume)
	if fallback {
		d.Reasons = append(d.Reasons, reasonProbesInterrupted)
		r.warn(reasonProbesInterrupted)
	}
	r.recordDecision(d)
	if d.RemoteChanged {
		if !r.cfg.RestartOnRemoteChange {
			return d.Err
		}
		r.warn("remote file changed; restarting from zero")
		if err := r.discardProgress(ctx); err != nil {
			return err
		}
		segs = nil
	}
	if !d.Resume {
		r.t.Size, r.t.SizeKnown, r.t.Validators = obs.Size, obs.SizeKnown, obs.Validators
	}
	r.t.Strategy = d.Strategy
	r.t.FinalURL = obs.FinalURL
	if r.t.FileName == "" {
		r.t.FileName = storage.SanitizeFilename(obs.FileName)
	}
	r.prevRangesProven = d.RangesProven

	file, sched, err := r.openFile(ctx, d, segs)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	return r.download(ctx, d, file, sched)
}

// recordDecision stores and publishes d without any error cause chain.
func (r *taskRun) recordDecision(d strategy.Decision) {
	r.decision = d
	if d.Err != nil {
		d.Err = &domain.Error{Kind: d.Err.Kind, Msg: d.Err.Msg, Action: d.Err.Action, RetryAfter: d.Err.RetryAfter}
	}
	js, err := json.Marshal(d)
	if err != nil {
		js = nil
	}
	r.t.DecisionJSON = js
	r.log.Info("decision", "summary", d.Summary())
	r.e.publish(events.DecisionMade{ID: r.t.ID, Summary: d.Summary(), DecisionJSON: js})
}

// discardProgress drops checkpointed segments and the temp file.
func (r *taskRun) discardProgress(ctx context.Context) error {
	if err := r.e.d.Store.ClearSegments(context.WithoutCancel(ctx), r.t.ID); err != nil {
		return err
	}
	if r.t.TempPath != "" {
		if err := os.Remove(r.t.TempPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			r.log.Warn("remove stale temp file", "err", err)
		}
		r.t.TempPath = ""
	}
	r.ckDownloaded.Store(0)
	return r.persist(ctx)
}

// openFile reopens and verifies the temp file for a resume (spec §9.3), or
// creates a fresh one. sched is nil for a fresh download.
func (r *taskRun) openFile(ctx context.Context, d strategy.Decision, segs []domain.SegmentRecord) (*storage.TempFile, *Scheduler, error) {
	r.live.Store(0)
	if d.Resume && len(segs) > 0 {
		f, sched, err := r.recoverFile(ctx, segs)
		if err != nil || f != nil {
			return f, sched, err
		}
	}
	if err := r.discardProgress(ctx); err != nil {
		return nil, nil, err
	}
	f, err := storage.CreateTemp(r.t.DestDir, r.t.FileName, d.Size, d.SizeKnown)
	if err != nil {
		return nil, nil, err
	}
	r.t.TempPath = f.Path()
	if err := r.persist(ctx); err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	return f, nil, nil
}

// recoverFile reopens the temp file and re-verifies segs. A nil file without
// error means the progress is unusable and the caller starts fresh.
func (r *taskRun) recoverFile(ctx context.Context, segs []domain.SegmentRecord) (*storage.TempFile, *Scheduler, error) {
	if r.t.TempPath == "" {
		r.warn("temp file missing; restarting from zero")
		return nil, nil, nil
	}
	f, err := storage.OpenTemp(r.t.TempPath)
	if err != nil {
		r.warn("temp file missing; restarting from zero")
		return nil, nil, nil
	}
	recs, discarded, err := recoverSegments(ctx, f, r.t.Size, segs)
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	if discarded > 0 {
		r.log.Info("recovery discarded unverifiable progress", "bytes", discarded)
	}
	sched, err := RestoreScheduler(r.t.Size, recs)
	if err != nil {
		_ = f.Close()
		r.warn("checkpoint unusable (" + err.Error() + "); restarting from zero")
		return nil, nil, nil
	}
	var have int64
	for _, rec := range recs {
		have += rec.Pos - rec.Start
	}
	r.live.Store(have)
	r.ckDownloaded.Store(have)
	return f, sched, nil
}

// download runs the executor for d and finalizes on success.
func (r *taskRun) download(ctx context.Context, d strategy.Decision, file *storage.TempFile, sched *Scheduler) error {
	if err := r.setState(ctx, domain.StateDownloading, ""); err != nil {
		return err
	}
	single := d.Strategy == domain.StrategySingle && (sched == nil || sched.Pending() <= 1)
	if sched == nil {
		switch {
		case !d.SizeKnown:
			sched = NewOpenScheduler()
		case single:
			sched = NewScheduler(d.Size, d.Size)
		default:
			sched = NewScheduler(d.Size, d.ChunkSize)
		}
	}
	env := &runEnv{
		task:     &domain.Task{ID: r.t.ID, OriginalURL: r.t.OriginalURL, Validators: r.t.Validators},
		decision: d,
		client:   r.client,
		file:     file,
		sched:    sched,
		limits:   []*ratelimit.Bandwidth{ratelimit.NewBandwidth(r.cfg.PerDownloadRateLimit), r.e.d.Global},
		conns:    r.e.d.Conns,
		cfg:      r.cfg,
		clock:    r.e.d.Clock,
		backoff:  r.backoff,
		budget:   NewBudget(r.cfg.MaxSegmentAttempts),
		onBytes: func(n int64) {
			r.live.Add(n)
			r.written.Add(n)
		},
		onRetry: func(a domain.Attempt) { r.recordAttempt(ctx, a) },
		bufPool: r.e.bufPool,
	}
	var ck *checkpointer
	if d.SizeKnown {
		ck = newCheckpointer(ckStore{r.e.d.Store, r}, r.t.ID, file, sched, r.e.d.Clock, r.cfg.CheckpointInterval, r.cfg.CheckpointBytes)
		env.onCheckpoint = func(ctx context.Context, n int64) error {
			return asLocal(ck.MaybeCheckpoint(ctx, n))
		}
	}

	stopProgress := r.startProgress(d)
	var err error
	if single {
		err = runSingle(ctx, env)
	} else {
		cc := strategy.ControllerConfig{Initial: d.InitialConns, Max: d.MaxConns, Adaptive: r.cfg.AdaptiveConcurrency}
		if d.Strategy == domain.StrategyConservative {
			cc.Max, cc.StartConservative = r.cfg.MaxConnections, true
		}
		err = runMultipart(ctx, env, strategy.NewController(cc), r.onStrategy)
	}
	stopProgress()
	if err != nil {
		if ck != nil {
			if cerr := ck.Checkpoint(context.WithoutCancel(ctx)); cerr != nil {
				r.log.Warn("final checkpoint failed", "err", cerr)
			}
		}
		return err
	}
	return r.finish(ctx, file, env.sched)
}

// asLocal makes store failures terminal rather than retryable (T22).
func asLocal(err error) error {
	if err == nil {
		return nil
	}
	var de *domain.Error
	if errors.As(err, &de) {
		return err
	}
	return domain.NewError(domain.KindLocal, "save checkpoint: "+err.Error(), "check the data directory", err)
}

func (r *taskRun) onStrategy(from, to domain.Strategy, reason string) {
	r.log.Info("strategy changed", "from", from, "to", to, "reason", reason)
	r.e.publish(events.StrategyChanged{ID: r.t.ID, From: from, To: to, Reason: reason})
}

// recordAttempt stores and publishes one failed try; it is safe for
// concurrent use by workers.
func (r *taskRun) recordAttempt(ctx context.Context, a domain.Attempt) {
	n := int(r.retries.Add(1))
	if err := r.e.d.Store.AddAttempt(context.WithoutCancel(ctx), r.t.ID, a); err != nil {
		r.log.Warn("record attempt", "err", err)
	}
	r.log.Info("retry", "kind", a.Kind, "delay", a.Delay, "err", a.Message)
	r.e.publish(events.RetryScheduled{ID: r.t.ID, Attempt: n, Kind: a.Kind, Delay: a.Delay, Reason: a.Message})
}

// startProgress publishes Progress at ProgressHz until the returned stop
// function is called (which publishes a last sample).
func (r *taskRun) startProgress(d strategy.Decision) (stop func()) {
	hz := max(r.cfg.ProgressHz, 1)
	interval := time.Second / time.Duration(hz)
	clock := r.e.d.Clock
	quit, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		start := clock.Now()
		lastT, lastN := start, r.live.Load()
		var ewma float64
		emit := func() {
			now, n := clock.Now(), r.live.Load()
			if dt := now.Sub(lastT).Seconds(); dt > 0 {
				s := float64(n-lastN) / dt
				if ewma == 0 {
					ewma = s
				} else {
					ewma = speedAlpha*s + (1-speedAlpha)*ewma
				}
			}
			lastT, lastN = now, n
			p := events.Progress{ID: r.t.ID, Downloaded: n, Total: d.Size, TotalKnown: d.SizeKnown, SpeedBps: ewma}
			if d.SizeKnown && ewma > 0 && now.Sub(start) >= etaMinElapsed {
				eta := time.Duration(float64(max(d.Size-n, 0)) / ewma * float64(time.Second))
				p.ETA = &eta
			}
			r.e.publish(p)
		}
		for {
			select {
			case <-quit:
				emit()
				return
			case <-clock.After(interval):
				emit()
			}
		}
	}()
	return func() { close(quit); <-done }
}

// finish verifies and finalizes a fully downloaded file (spec §10).
func (r *taskRun) finish(ctx context.Context, file *storage.TempFile, sched *Scheduler) error {
	if err := r.setState(ctx, domain.StateVerifying, ""); err != nil {
		return err
	}
	if !sched.AllDone() {
		return domain.NewError(domain.KindIntegrity, "download ended with segments missing", "", nil)
	}
	if err := file.Sync(); err != nil {
		return asLocal(err)
	}
	size := sched.Total()
	if fi, err := os.Stat(file.Path()); err != nil || fi.Size() != size {
		return domain.NewError(domain.KindLocal, "temp file size does not match the download", "check the disk", err)
	}
	r.t.Size = size
	verify := verifiedSegments
	if r.t.ExpectedSHA256 != "" {
		bp, _ := r.e.bufPool.Get().(*[]byte)
		got, err := storage.HashRangeBuf(file, domain.ByteRange{Start: 0, End: size}, *bp)
		r.e.bufPool.Put(bp)
		if err != nil {
			return domain.NewError(domain.KindLocal, "hash temp file: "+err.Error(), "check the disk", err)
		}
		want, _ := hex.DecodeString(r.t.ExpectedSHA256)
		if !bytes.Equal(got, want) {
			// Segment hashes match the corrupt bytes, so a retry must refetch.
			if err := r.e.d.Store.ClearSegments(context.WithoutCancel(ctx), r.t.ID); err != nil {
				r.log.Warn("clear segments", "err", err)
			}
			r.ckDownloaded.Store(0)
			return domain.NewError(domain.KindIntegrity,
				fmt.Sprintf("SHA-256 mismatch: got %x, want %s", got, r.t.ExpectedSHA256),
				"the download is corrupt or the checksum is wrong; the temp file is kept for inspection", nil)
		}
		verify = verifiedSHA256
	}
	if err := file.Close(); err != nil {
		return asLocal(err)
	}
	dest, err := storage.SafeJoin(r.t.DestDir, r.t.FileName)
	if err != nil {
		return domain.NewError(domain.KindPolicy, "unsafe destination file name", "", err)
	}
	final, err := storage.Finalize(r.t.TempPath, dest, r.t.ExistingPolicy)
	if final == "" {
		return asLocal(err)
	}
	if err != nil {
		r.warn("download placed at " + final + " but not confirmed durable: " + err.Error())
	}
	r.t.FinalPath, r.t.VerifyResult, r.t.TempPath = final, verify, ""
	r.t.LastErrorKind, r.t.LastErrorMsg = domain.KindTransient, ""
	r.ckDownloaded.Store(size)
	r.log.Info("finalized", "path", final, "verify", verify)
	if err := r.setState(ctx, domain.StateCompleted, verify); err != nil {
		return err
	}
	if err := r.e.d.Store.ClearSegments(context.WithoutCancel(ctx), r.t.ID); err != nil {
		r.log.Warn("clear segments", "err", err)
	}
	return nil
}

// stopped settles a paused or cancelled run.
func (r *taskRun) stopped(ctx context.Context, cause error) error {
	if errors.Is(cause, domain.ErrCancelled) {
		if err := r.setState(ctx, domain.StateCancelled, "cancelled"); err != nil {
			return err
		}
		return domain.ErrCancelled
	}
	if err := r.setState(ctx, domain.StatePaused, "paused"); err != nil {
		return err
	}
	return cause
}

// handleFailure decides what follows a failed pass: retry (true), or a
// final state and the error to return (false).
func (r *taskRun) handleFailure(ctx context.Context, err error) (bool, error) {
	de := toDomainError(err)
	switch de.Kind {
	case domain.KindOffline:
		return r.waitOffline(ctx, de)
	case domain.KindTransient:
		return r.retry(ctx, de, de.Msg)
	case domain.KindRemoteChanged:
		unproven := r.t.State == domain.StateDownloading && !r.decision.RangesProven
		if unproven || r.cfg.RestartOnRemoteChange {
			// An interrupted download without proven ranges can only restart
			// (T20); otherwise restart only when configured.
			if derr := r.discardProgress(ctx); derr != nil {
				return false, derr
			}
			return r.retry(ctx, de, "remote changed; restarting from zero")
		}
		r.t.LastErrorKind, r.t.LastErrorMsg = de.Kind, de.Msg
		if serr := r.setState(ctx, domain.StatePaused, de.Msg); serr != nil {
			return false, serr
		}
		return false, de
	default:
		return false, r.fail(ctx, de)
	}
}

// toDomainError classifies err into a *domain.Error with a redacted message.
func toDomainError(err error) *domain.Error {
	var de *domain.Error
	if errors.As(err, &de) {
		return de
	}
	return domain.NewError(Classify(err), transport.RedactError(err).Error(), "", err)
}

// retry spends the task budget, enters retrying and sleeps the backoff.
func (r *taskRun) retry(ctx context.Context, de *domain.Error, reason string) (bool, error) {
	if n := r.written.Load(); n-r.failBytes >= progressBytes {
		r.budget.Progress()
		r.attempt = 0
	}
	r.failBytes = r.written.Load()
	if !r.budget.Spend() {
		return false, r.fail(ctx, domain.NewError(de.Kind, "retry budget exhausted: "+de.Msg, "retry later", de))
	}
	delay := r.backoff.Delay(r.attempt, de.RetryAfter)
	r.attempt++
	if err := r.setState(ctx, domain.StateRetrying, reason); err != nil {
		return false, err
	}
	r.recordAttempt(ctx, domain.Attempt{At: r.e.d.Clock.Now(), Kind: de.Kind, Message: de.Msg, Delay: delay})
	if err := r.e.d.Clock.Sleep(ctx, delay); err != nil {
		return false, r.stopped(ctx, cmpCause(ctx, err))
	}
	return true, nil
}

// cmpCause returns ctx's cause when it ended, else err.
func cmpCause(ctx context.Context, err error) error {
	if c := stopCause(ctx); c != nil {
		return c
	}
	return err
}

// waitOffline enters retrying "waiting for network" and polls connectivity
// without spending the retry budget (spec §8.1).
func (r *taskRun) waitOffline(ctx context.Context, de *domain.Error) (bool, error) {
	if err := r.setState(ctx, domain.StateRetrying, reasonWaitingForNetwork); err != nil {
		return false, err
	}
	base := r.e.d.OfflineBaseDelay
	r.recordAttempt(ctx, domain.Attempt{At: r.e.d.Clock.Now(), Kind: domain.KindOffline, Message: de.Msg, Delay: base})
	// Wait one interval first so a check that passes while requests still
	// fail cannot spin.
	if err := r.e.d.Clock.Sleep(ctx, base); err != nil {
		return false, r.stopped(ctx, cmpCause(ctx, err))
	}
	w := &OfflineWaiter{Clock: r.e.d.Clock, Limit: r.cfg.OfflineWaitLimit, Base: base, Check: r.checkOnline}
	err := w.Wait(ctx, func(attempt int, next time.Duration) {
		r.e.publish(events.RetryScheduled{ID: r.t.ID, Attempt: attempt + 1, Kind: domain.KindOffline, Delay: next, Reason: reasonWaitingForNetwork})
	})
	if err != nil {
		if c := stopCause(ctx); c != nil {
			return false, r.stopped(ctx, c)
		}
		return false, r.fail(ctx, toDomainError(err))
	}
	return true, nil
}

// checkOnline dials the origin host:port through the task client's dialer.
func (r *taskRun) checkOnline(ctx context.Context) error {
	u, err := url.Parse(r.t.OriginalURL)
	if err != nil {
		return err
	}
	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
	}
	// Without a client yet (host resolution failed), probe with a plain
	// dialer using the engine's resolver; the client is built after the wait.
	dial := (&net.Dialer{Resolver: r.e.d.Resolver}).DialContext
	if r.client != nil {
		if tr, ok := r.client.Transport.(*http.Transport); ok && tr.DialContext != nil {
			dial = tr.DialContext
		}
	}
	ctx, cancel := context.WithTimeout(ctx, offlineCheckTimeout)
	defer cancel()
	c, err := dial(ctx, "tcp", net.JoinHostPort(u.Hostname(), port))
	if err != nil {
		return err
	}
	return c.Close()
}

// fail records de (with a better action for expired signed URLs) and moves
// the task to failed. Verified progress is kept.
func (r *taskRun) fail(ctx context.Context, de *domain.Error) error {
	if de.Kind == domain.KindAuth {
		if u, err := url.Parse(r.t.OriginalURL); err == nil && transport.IsSignedURL(u) {
			de = &domain.Error{Kind: de.Kind, Msg: de.Msg, Action: expiredAction, Err: de}
		}
	}
	r.t.LastErrorKind, r.t.LastErrorMsg = de.Kind, de.Msg
	reason := de.Msg
	if de.Action != "" {
		reason += " (" + de.Action + ")"
	}
	if err := r.setState(ctx, domain.StateFailed, reason); err != nil {
		return errors.Join(de, err)
	}
	return de
}
