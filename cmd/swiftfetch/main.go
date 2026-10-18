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
	"sync"
	"sync/atomic"
	"syscall"

	gioapp "gioui.org/app"
	"gioui.org/io/system"

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
	"github.com/raptordnj/swiftfetch/internal/tray"
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
	// trayOK is set once the system tray is up.
	trayOK atomic.Bool

	winMu sync.Mutex
	win   *gioapp.Window // the open window, nil when closed to tray
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
			QueuePaused: r.queue.Paused, Settings: r.app.Settings, Invalidate: r.invalidateWindow,
			TrayAvailable: r.trayOK.Load, MarkTrayNotice: r.markTrayNotice}, r.store)
	}()
	err := r.app.Run(r.ctx)
	if errors.Is(err, context.Canceled) {
		err = nil
	}
	r.cancel()
	<-stateDone
	return errors.Join(err, r.close())
}

// markTrayNotice persists TrayNoticeShown so the tray notice shows once.
func (r *runtime) markTrayNotice() {
	s := r.app.Settings()
	s.TrayNoticeShown = true
	r.app.Send(app.SaveSettings{S: s})
}

// showWindow opens the main window, or raises it when one is already open;
// at most one window exists at a time.
func (r *runtime) showWindow() {
	r.winMu.Lock()
	defer r.winMu.Unlock()
	if r.win != nil {
		r.win.Perform(system.ActionRaise)
		return
	}
	w := new(gioapp.Window)
	r.win = w
	inv := w.Invalidate
	r.invalidate.Store(&inv)
	go func() {
		err := ui.RunWindow(ui.WindowDeps{App: r.app, Store: r.store, Window: w, TrayAvailable: r.trayOK.Load,
			Logger: r.log, OnClosed: func() {
				r.winMu.Lock()
				if r.win == w {
					r.win = nil
					r.invalidate.Store(nil)
				}
				r.winMu.Unlock()
			}})
		if err != nil {
			r.log.Error("window", "err", err)
		}
	}()
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

	trayStop := make(chan func(), 1)
	go func() {
		code := 0
		if err := rt.run(); err != nil {
			rt.log.Error("shutdown", "err", err)
			code = 1
		}
		select {
		case stop := <-trayStop:
			stop()
		default:
		}
		os.Exit(code)
	}()

	// The tray comes up in the background (≤ 2 s) so the window is not
	// delayed; until it is ready, closing the window quits.
	go func() {
		ok, stop := tray.Start(tray.Deps{Send: rt.app.Send, Snapshot: rt.store.Load, Show: rt.showWindow})
		rt.log.Info("tray", "available", ok)
		rt.trayOK.Store(ok)
		trayStop <- stop
	}()
	rt.showWindow()
	gioapp.Main()
}
