package app

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/raptordnj/swiftfetch/internal/clocktest"
	"github.com/raptordnj/swiftfetch/internal/config"
	"github.com/raptordnj/swiftfetch/internal/domain"
	"github.com/raptordnj/swiftfetch/internal/downloader"
	"github.com/raptordnj/swiftfetch/internal/events"
	"github.com/raptordnj/swiftfetch/internal/persistence"
	"github.com/raptordnj/swiftfetch/internal/ratelimit"
	"github.com/raptordnj/swiftfetch/internal/services"
	"github.com/raptordnj/swiftfetch/internal/settings"
	"github.com/raptordnj/swiftfetch/internal/transport"
	"github.com/raptordnj/swiftfetch/tests/faultserver"
)

type fx struct {
	app *App
	db  *persistence.DB
	srv *faultserver.Server
	bw  *ratelimit.Bandwidth
	sub *events.Subscription
	dir string
	q   *services.Queue
}

func newFx(t *testing.T, bps int) *fx {
	t.Helper()
	f := &fx{srv: faultserver.New(t, faultserver.Options{Size: 1 << 20, Seed: 3, ETag: "v1", BytesPerSecondGlobal: int64(bps)})}
	db, err := persistence.Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	f.db = db
	cfg := config.Default()
	cfg.DownloadDir, cfg.DataDir = t.TempDir(), t.TempDir()
	cfg.RetryBaseDelay, cfg.RetryMaxDelay = 5*time.Millisecond, 20*time.Millisecond
	cfg.CheckpointInterval = 100 * time.Millisecond
	cfg.ProgressHz = 20
	f.dir = cfg.DownloadDir
	bus := events.NewBus(cfg.ProgressHz, domain.RealClock{})
	f.sub = bus.Subscribe()
	f.bw = ratelimit.NewBandwidth(0)
	e := downloader.NewEngine(downloader.EngineDeps{
		Store: db, Config: cfg, Bus: bus, Clock: domain.RealClock{},
		Global: f.bw, Conns: ratelimit.NewSemaphore(cfg.GlobalMaxConnections),
		NewClient: func(p bool) *http.Client {
			return transport.NewClient(transport.ClientOptions{Policy: cfg.NetworkPolicy, AllowPrivate: p, Dialer: f.srv.Dialer()})
		},
	})
	f.q = services.NewQueue(services.QueueDeps{Engine: e, Store: db, Bus: bus, Clock: clocktest.New(time.Unix(1e6, 0)), MaxSimultaneous: 2, ResumeOnStartup: true})
	repo := settings.NewRepo(db, nil)
	s := settings.Default()
	s.DownloadDir = cfg.DownloadDir
	if err := repo.Save(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	f.app = New(Deps{Engine: e, Queue: f.q, Settings: repo, Bandwidth: f.bw, Bus: bus, BaseConfig: cfg})
	f.q.Start(context.Background())
	t.Cleanup(func() { _ = f.q.Shutdown(context.Background()) })
	return f
}

func (f *fx) run(t *testing.T) chan error {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.app.Run(ctx) }()
	t.Cleanup(cancel)
	return done
}

// next returns the next event matching pred.
func (f *fx) next(t *testing.T, pred func(events.Event) bool) events.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for {
		ev, err := f.sub.Next(ctx)
		if err != nil {
			t.Fatal("waiting for event:", err)
		}
		if pred(ev) {
			return ev
		}
	}
}

func result(cmd string) func(events.Event) bool {
	return func(ev events.Event) bool { r, ok := ev.(events.CommandResult); return ok && r.Cmd == cmd }
}

func TestAppAddCompletes(t *testing.T) {
	f := newFx(t, 0)
	f.run(t)
	f.app.Send(AddDownload{URL: f.srv.URL + "/file"})
	r := f.next(t, result("AddDownload")).(events.CommandResult)
	if r.Err != nil || r.ID == "" {
		t.Fatalf("%#v", r)
	}
	f.next(t, func(ev events.Event) bool {
		sc, ok := ev.(events.StateChanged)
		return ok && sc.ID == r.ID && sc.To == domain.StateCompleted
	})
	if m, _ := filepath.Glob(filepath.Join(f.dir, "*")); len(m) == 0 {
		t.Fatal("file not in settings download dir")
	}
}

