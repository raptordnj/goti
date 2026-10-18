package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/raptordnj/swiftfetch/internal/domain"
	"github.com/raptordnj/swiftfetch/internal/events"
	"github.com/raptordnj/swiftfetch/internal/services"
	"github.com/raptordnj/swiftfetch/internal/settings"
	"github.com/raptordnj/swiftfetch/internal/transport"
)

const (
	reasonWaitingForNetwork = "waiting for network" // downloader.reasonWaitingForNetwork
	maxRetries              = 10
	maxToasts               = 3
	toastTTL                = 5 * time.Second
	shortErrLen             = 60
)

type entry struct {
	t                       domain.Task
	chunksDone, chunksTotal int
	progress                bool // live progress received
	downloaded              int64
	total                   int64
	totalKnown              bool
	speed                   float64
	eta                     *time.Duration
	conns                   int
	summary                 string
	retries                 []AttemptView
	waitingNet              bool
	retryAt                 time.Time
}

// model is the mutable state folded from events; only the Run goroutine touches it.
type model struct {
	tasks       map[string]*entry
	toasts      []Toast
	err         string
	addSeq      uint64
	addErr      string
	running     func(string) bool
	queuePaused func() bool
	settings    func() settings.Settings
}

func newModelState() *model { return &model{tasks: make(map[string]*entry)} }

// setTask inserts or refreshes a task row from the DB, keeping live data.
func (m *model) setTask(t *domain.Task, attempts []domain.Attempt) {
	e, ok := m.tasks[t.ID]
	if !ok {
		e = &entry{}
		m.tasks[t.ID] = e
	}
	e.t = *t
	if !e.progress {
		e.downloaded, e.total, e.totalKnown = t.Downloaded, t.Size, t.SizeKnown
	}
	if attempts != nil {
		e.retries = e.retries[:0]
		for i, a := range attempts {
			e.retries = append(e.retries, AttemptView{At: a.At, Attempt: i + 1, Kind: a.Kind.String(), Message: a.Message, Delay: a.Delay})
		}
		e.retries = capRetries(e.retries)
	}
}

func capRetries(r []AttemptView) []AttemptView {
	if len(r) > maxRetries {
		r = append([]AttemptView(nil), r[len(r)-maxRetries:]...)
	}
	return r
}

// result says what I/O the caller must do after apply.
type result struct {
	reload string // task ID to re-read from the DB
	full   bool   // full reload
	dirty  bool   // model changed; publish
}

// apply folds one event into m. It performs no I/O.
func apply(m *model, ev events.Event, now time.Time) result {
	switch e := ev.(type) {
	case events.StateChanged:
		en, ok := m.tasks[e.ID]
		if !ok {
			return result{reload: e.ID, dirty: true}
		}
		en.t.State = e.To
		en.waitingNet = e.To == domain.StateRetrying && e.Reason == reasonWaitingForNetwork
		if e.To != domain.StateRetrying {
			en.retryAt = time.Time{}
		}
		if e.To != domain.StateDownloading {
			en.speed, en.eta, en.conns = 0, nil, 0
		}
		if !liveState(e.To) {
			// Not transferring: the DB row (reloaded next) is authoritative.
			en.progress = false
		}
		return result{reload: e.ID, dirty: true}
	case events.Progress:
		en, ok := m.tasks[e.ID]
		if !ok {
			return result{}
		}
		en.progress = true
		en.downloaded, en.total, en.totalKnown = e.Downloaded, e.Total, e.TotalKnown
		en.speed, en.eta, en.conns = e.SpeedBps, e.ETA, e.ActiveConns
		return result{dirty: true}
	case events.DecisionMade:
		if en, ok := m.tasks[e.ID]; ok {
			en.summary = e.Summary
			return result{dirty: true}
		}
	case events.StrategyChanged:
		if en, ok := m.tasks[e.ID]; ok {
			en.t.Strategy = e.To
			return result{dirty: true}
		}
	case events.RetryScheduled:
		if en, ok := m.tasks[e.ID]; ok {
			msg := e.Reason
			en.retries = capRetries(append(en.retries, AttemptView{At: now, Attempt: e.Attempt, Kind: e.Kind.String(), Message: msg, Delay: e.Delay}))
			en.retryAt = now.Add(e.Delay)
			en.waitingNet = e.Reason == reasonWaitingForNetwork
			return result{dirty: true}
		}
	case events.Warning:
		if e.Msg != "" {
			m.addToast(Toast{Kind: ToastInfo, Text: redactText(e.Msg), At: now})
			return result{dirty: true}
		}
	case events.CommandResult:
		r := result{reload: e.ID, dirty: true}
		if e.Cmd == "AddDownload" {
			m.addSeq++
			m.addErr = ""
			if e.Err != nil {
				m.addErr = redactText(transport.RedactError(e.Err).Error())
			}
		}
		switch {
		case e.Err != nil:
			m.addToast(Toast{Kind: ToastError, Text: redactText(transport.RedactError(e.Err).Error()), At: now})
		case e.Cmd == "AddDownload":
			m.addToast(Toast{Kind: ToastSuccess, Text: "Download added", At: now})
		}
		return r
	case events.Resync:
		for _, en := range m.tasks {
			en.progress = false
		}
		return result{full: true, dirty: true}
	}
	return result{}
}

