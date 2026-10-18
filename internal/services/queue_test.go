package services

import (
	"context"
	"net/http"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/raptordnj/swiftfetch/internal/clocktest"
	"github.com/raptordnj/swiftfetch/internal/config"
	"github.com/raptordnj/swiftfetch/internal/domain"
	"github.com/raptordnj/swiftfetch/internal/downloader"
	"github.com/raptordnj/swiftfetch/internal/events"
	"github.com/raptordnj/swiftfetch/internal/persistence"
	"github.com/raptordnj/swiftfetch/internal/ratelimit"
	"github.com/raptordnj/swiftfetch/internal/transport"
	"github.com/raptordnj/swiftfetch/tests/faultserver"
)

// qFixture is a real Engine over SQLite and a throttled fault server.
type qFixture struct {
	e     *downloader.Engine
	db    *persistence.DB
	srv   *faultserver.Server
	bus   *events.Bus
	clock *clocktest.FakeClock

	mu  sync.Mutex
	evs []events.Event
}

func newQFixture(t *testing.T) *qFixture {
	t.Helper()
	f := &qFixture{
		srv:   faultserver.New(t, faultserver.Options{Size: 1 << 20, Seed: 3, ETag: "v1", BytesPerSecondGlobal: 3 << 20}),
		clock: clocktest.New(time.Unix(1_000_000, 0)),
	}
	db, err := persistence.Open(filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	f.db = db
	cfg := config.Default()
	cfg.DownloadDir = t.TempDir()
	cfg.DataDir = t.TempDir()
	cfg.RetryBaseDelay, cfg.RetryMaxDelay = 5*time.Millisecond, 20*time.Millisecond
	cfg.CheckpointInterval = 100 * time.Millisecond
	cfg.ProgressHz = 20
	f.bus = events.NewBus(cfg.ProgressHz, domain.RealClock{})
	sub := f.bus.Subscribe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			ev, err := sub.Next(ctx)
			if err != nil {
				return
			}
			f.mu.Lock()
			f.evs = append(f.evs, ev)
			f.mu.Unlock()
		}
	}()
	t.Cleanup(func() { cancel(); <-done })
	f.e = downloader.NewEngine(downloader.EngineDeps{
		Store: db, Config: cfg, Bus: f.bus, Clock: domain.RealClock{},
		Global: ratelimit.NewBandwidth(0), Conns: ratelimit.NewSemaphore(cfg.GlobalMaxConnections),
		NewClient: func(allowPrivate bool) *http.Client {
			return transport.NewClient(transport.ClientOptions{
				Policy: cfg.NetworkPolicy, AllowPrivate: allowPrivate, Dialer: f.srv.Dialer(),
			})
		},
	})
	return f
}

func (f *qFixture) add(t *testing.T) string {
	t.Helper()
	task, err := f.e.Add(context.Background(), downloader.AddRequest{URL: f.srv.URL + "/file"})
	if err != nil {
		t.Fatal(err)
	}
	return task.ID
}

func (f *qFixture) task(t *testing.T, id string) *domain.Task {
	t.Helper()
	task, err := f.db.GetTask(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func (f *qFixture) setState(t *testing.T, id string, s domain.State) {
	t.Helper()
	task := f.task(t, id)
	task.State = s
	if err := f.db.UpdateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
}

func (f *qFixture) queue(t *testing.T, max int, resume bool) *Queue {
	t.Helper()
	q := NewQueue(QueueDeps{Engine: f.e, Store: f.db, Bus: f.bus, Clock: f.clock, MaxSimultaneous: max, ResumeOnStartup: resume})
	t.Cleanup(func() { _ = q.Shutdown(context.Background()) })
	return q
}

func (f *qFixture) events() []events.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]events.Event(nil), f.evs...)
}

// entered reports whether id ever reached state s.
func (f *qFixture) entered(id string, s domain.State) bool {
	for _, ev := range f.events() {
		if sc, ok := ev.(events.StateChanged); ok && sc.ID == id && sc.To == s {
			return true
		}
	}
	return false
}

func (f *qFixture) runningCount(ids []string) int {
	n := 0
	for _, id := range ids {
		if f.e.Running(id) {
			n++
		}
	}
	return n
}

func (f *qFixture) waitState(t *testing.T, d time.Duration, s domain.State, ids ...string) {
	t.Helper()
	waitFor(t, d, string(s), func() bool {
		for _, id := range ids {
			if f.task(t, id).State != s {
				return false
			}
		}
		return true
	})
}

func waitFor(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func addN(t *testing.T, f *qFixture, n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = f.add(t)
	}
	return ids
}

func TestInterrupted(t *testing.T) {
	for _, s := range domain.AllStates {
		want := s == domain.StateInspecting || s == domain.StateDownloading || s == domain.StateRetrying || s == domain.StateVerifying
		if Interrupted(s) != want {
			t.Errorf("Interrupted(%s) = %v", s, !want)
		}
	}
}