func TestAppSendFull(t *testing.T) {
	f := newFx(t, 0)
	for i := 0; i < 64; i++ {
		if !f.app.Send(PauseAll{}) {
			t.Fatalf("send %d refused", i+1)
		}
	}
	if f.app.Send(PauseAll{}) {
		t.Fatal("65th send accepted")
	}
}

func TestAppRemoveDeletesPartial(t *testing.T) {
	f := newFx(t, 200<<10)
	f.run(t)
	f.app.Send(AddDownload{URL: f.srv.URL + "/file"})
	id := f.next(t, result("AddDownload")).(events.CommandResult).ID
	f.next(t, func(ev events.Event) bool {
		sc, ok := ev.(events.StateChanged)
		return ok && sc.ID == id && sc.To == domain.StateDownloading
	})
	time.Sleep(300 * time.Millisecond)
	f.app.Send(PauseTask{ID: id})
	f.next(t, result("PauseTask"))
	tk, _ := f.db.GetTask(context.Background(), id)
	if _, err := os.Stat(tk.TempPath); err != nil {
		t.Fatal("expected partial file:", err)
	}
	f.app.Send(RemoveTask{ID: id, DeletePartial: true})
	if r := f.next(t, result("RemoveTask")).(events.CommandResult); r.Err != nil {
		t.Fatal(r.Err)
	}
	if _, err := os.Stat(tk.TempPath); !os.IsNotExist(err) {
		t.Fatalf("partial remains: %v", err)
	}
	if _, err := f.db.GetTask(context.Background(), id); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("task remains: %v", err)
	}
}

func TestAppSaveSettingsLive(t *testing.T) {
	f := newFx(t, 0)
	f.run(t)
	s := f.app.Settings()
	s.GlobalRateLimit, s.MaxSimultaneous = 123456, 5
	f.app.Send(SaveSettings{S: s})
	if r := f.next(t, result("SaveSettings")).(events.CommandResult); r.Err != nil {
		t.Fatal(r.Err)
	}
	if f.bw.Limit() != 123456 || f.app.Settings().MaxSimultaneous != 5 {
		t.Fatalf("not applied: %d %+v", f.bw.Limit(), f.app.Settings())
	}
	bad := s
	bad.MaxConnections = 99
	f.app.Send(SaveSettings{S: bad})
	r := f.next(t, result("SaveSettings")).(events.CommandResult)
	var fe config.FieldError
	if !errors.As(r.Err, &fe) {
		t.Fatalf("want FieldError, got %v", r.Err)
	}
	if f.app.Settings().MaxConnections == 99 {
		t.Fatal("invalid settings cached")
	}
}

func TestAppQuit(t *testing.T) {
	f := newFx(t, 200<<10)
	done := f.run(t)
	f.app.Send(AddDownload{URL: f.srv.URL + "/file"})
	id := f.next(t, result("AddDownload")).(events.CommandResult).ID
	f.next(t, func(ev events.Event) bool {
		sc, ok := ev.(events.StateChanged)
		return ok && sc.ID == id && sc.To == domain.StateDownloading
	})
	f.app.Send(Quit{})
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Run did not return")
	}
	tk, _ := f.db.GetTask(context.Background(), id)
	if tk.State != domain.StatePaused {
		t.Fatalf("state %s", tk.State)
	}
}

func TestAppCommandPanicRecovered(t *testing.T) {
	f := newFx(t, 0)
	f.app.hook = func(c Command) {
		if p, ok := c.(PauseTask); ok && p.ID == "boom" {
			panic("boom")
		}
	}
	f.run(t)
	f.app.Send(PauseTask{ID: "boom"})
	if r := f.next(t, result("PauseTask")).(events.CommandResult); r.Err == nil {
		t.Fatal("want error")
	}
	f.app.Send(PauseAll{})
	if r := f.next(t, result("PauseAll")).(events.CommandResult); r.Err != nil {
		t.Fatal(r.Err)
	}
}