func (m *model) addToast(t Toast) {
	m.toasts = append(m.toasts, t)
	if len(m.toasts) > maxToasts {
		m.toasts = append([]Toast(nil), m.toasts[len(m.toasts)-maxToasts:]...)
	}
}

// liveToasts drops expired toasts and returns the rest plus the next expiry.
func (m *model) liveToasts(now time.Time) ([]Toast, time.Time) {
	var out []Toast
	var next time.Time
	for _, t := range m.toasts {
		exp := t.At.Add(toastTTL)
		if exp.After(now) {
			out = append(out, t)
			if next.IsZero() || exp.Before(next) {
				next = exp
			}
		}
	}
	m.toasts = out
	return append([]Toast(nil), out...), next
}

func (m *model) isRunning(id string) bool {
	if m.running == nil {
		return true
	}
	return m.running(id)
}

func sectionOf(st domain.State, interrupted bool) Section {
	if interrupted {
		return SectionPaused
	}
	switch st {
	case domain.StateQueued:
		return SectionQueued
	case domain.StatePaused:
		return SectionPaused
	case domain.StateCompleted:
		return SectionCompleted
	case domain.StateFailed, domain.StateCancelled:
		return SectionFailed
	}
	return SectionActive
}

func view(e *entry, interrupted bool) TaskView {
	t := &e.t
	dest := t.FinalPath
	if dest == "" && t.DestDir != "" {
		dest = filepath.Join(t.DestDir, t.FileName)
	}
	v := TaskView{
		ID: t.ID, FileName: t.FileName, DestPath: dest, URL: transport.Redact(t.OriginalURL),
		State: t.State, Interrupted: interrupted, Section: sectionOf(t.State, interrupted),
		Downloaded: e.downloaded, Total: e.total, TotalKnown: e.totalKnown, Percent: -1,
		Strategy: string(t.Strategy), DecisionSummary: e.summary, VerifyResult: t.VerifyResult,
		Retries: append([]AttemptView(nil), e.retries...), CreatedAt: t.CreatedAt,
		RangesSupported: rangesProven(t.DecisionJSON), ChunksDone: e.chunksDone, ChunksTotal: e.chunksTotal,
		Resumable:  rangesProven(t.DecisionJSON) && t.Validators.Kind() != domain.ValidatorNone,
		waitingNet: e.waitingNet && t.State == domain.StateRetrying, retryAt: e.retryAt,
	}
	if t.State == domain.StateDownloading && !interrupted {
		v.SpeedBps, v.ETA, v.Conns = e.speed, e.eta, e.conns
	}
	if t.State == domain.StateCompleted && v.TotalKnown && v.Total > 0 {
		v.Downloaded = v.Total
	}
	if v.TotalKnown && v.Total > 0 {
		v.Percent = math.Min(100, float64(v.Downloaded)*100/float64(v.Total))
	} else if v.TotalKnown && t.State == domain.StateCompleted {
		v.Percent = 100
	}
	if t.State == domain.StateFailed && t.LastErrorMsg != "" {
		v.LastError = redactText(t.LastErrorMsg)
		v.LastErrorAction = errorAction(t.LastErrorKind)
	}
	a := actionsFor(t.State, interrupted, t.LastErrorKind)
	v.CanPause, v.CanResume, v.CanCancel, v.CanRetry, v.CanRestart, v.CanOpen = a.pause, a.resume, a.cancel, a.retry, a.restart, a.open
	return v
}

