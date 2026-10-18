// Command swiftfetch is the SwiftFetch desktop GUI (spec §11).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"

	gioapp "gioui.org/app"

	"github.com/raptordnj/swiftfetch/internal/app"
	"github.com/raptordnj/swiftfetch/internal/config"
	"github.com/raptordnj/swiftfetch/internal/domain"
	"github.com/raptordnj/swiftfetch/internal/downloader"
	"github.com/raptordnj/swiftfetch/internal/events"
	"github.com/raptordnj/swiftfetch/internal/persistence"
	"github.com/raptordnj/swiftfetch/internal/platform"
	"github.com/raptordnj/swiftfetch/internal/ratelimit"
	"github.com/raptordnj/swiftfetch/internal/services"
	"github.com/raptordnj/swiftfetch/internal/settings"
	"github.com/raptordnj/swiftfetch/internal/storage"
	"github.com/raptordnj/swiftfetch/internal/ui"
	"github.com/raptordnj/swiftfetch/internal/ui/state"
)

// runtime is the wired application minus window and tray.
type runtime struct {
	app    *app.App
	store  *state.Store
	db     *persistence.DB
	lock   *storage.FileLock
	queue  *services.Queue
	engine *downloader.Engine
	bus    *events.Bus
	log    *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc
	// invalidate is the current window's Invalidate (nil = no window).
	invalidate atomic.Pointer[func()]
}

// build wires DB, settings, bus, engine, queue, app and state for dataDir,
// holding the single-instance lock.
func build(dataDir string) (*runtime, error) {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg := config.Default()
	if dataDir != "" {
		cfg.DataDir = dataDir
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("cannot create data directory: %w", err)
	}
	lock, err := platform.InstanceLock(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	db, err := persistence.Open(filepath.Join(cfg.DataDir, "swiftfetch.db"))
	if err != nil {
		_ = lock.Unlock()
		return nil, fmt.Errorf("cannot open database: %w", err)
	}
	repo := settings.NewRepo(db, log)
	s := repo.Load(context.Background())
	clock := domain.RealClock{}
	bus := events.NewBus(cfg.ProgressHz, clock)
	bw := ratelimit.NewBandwidth(s.GlobalRateLimit)
	eng := downloader.NewEngine(downloader.EngineDeps{
		Store: db, Config: s.Apply(cfg), Bus: bus, Clock: clock, Logger: log,
		Global: bw, Conns: ratelimit.NewSemaphore(cfg.GlobalMaxConnections),
	})
	q := services.NewQueue(services.QueueDeps{Engine: eng, Store: db, Bus: bus, Clock: clock, Logger: log,
		MaxSimultaneous: s.MaxSimultaneous, ResumeOnStartup: s.ResumeOnStartup})
	a := app.New(app.Deps{Engine: eng, Queue: q, Settings: repo, Bandwidth: bw, Bus: bus, Logger: log, BaseConfig: cfg})
	ctx, cancel := context.WithCancel(context.Background())
	return &runtime{app: a, store: &state.Store{}, db: db, lock: lock, queue: q, engine: eng, bus: bus, log: log,
		ctx: ctx, cancel: cancel}, nil
}

func (r *runtime) invalidateWindow() {
	if f := r.invalidate.Load(); f != nil {
		(*f)()
	}
}

// run starts the queue and state goroutine, then processes commands until
// Quit; it releases everything before returning.
func (r *runtime) run() error {
	r.queue.Start(r.ctx)
	stateDone := make(chan struct{})
	go func() {
		defer close(stateDone)
		state.Run(r.ctx, state.Deps{Bus: r.bus, Store: r.db, Running: r.engine.Running,
			QueuePaused: r.queue.Paused, Settings: r.app.Settings, Invalidate: r.invalidateWindow}, r.store)
	}()
	err := r.app.Run(r.ctx)
	if errors.Is(err, context.Canceled) {
		err = nil
	}
	r.cancel()
	<-stateDone
	return errors.Join(err, r.close())
}

// close releases the DB and the instance lock.
func (r *runtime) close() error {
	r.cancel()
	return errors.Join(r.db.Close(), r.lock.Unlock())
}

func main() {
	dataDir := flag.String("data-dir", os.Getenv("SWIFTFETCH_DATA_DIR"), "data directory (env SWIFTFETCH_DATA_DIR)")
	flag.Parse()
	rt, err := build(*dataDir)
	if errors.Is(err, platform.ErrAlreadyRunning) {
		fmt.Fprintln(os.Stderr, "SwiftFetch is already running")
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "swiftfetch:", err)
		os.Exit(1)
	}

	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() {
		for range sigs {
			rt.app.Send(app.Quit{})
		}
	}()

	go func() {
		code := 0
		if err := rt.run(); err != nil {
			rt.log.Error("shutdown", "err", err)
			code = 1
		}
		os.Exit(code)
	}()

	trayAvailable := func() bool { return false } // tray arrives in Task 10
	w := new(gioapp.Window)
	inv := w.Invalidate
	rt.invalidate.Store(&inv)
	go func() {
		if err := ui.RunWindow(ui.WindowDeps{App: rt.app, Store: rt.store, Window: w, TrayAvailable: trayAvailable,
			Logger: rt.log, OnClosed: func() { rt.invalidate.Store(nil) }}); err != nil {
			rt.log.Error("window", "err", err)
		}
	}()
	gioapp.Main()
}
