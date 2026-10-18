package state

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/raptordnj/swiftfetch/internal/clocktest"
	"github.com/raptordnj/swiftfetch/internal/domain"
	"github.com/raptordnj/swiftfetch/internal/events"
	"github.com/raptordnj/swiftfetch/internal/settings"
)

var t0 = time.Unix(100000, 0)

func task(id string, st domain.State, created time.Duration) *domain.Task {
	return &domain.Task{ID: id, OriginalURL: "https://user:pw@example.com/" + id + "?token=x", FileName: id + ".bin",
		FinalPath: "/dl/" + id + ".bin", State: st, CreatedAt: t0.Add(created), Size: 1000, SizeKnown: true}
}

func newModel(ts ...*domain.Task) *model {
	m := newModelState()
	for _, t := range ts {
		m.setTask(t, nil)
	}
	return m
}

func find(s *Snapshot, id string) *TaskView {
	for i := range s.Tasks {
		if s.Tasks[i].ID == id {
			return &s.Tasks[i]
		}
	}
	return nil
}

func TestApplyStateChanged(t *testing.T) {
	m := newModel(task("a", domain.StateQueued, 0))
	r := apply(m, events.StateChanged{ID: "a", From: domain.StateQueued, To: domain.StateDownloading}, t0)
	if r.reload != "a" {
		t.Fatalf("want reload of a, got %+v", r)
	}
	v := find(build(m, t0), "a")
	if v.State != domain.StateDownloading || v.Section != SectionActive || !v.CanPause {
		t.Fatalf("%+v", v)
	}
	if strings.Contains(v.URL, "pw") || strings.Contains(v.URL, "token=x") {
		t.Fatalf("URL not redacted: %s", v.URL)
	}
}

func TestApplyProgress(t *testing.T) {
	m := newModel(task("a", domain.StateDownloading, 0))
	eta := 3 * time.Second
	apply(m, events.Progress{ID: "a", Downloaded: 250, Total: 1000, TotalKnown: true, SpeedBps: 10, ActiveConns: 4, ETA: &eta}, t0)
	s := build(m, t0)
	v := find(s, "a")
	if v.Percent != 25 || v.Downloaded != 250 || v.Conns != 4 || v.ETA == nil || s.TotalSpeedBps != 10 || s.ActiveCount != 1 {
		t.Fatalf("%+v", v)
	}
	apply(m, events.Progress{ID: "a", Downloaded: 300}, t0)
	if v := find(build(m, t0), "a"); v.Percent != -1 || v.TotalKnown {
		t.Fatalf("want -1, got %+v", v)
	}
}

func TestApplyRetry(t *testing.T) {
	m := newModel(task("a", domain.StateRetrying, 0))
	for i := 1; i <= 15; i++ {
		apply(m, events.RetryScheduled{ID: "a", Attempt: i, Delay: time.Second, Reason: fmt.Sprint("r", i)}, t0)
	}
	v := find(build(m, t0), "a")
	if len(v.Retries) != 10 || v.Retries[0].Attempt != 6 || v.Retries[9].Attempt != 15 {
		t.Fatalf("%+v", v.Retries)
	}
}

type fakeStore struct {
	mu    sync.Mutex
	tasks map[string]*domain.Task
	segs  map[string][]domain.SegmentRecord
	err   error
}