func TestQueueMaxSimultaneous(t *testing.T) {
	f := newQFixture(t)
	ids := addN(t, f, 5)
	q := f.queue(t, 2, true)
	q.Start(context.Background())
	maxSeen := 0
	waitFor(t, 30*time.Second, "all complete", func() bool {
		if n := f.runningCount(ids); n > maxSeen {
			maxSeen = n
		}
		for _, id := range ids {
			if f.task(t, id).State != domain.StateCompleted {
				return false
			}
		}
		return true
	})
	if maxSeen > 2 || maxSeen == 0 {
		t.Fatalf("max running %d", maxSeen)
	}
}

func TestQueueFIFO(t *testing.T) {
	f := newQFixture(t)
	ids := addN(t, f, 3)
	// Give the tasks a CreatedAt order opposite to their ID order.
	sorted := append([]string(nil), ids...)
	sort.Sort(sort.Reverse(sort.StringSlice(sorted)))
	base := time.Unix(2_000_000, 0)
	for i, id := range sorted {
		task := f.task(t, id)
		task.CreatedAt = base.Add(time.Duration(i) * time.Second)
		if err := f.db.UpdateTask(context.Background(), task); err != nil {
			t.Fatal(err)
		}
	}
	q := f.queue(t, 1, true)
	q.Start(context.Background())
	f.waitState(t, 30*time.Second, domain.StateCompleted, ids...)
	var order []string
	for _, ev := range f.events() {
		if sc, ok := ev.(events.StateChanged); ok && sc.To == domain.StateCompleted {
			order = append(order, sc.ID)
		}
	}
	for i := range sorted {
		if order[i] != sorted[i] {
			t.Fatalf("order %v, want %v", order, sorted)
		}
	}
}

func TestQueueLowerSlots(t *testing.T) {
	f := newQFixture(t)
	ids := addN(t, f, 5)
	q := f.queue(t, 3, true)
	q.Start(context.Background())
	waitFor(t, 10*time.Second, "3 running", func() bool { return f.runningCount(ids) == 3 })
	q.SetMaxSimultaneous(1)
	waitFor(t, 30*time.Second, "all complete", func() bool {
		started, completed := 0, 0
		for _, id := range ids {
			st := f.task(t, id).State
			if st == domain.StatePaused {
				t.Fatalf("task %s paused by SetMaxSimultaneous", id)
			}
			if st != domain.StateQueued {
				started++
			}
			if st == domain.StateCompleted {
				completed++
			}
		}
		if started >= 4 && completed < 2 {
			t.Fatalf("4th started with only %d complete", completed)
		}
		if n := f.runningCount(ids); n > 3 {
			t.Fatalf("%d running", n)
		}
		return completed == 5
	})
}