// build produces an immutable Snapshot from m at time now.
func build(m *model, now time.Time) *Snapshot {
	s := &Snapshot{Err: m.err, Tasks: make([]TaskView, 0, len(m.tasks)), AddResultSeq: m.addSeq, AddErr: m.addErr}
	for _, e := range m.tasks {
		interrupted := services.Interrupted(e.t.State) && !m.isRunning(e.t.ID)
		v := view(e, interrupted)
		s.Counts[v.Section]++
		if v.Section == SectionActive {
			s.ActiveCount++
			s.TotalSpeedBps += v.SpeedBps
			if v.waitingNet {
				s.Offline = true
			}
		}
		s.Tasks = append(s.Tasks, v)
	}
	sort.Slice(s.Tasks, func(i, j int) bool {
		a, b := &s.Tasks[i], &s.Tasks[j]
		if a.Section != b.Section {
			return a.Section < b.Section
		}
		if !a.CreatedAt.Equal(b.CreatedAt) {
			if a.Section == SectionCompleted {
				return a.CreatedAt.After(b.CreatedAt)
			}
			return a.CreatedAt.Before(b.CreatedAt)
		}
		return a.ID < b.ID
	})
	s.Toasts, _ = m.liveToasts(now)
	if m.settings != nil {
		s.Settings = m.settings()
	}
	if m.queuePaused != nil {
		s.QueuePaused = m.queuePaused()
	}
	return s
}

// StatusText is the TaskRow status line (spec §8).
func StatusText(v TaskView, now time.Time) string {
	if v.Interrupted {
		return "Interrupted"
	}
	switch v.State {
	case domain.StateQueued:
		return "Queued"
	case domain.StateInspecting:
		return "Inspecting · validating strategy"
	case domain.StateDownloading:
		return "Downloading"
	case domain.StateRetrying:
		if v.waitingNet {
			return "Waiting for network"
		}
		if !v.retryAt.IsZero() {
			secs := int(math.Ceil(v.retryAt.Sub(now).Seconds()))
			if secs < 0 {
				secs = 0
			}
			return fmt.Sprintf("Retrying in %d s", secs)
		}
		return "Retrying"
	case domain.StatePaused:
		return "Paused"
	case domain.StateVerifying:
		return "Verifying"
	case domain.StateCompleted:
		return "Completed"
	case domain.StateFailed:
		if v.LastError == "" {
			return "Failed"
		}
		return "Failed: " + shortError(v.LastError)
	case domain.StateCancelled:
		return "Cancelled"
	}
	return string(v.State)
}

func shortError(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	r := []rune(strings.TrimSpace(s))
	if len(r) > shortErrLen {
		return string(r[:shortErrLen-1]) + "…"
	}
	return string(r)
}

// TaskStore is the read side of persistence used by the state goroutine.
type TaskStore interface {
	ListTasks(ctx context.Context) ([]*domain.Task, error)
	GetTask(ctx context.Context, id string) (*domain.Task, error)
	ListAttempts(ctx context.Context, id string) ([]domain.Attempt, error)
	LoadSegments(ctx context.Context, taskID string) ([]domain.SegmentRecord, error)
}

// rangesProven reads the "ranges_proven" flag of a strategy decision.
func rangesProven(js []byte) bool {
	var d struct {
		RangesProven bool `json:"ranges_proven"`
	}
	return len(js) > 0 && json.Unmarshal(js, &d) == nil && d.RangesProven
}

// loadTask re-reads attempts and segments of t and stores it in m.
func loadTask(ctx context.Context, m *model, st TaskStore, t *domain.Task) error {
	att, err := st.ListAttempts(ctx, t.ID)
	if err != nil {
		return err
	}
	if att == nil {
		att = []domain.Attempt{}
	}
	segs, err := st.LoadSegments(ctx, t.ID)
	if err != nil {
		return err
	}
	m.setTask(t, att)
	e := m.tasks[t.ID]
	e.chunksDone, e.chunksTotal = 0, len(segs)
	for _, sg := range segs {
		if sg.Done {
			e.chunksDone++
		}
	}
	return nil
}

func reloadAll(ctx context.Context, m *model, st TaskStore) error {
	ts, err := st.ListTasks(ctx)
	if err != nil {
		return err
	}
	seen := make(map[string]bool, len(ts))
	for _, t := range ts {
		seen[t.ID] = true
		if err := loadTask(ctx, m, st, t); err != nil {
			return err
		}
	}
	for id := range m.tasks {
		if !seen[id] {
			delete(m.tasks, id)
		}
	}
	return nil
}

func reloadOne(ctx context.Context, m *model, st TaskStore, id string) error {
	t, err := st.GetTask(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		delete(m.tasks, id)
		return nil
	}
	if err != nil {
		return err
	}
	return loadTask(ctx, m, st, t)
}

func liveState(s domain.State) bool {
	return s == domain.StateInspecting || s == domain.StateDownloading || s == domain.StateRetrying
}

var urlRe = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://[^\s"'<>]+`)

// redactText replaces every URL in s with its redacted form.
func redactText(s string) string {
	return urlRe.ReplaceAllStringFunc(s, transport.Redact)
}