func (f *fakeStore) put(t *domain.Task) { f.mu.Lock(); f.tasks[t.ID] = t; f.mu.Unlock() }
func (f *fakeStore) ListTasks(context.Context) ([]*domain.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	var out []*domain.Task
	for _, t := range f.tasks {
		c := *t
		out = append(out, &c)
	}
	return out, nil
}
func (f *fakeStore) GetTask(_ context.Context, id string) (*domain.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tasks[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	c := *t
	return &c, nil
}
func (f *fakeStore) ListAttempts(context.Context, string) ([]domain.Attempt, error) { return nil, nil }
func (f *fakeStore) LoadSegments(_ context.Context, id string) ([]domain.SegmentRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.segs[id], nil
}

func TestRangeResumeChunksMapping(t *testing.T) {
	a := task("a", domain.StatePaused, 0)
	a.DecisionJSON = []byte(`{"ranges_proven":true}`)
	a.Validators = domain.Validators{ETag: "x", ETagStrong: true}
	b := task("b", domain.StateQueued, 1)
	b.DecisionJSON = []byte(`{"ranges_proven":true}`) // no validators
	c := task("c", domain.StateFailed, 2)             // no decision
	fs := &fakeStore{tasks: map[string]*domain.Task{"a": a, "b": b, "c": c},
		segs: map[string][]domain.SegmentRecord{"a": {{Done: true}, {Done: true}, {}, {}}}}
	m := newModelState()
	if err := reloadAll(context.Background(), m, fs); err != nil {
		t.Fatal(err)
	}
	s := build(m, t0)
	va, vb, vc := find(s, "a"), find(s, "b"), find(s, "c")
	if !va.RangesSupported || !va.Resumable || va.ChunksDone != 2 || va.ChunksTotal != 4 {
		t.Errorf("a %+v", *va)
	}
	if !vb.RangesSupported || vb.Resumable || vb.ChunksTotal != 0 {
		t.Errorf("b %+v", *vb)
	}
	if vc.RangesSupported || vc.Resumable {
		t.Errorf("c %+v", *vc)
	}
	fs.put(c)
	fs.mu.Lock()
	fs.segs["c"] = []domain.SegmentRecord{{Done: true}}
	fs.mu.Unlock()
	if err := reloadOne(context.Background(), m, fs, "c"); err != nil {
		t.Fatal(err)
	}
	if v := find(build(m, t0), "c"); v.ChunksDone != 1 || v.ChunksTotal != 1 {
		t.Errorf("reloadOne c %+v", *v)
	}
}

func TestResyncReloads(t *testing.T) {
	m := newModel(task("a", domain.StateQueued, 0))
	if r := apply(m, events.Resync{}, t0); !r.full {
		t.Fatal("Resync must request full reload")
	}
	fs := &fakeStore{tasks: map[string]*domain.Task{"b": task("b", domain.StatePaused, 0)}}
	if err := reloadAll(context.Background(), m, fs); err != nil {
		t.Fatal(err)
	}
	s := build(m, t0)
	if find(s, "a") != nil || find(s, "b") == nil {
		t.Fatalf("%+v", s.Tasks)
	}
}

func TestSectionsOrder(t *testing.T) {
	c1, c2 := task("c1", domain.StateCompleted, 1), task("c2", domain.StateCompleted, 2)
	q1, q2 := task("q1", domain.StateQueued, 5), task("q2", domain.StateQueued, 3)
	f := task("f", domain.StateCancelled, 0)
	d := task("d", domain.StateDownloading, 9)
	m := newModel(c1, c2, q1, q2, f, d)
	m.running = func(string) bool { return true }
	s := build(m, t0)
	var ids []string
	for _, v := range s.Tasks {
		ids = append(ids, v.ID)
	}
	if got := strings.Join(ids, ","); got != "d,q2,q1,c2,c1,f" {
		t.Fatalf("order %s", got)
	}
	if s.Counts[SectionQueued] != 2 || s.Counts[SectionFailed] != 1 || s.Counts[SectionCompleted] != 2 {
		t.Fatalf("%v", s.Counts)
	}
}

func TestInterruptedMapping(t *testing.T) {
	m := newModel(task("a", domain.StateDownloading, 0))
	m.running = func(string) bool { return false }
	v := find(build(m, t0), "a")
	if !v.Interrupted || v.Section != SectionPaused || !v.CanResume || v.CanPause || !v.CanCancel {
		t.Fatalf("%+v", v)
	}
	if StatusText(*v, t0) != "Interrupted" {
		t.Fatal(StatusText(*v, t0))
	}
}

func TestActionsTable(t *testing.T) {
	type acts struct{ pause, resume, cancel, retry, restart, open bool }
	want := map[domain.State]acts{
		domain.StateQueued:      {pause: true, cancel: true},
		domain.StateInspecting:  {pause: true, cancel: true},
		domain.StateDownloading: {pause: true, cancel: true},
		domain.StateRetrying:    {pause: true, cancel: true},
		domain.StateVerifying:   {cancel: true},
		domain.StatePaused:      {resume: true, cancel: true},
		domain.StateCompleted:   {open: true},
		domain.StateFailed:      {retry: true, cancel: true},
		domain.StateCancelled:   {},
	}
	for _, st := range domain.AllStates {
		a := actionsFor(st, false, domain.KindTransient)
		got := acts{a.pause, a.resume, a.cancel, a.retry, a.restart, a.open}
		if got != want[st] {
			t.Errorf("%s: got %+v want %+v", st, got, want[st])
		}
	}
	if a := actionsFor(domain.StateFailed, false, domain.KindRemoteChanged); !a.restart || !a.retry {
		t.Errorf("remote changed failed must allow restart: %+v", a)
	}
	for _, st := range []domain.State{domain.StateInspecting, domain.StateDownloading, domain.StateRetrying, domain.StateVerifying} {
		if a := actionsFor(st, true, domain.KindTransient); (a != actions{resume: true, cancel: true}) {
			t.Errorf("interrupted %s: %+v", st, a)
		}
	}
}

func TestStatusText(t *testing.T) {
	now := t0
	retryAt := now.Add(3500 * time.Millisecond)
	cases := []struct {
		v    TaskView
		want string
	}{
		{TaskView{State: domain.StateInspecting}, "Inspecting · validating strategy"},
		{TaskView{State: domain.StateDownloading}, "Downloading"},
		{TaskView{State: domain.StateRetrying, waitingNet: true}, "Waiting for network"},
		{TaskView{State: domain.StateRetrying, retryAt: retryAt}, "Retrying in 4 s"},
		{TaskView{State: domain.StatePaused}, "Paused"},
		{TaskView{State: domain.StateDownloading, Interrupted: true}, "Interrupted"},
		{TaskView{State: domain.StateVerifying}, "Verifying"},
		{TaskView{State: domain.StateCompleted}, "Completed"},
		{TaskView{State: domain.StateFailed, LastError: "server said no"}, "Failed: server said no"},
		{TaskView{State: domain.StateCancelled}, "Cancelled"},
		{TaskView{State: domain.StateQueued}, "Queued"},
	}
	for _, c := range cases {
		if got := StatusText(c.v, now); got != c.want {
			t.Errorf("%s: got %q want %q", c.v.State, got, c.want)
		}
	}
	long := TaskView{State: domain.StateFailed, LastError: strings.Repeat("x", 200) + "\nmore"}
	if got := StatusText(long, now); len([]rune(got)) > 70 || strings.Contains(got, "\n") {
		t.Errorf("short error not short: %q", got)
	}
}

func TestToastsExpire(t *testing.T) {
	m := newModel()
	for i := 0; i < 4; i++ {
		apply(m, events.CommandResult{Cmd: "PauseTask", Err: fmt.Errorf("e%d", i)}, t0.Add(time.Duration(i)*time.Second))
	}
	apply(m, events.CommandResult{Cmd: "PauseTask"}, t0)
	s := build(m, t0.Add(3*time.Second))
	if len(s.Toasts) != 3 || s.Toasts[0].Text != "e1" || s.Toasts[2].Text != "e3" || s.Toasts[2].Kind != ToastError {
		t.Fatalf("%+v", s.Toasts)
	}
	apply(m, events.CommandResult{Cmd: "AddDownload", ID: "n"}, t0.Add(4*time.Second))
	s = build(m, t0.Add(7500*time.Millisecond)) // e1 (t+1) and e2 (t+2) expired
	if len(s.Toasts) != 2 || s.Toasts[0].Text != "e3" || s.Toasts[1].Text != "Download added" || s.Toasts[1].Kind != ToastSuccess {
		t.Fatalf("%+v", s.Toasts)
	}
}

func TestOfflineFlag(t *testing.T) {
	m := newModel(task("a", domain.StateDownloading, 0))
	m.running = func(string) bool { return true }
	apply(m, events.StateChanged{ID: "a", From: domain.StateDownloading, To: domain.StateRetrying, Reason: "waiting for network"}, t0)
	s := build(m, t0)
	if !s.Offline || StatusText(*find(s, "a"), t0) != "Waiting for network" {
		t.Fatalf("offline %v", s.Offline)
	}
	apply(m, events.StateChanged{ID: "a", From: domain.StateRetrying, To: domain.StateInspecting}, t0)
	if build(m, t0).Offline {
		t.Fatal("offline should clear")
	}
	apply(m, events.StateChanged{ID: "a", From: domain.StateInspecting, To: domain.StateRetrying}, t0)
	if build(m, t0).Offline {
		t.Fatal("plain retry is not offline")
	}
	apply(m, events.RetryScheduled{ID: "a", Delay: time.Second, Reason: "waiting for network"}, t0)
	if !build(m, t0).Offline {
		t.Fatal("RetryScheduled offline")
	}
}

func TestLoadNeverNil(t *testing.T) {
	var s Store
	if s.Load() == nil {
		t.Fatal("nil snapshot")
	}
}

func TestRunLoadError(t *testing.T) {
	clk := clocktest.New(t0)
	fs := &fakeStore{tasks: map[string]*domain.Task{}, err: errors.New("db gone")}
	var st Store
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { Run(ctx, Deps{Bus: events.NewBus(10, clk), Store: fs, Clock: clk}, &st); close(done) }()
	waitFor(t, func() bool { return st.Load().Err != "" })
	cancel()
	<-done
}

func waitFor(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !f() {
		if time.Now().After(deadline) {
			t.Fatal("timeout")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestStateCoalesces(t *testing.T) {
	clk := clocktest.New(t0)
	bus := events.NewBus(1000, clk)
	fs := &fakeStore{tasks: map[string]*domain.Task{}}
	for i := 0; i < 100; i++ {
		fs.put(task(fmt.Sprint("t", i), domain.StateDownloading, time.Duration(i)))
	}
	var inv atomic.Int64
	var st Store
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Run(ctx, Deps{Bus: bus, Store: fs, Clock: clk, Invalidate: func() { inv.Add(1) },
			Running: func(string) bool { return true }, QueuePaused: func() bool { return false },
			Settings: func() settings.Settings { return settings.Settings{MaxSimultaneous: 3} }}, &st)
		close(done)
	}()
	waitFor(t, func() bool { return len(st.Load().Tasks) == 100 })
	start := time.Now()
	for i := 0; i < 10000; i++ {
		bus.Publish(events.Progress{ID: fmt.Sprint("t", i%100), Downloaded: int64(i), Total: 10000, TotalKnown: true})
		clk.Advance(100 * time.Microsecond)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("Publish blocked")
	}
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
	if n := inv.Load(); n > 11 || n < 2 {
		t.Fatalf("invalidate calls %d", n)
	}
	if st.Load().Settings.MaxSimultaneous != 3 {
		t.Fatal("settings not in snapshot")
	}
}

func TestCompletedAfterProgressShowsFull(t *testing.T) {
	m := newModel(task("a", domain.StateDownloading, 0))
	apply(m, events.Progress{ID: "a", Downloaded: 990, Total: 1000, TotalKnown: true}, t0)
	apply(m, events.StateChanged{ID: "a", From: domain.StateVerifying, To: domain.StateCompleted}, t0)
	c := task("a", domain.StateCompleted, 0)
	c.Downloaded = 1000
	m.setTask(c, nil) // reload after StateChanged
	if v := find(build(m, t0), "a"); v.Percent != 100 || v.Downloaded != 1000 {
		t.Fatalf("%+v", v)
	}
	// Even if the DB row lags, Completed shows Downloaded = Size.
	m2 := newModel(task("b", domain.StateDownloading, 0))
	apply(m2, events.Progress{ID: "b", Downloaded: 990, Total: 1000, TotalKnown: true}, t0)
	apply(m2, events.StateChanged{ID: "b", From: domain.StateVerifying, To: domain.StateCompleted}, t0)
	if v := find(build(m2, t0), "b"); v.Percent != 100 || v.Downloaded != 1000 {
		t.Fatalf("%+v", v)
	}
}

func TestPausedUsesDBDownloaded(t *testing.T) {
	m := newModel(task("a", domain.StateDownloading, 0))
	apply(m, events.Progress{ID: "a", Downloaded: 700, Total: 1000, TotalKnown: true}, t0)
	apply(m, events.StateChanged{ID: "a", From: domain.StateDownloading, To: domain.StatePaused}, t0)
	p := task("a", domain.StatePaused, 0)
	p.Downloaded = 640
	m.setTask(p, nil)
	if v := find(build(m, t0), "a"); v.Downloaded != 640 || v.Percent != 64 {
		t.Fatalf("%+v", v)
	}
	// Resync also drops live numbers in favour of the DB.
	apply(m, events.StateChanged{ID: "a", From: domain.StateQueued, To: domain.StateDownloading}, t0)
	apply(m, events.Progress{ID: "a", Downloaded: 900, Total: 1000, TotalKnown: true}, t0)
	apply(m, events.Resync{}, t0)
	p.Downloaded, p.State = 880, domain.StateDownloading
	m.setTask(p, nil)
	if v := find(build(m, t0), "a"); v.Downloaded != 880 {
		t.Fatalf("after resync %+v", v)
	}
}

func TestToastRedactsCredentials(t *testing.T) {
	m := newModel()
	apply(m, events.CommandResult{Cmd: "AddDownload", Err: fmt.Errorf("fetch https://bob:hunter2@example.com/f?token=abc failed")}, t0)
	apply(m, events.CommandResult{Cmd: "AddDownload", Err: &url.Error{Op: "Get", URL: "https://bob:hunter2@example.com/x", Err: errors.New("eof")}}, t0)
	for _, ts := range build(m, t0).Toasts {
		if strings.Contains(ts.Text, "hunter2") || strings.Contains(ts.Text, "abc") {
			t.Fatalf("leaked: %q", ts.Text)
		}
	}
}

func TestAddResultSequence(t *testing.T) {
	m := newModel()
	t0 := time.Unix(1e6, 0)
	if s := build(m, t0); s.AddResultSeq != 0 || s.AddErr != "" {
		t.Fatalf("initial %d %q", s.AddResultSeq, s.AddErr)
	}
	apply(m, events.CommandResult{Cmd: "AddDownload", Err: fmt.Errorf("bad https://u:pw@h/x")}, t0)
	s := build(m, t0)
	if s.AddResultSeq != 1 || s.AddErr == "" || strings.Contains(s.AddErr, "pw") {
		t.Fatalf("after error %d %q", s.AddResultSeq, s.AddErr)
	}
	apply(m, events.CommandResult{Cmd: "PauseTask", Err: fmt.Errorf("x")}, t0)
	if s := build(m, t0); s.AddResultSeq != 1 {
		t.Fatalf("other command bumped seq: %d", s.AddResultSeq)
	}
	apply(m, events.CommandResult{Cmd: "AddDownload", ID: "n"}, t0)
	if s := build(m, t0); s.AddResultSeq != 2 || s.AddErr != "" {
		t.Fatalf("after success %d %q", s.AddResultSeq, s.AddErr)
	}
}

func TestTrayNoticeOnce(t *testing.T) {
	const notice = "Closing the window keeps downloads running in the tray (Settings › Close to tray)"
	for _, tc := range []struct {
		name      string
		avail     bool
		shown     bool
		wantToast bool
	}{{"available", true, false, true}, {"no tray", false, false, false}, {"already shown", true, true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			clk := clocktest.New(t0)
			bus := events.NewBus(1000, clk)
			fs := &fakeStore{tasks: map[string]*domain.Task{}}
			fs.put(task("a", domain.StateDownloading, 0))
			var marks atomic.Int64
			var st Store
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() {
				Run(ctx, Deps{Bus: bus, Store: fs, Clock: clk,
					Running: func(string) bool { return true }, QueuePaused: func() bool { return false },
					Settings:       func() settings.Settings { return settings.Settings{TrayNoticeShown: tc.shown} },
					TrayAvailable:  func() bool { return tc.avail },
					MarkTrayNotice: func() { marks.Add(1) }}, &st)
				close(done)
			}()
			waitFor(t, func() bool { return len(st.Load().Tasks) == 1 })
			for i := 0; i < 5; i++ {
				bus.Publish(events.Progress{ID: "a", Downloaded: int64(i), Total: 10, TotalKnown: true})
				clk.Advance(200 * time.Millisecond)
			}
			// Poll until the toast count and marks are stable for 5 checks
			// (or a 2 s deadline), instead of a fixed sleep.
			count := func() int {
				n := 0
				for _, ts := range st.Load().Toasts {
					if ts.Text == notice {
						n++
					}
				}
				return n
			}
			n, m, stable := count(), marks.Load(), 0
			for deadline := time.Now().Add(2 * time.Second); stable < 5 && time.Now().Before(deadline); {
				time.Sleep(5 * time.Millisecond)
				if nn, mm := count(), marks.Load(); nn == n && mm == m {
					stable++
				} else {
					n, m, stable = nn, mm, 0
				}
			}
			cancel()
			<-done
			want := int64(0)
			if tc.wantToast {
				want = 1
			}
			if marks.Load() != want || (n == 1) != tc.wantToast || n > 1 {
				t.Fatalf("marks=%d toasts=%d", marks.Load(), n)
			}
		})
	}
}