func TestQueuePauseResume(t *testing.T) {
	f := newQFixture(t)
	id := f.add(t)
	q := f.queue(t, 2, true)
	q.Start(context.Background())
	waitFor(t, 10*time.Second, "running", func() bool { return f.e.Running(id) })
	if err := q.Pause(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	f.waitState(t, 10*time.Second, domain.StatePaused, id)
	if err := q.Resume(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	f.waitState(t, 30*time.Second, domain.StateCompleted, id)
}

func TestQueuePauseQueuedIdle(t *testing.T) {
	f := newQFixture(t)
	a := f.add(t)
	q := f.queue(t, 1, true)
	q.Start(context.Background())
	waitFor(t, 10*time.Second, "a running", func() bool { return f.e.Running(a) })
	b := f.add(t)
	if err := q.Pause(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	q.Enqueue(b)
	f.waitState(t, 30*time.Second, domain.StateCompleted, a)
	if err := q.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := f.task(t, b).State; st != domain.StatePaused {
		t.Fatalf("b state %s", st)
	}
	if f.entered(b, domain.StateInspecting) {
		t.Fatal("b was started")
	}
}

func TestQueuePauseRace(t *testing.T) {
	f := newQFixture(t)
	q := f.queue(t, 2, true)
	q.Start(context.Background())
	id := f.add(t)
	q.Enqueue(id)
	if err := q.Pause(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if st := f.task(t, id).State; st != domain.StatePaused {
		t.Fatalf("state after Pause %s", st)
	}
	q.Enqueue(id)
	if err := q.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := f.task(t, id).State; st != domain.StatePaused {
		t.Fatalf("final state %s", st)
	}
}

func TestQueuePauseAllResumeAll(t *testing.T) {
	f := newQFixture(t)
	ids := addN(t, f, 4)
	q := f.queue(t, 2, true)
	q.Start(context.Background())
	waitFor(t, 10*time.Second, "2 running", func() bool { return f.runningCount(ids) == 2 })
	if err := q.PauseAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !q.Paused() {
		t.Fatal("Paused() false")
	}
	f.waitState(t, 10*time.Second, domain.StatePaused, ids...)
	late := f.add(t)
	q.Enqueue(late)
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if f.e.Running(late) || f.runningCount(ids) > 0 {
			t.Fatal("task started while paused")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if st := f.task(t, late).State; st != domain.StateQueued {
		t.Fatalf("late state %s", st)
	}
	if err := q.ResumeAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.waitState(t, 30*time.Second, domain.StateCompleted, append(ids, late)...)
}

func TestQueueRestoreResume(t *testing.T) {
	f := newQFixture(t)
	ids := addN(t, f, 3)
	f.setState(t, ids[1], domain.StatePaused)
	f.setState(t, ids[2], domain.StateDownloading)
	q := f.queue(t, 3, true)
	q.Start(context.Background())
	f.waitState(t, 30*time.Second, domain.StateCompleted, ids[0], ids[2])
	if err := q.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := f.task(t, ids[1]).State; st != domain.StatePaused || f.entered(ids[1], domain.StateInspecting) {
		t.Fatalf("paused task touched: %s", st)
	}
}

func TestQueueRestoreNoResume(t *testing.T) {
	f := newQFixture(t)
	ids := addN(t, f, 2)
	f.setState(t, ids[1], domain.StateDownloading)
	q := f.queue(t, 3, false)
	q.Start(context.Background())
	f.waitState(t, 10*time.Second, domain.StatePaused, ids[0])
	q.Enqueue("")
	if err := q.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := f.task(t, ids[1]).State; st != domain.StateDownloading {
		t.Fatalf("interrupted task state %s", st)
	}
	if f.entered(ids[0], domain.StateInspecting) {
		t.Fatal("queued task was started")
	}
}

func TestQueueShutdown(t *testing.T) {
	f := newQFixture(t)
	ids := addN(t, f, 2)
	q := f.queue(t, 2, true)
	q.Start(context.Background())
	waitFor(t, 10*time.Second, "2 running", func() bool { return f.runningCount(ids) == 2 })
	if err := q.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if st := f.task(t, id).State; st != domain.StatePaused {
			t.Fatalf("%s state %s", id, st)
		}
	}
	select {
	case <-q.loopDone:
	default:
		t.Fatal("scheduler still running")
	}
}

// fakeEngine fails the first Run of each task with ErrLockedElsewhere.
type fakeEngine struct {
	mu    sync.Mutex
	runs  map[string]int
	store *fakeStore
}

func (e *fakeEngine) Run(_ context.Context, id string) error {
	e.mu.Lock()
	e.runs[id]++
	n := e.runs[id]
	e.mu.Unlock()
	if n == 1 {
		return downloader.ErrLockedElsewhere
	}
	e.store.set(id, domain.StateCompleted)
	return nil
}
func (e *fakeEngine) runCount(id string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.runs[id]
}
func (*fakeEngine) Pause(string) bool                     { return false }
func (*fakeEngine) Hold(context.Context, string) error    { return nil }
func (*fakeEngine) Requeue(context.Context, string) error { return nil }
func (*fakeEngine) Running(string) bool                   { return false }
func (*fakeEngine) Shutdown(context.Context) error        { return nil }

type fakeStore struct {
	mu    sync.Mutex
	tasks map[string]*domain.Task
	lists int
}

func (s *fakeStore) set(id string, st domain.State) {
	s.mu.Lock()
	s.tasks[id].State = st
	s.mu.Unlock()
}
func (s *fakeStore) listCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lists
}
func (s *fakeStore) ListTasks(context.Context) ([]*domain.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lists++
	var out []*domain.Task
	for _, t := range s.tasks {
		c := *t
		out = append(out, &c)
	}
	return out, nil
}

func TestQueueLockedElsewhere(t *testing.T) {
	st := &fakeStore{tasks: map[string]*domain.Task{"t1": {ID: "t1", State: domain.StateQueued}}}
	eng := &fakeEngine{runs: map[string]int{}, store: st}
	bus := events.NewBus(10, domain.RealClock{})
	sub := bus.Subscribe()
	defer sub.Close()
	clk := clocktest.New(time.Unix(1_000_000, 0))
	q := NewQueue(QueueDeps{Engine: eng, Store: st, Bus: bus, Clock: clk, MaxSimultaneous: 2, ResumeOnStartup: true})
	q.Start(context.Background())
	defer func() { _ = q.Shutdown(context.Background()) }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		ev, err := sub.Next(ctx)
		if err != nil {
			t.Fatal("no warning:", err)
		}
		if w, ok := ev.(events.Warning); ok && w.ID == "t1" {
			if w.Msg != "running in another SwiftFetch process" {
				t.Fatalf("msg %q", w.Msg)
			}
			break
		}
	}
	// A tick within the 30 s window rescans but must not retry t1.
	waitFor(t, 5*time.Second, "tick waiter", func() bool { return clk.Waiters() > 0 })
	before := st.listCount()
	clk.Advance(29 * time.Second)
	waitFor(t, 5*time.Second, "rescan", func() bool { return st.listCount() > before })
	waitFor(t, 5*time.Second, "tick waiter", func() bool { return clk.Waiters() > 0 })
	if n := eng.runCount("t1"); n != 1 {
		t.Fatalf("runs within skip window: %d", n)
	}
	clk.Advance(5 * time.Second)
	waitFor(t, 5*time.Second, "retry", func() bool { return eng.runCount("t1") == 2 })
}
