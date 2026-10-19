// Command goti is the Goti desktop GUI (spec §11).
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
	"time"

	gioapp "gioui.org/app"
	"gioui.org/io/system"

	"github.com/raptordnj/goti/internal/app"
	"github.com/raptordnj/goti/internal/config"
	"github.com/raptordnj/goti/internal/domain"
	"github.com/raptordnj/goti/internal/downloader"
	"github.com/raptordnj/goti/internal/events"
	"github.com/raptordnj/goti/internal/mobile"
	"github.com/raptordnj/goti/internal/persistence"
	"github.com/raptordnj/goti/internal/platform"
	"github.com/raptordnj/goti/internal/ratelimit"
	"github.com/raptordnj/goti/internal/services"
	"github.com/raptordnj/goti/internal/settings"
	"github.com/raptordnj/goti/internal/storage"
	"github.com/raptordnj/goti/internal/tray"
	"github.com/raptordnj/goti/internal/ui"
	"github.com/raptordnj/goti/internal/ui/state"
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

	dataDir string

	ctx    context.Context
	cancel context.CancelFunc
	// invalidate is the current window's Invalidate (nil = no window).
	invalidate atomic.Pointer[func()]
	// trayOK is set once the system tray is up.
	trayOK atomic.Bool

	// onAdd runs after each Add submit (Android: notification permission once).
	onAdd func()

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
	db, err := persistence.Open(config.DBPath(cfg.DataDir))
	if err != nil {
		_ = lock.Unlock()
		return nil, fmt.Errorf("cannot open database: %w", err)
	}
	repo := settings.NewRepo(db, log)
	s := repo.Load(context.Background())
	if err := ensureDownloadDir(platform.Mobile, s.DownloadDir); err != nil {
		log.Warn("cannot create download directory", "dir", s.DownloadDir, "err", err)
	}
	clock := domain.RealClock{}
	bus := events.NewBus(cfg.ProgressHz, clock)
	bw := ratelimit.NewBandwidth(s.GlobalRateLimit)
	eng := downloader.NewEngine(downloader.EngineDeps{
		Store: db, Config: s.Apply(cfg), Bus: bus, Clock: clock, Logger: log,
		Global: bw, Conns: ratelimit.NewSemaphore(cfg.GlobalMaxConnections),
	})
	q := services.NewQueue(services.QueueDeps{Engine: eng, Store: db, KV: db, Bus: bus, Clock: clock, Logger: log,
		MaxSimultaneous: s.MaxSimultaneous, ResumeOnStartup: s.ResumeOnStartup})
	a := app.New(app.Deps{Engine: eng, Queue: q, Settings: repo, Bandwidth: bw, Bus: bus, Logger: log, BaseConfig: cfg})
	ctx, cancel := context.WithCancel(context.Background())
	return &runtime{app: a, store: &state.Store{}, db: db, lock: lock, queue: q, engine: eng, bus: bus, log: log,
		ctx: ctx, cancel: cancel, dataDir: cfg.DataDir}, nil
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
	r.app.Send(app.MarkTrayNoticeShown{})
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
	started := time.Now()
	go func() {
		err := ui.RunWindow(ui.WindowDeps{App: r.app, Store: r.store, Window: w, KeepAlive: func(closeToTray bool) bool { return keepAlive(platform.Mobile, closeToTray, r.trayOK.Load()) },
			TakeSharedURL: mobile.DefaultInbox.TakeSharedURL, OnAddSubmit: r.onAdd,
			Logger: r.log, Version: version, DataDir: r.dataDir, OnClosed: func() {
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
		if platform.Mobile {
			// The process outlives the activity: leave a pending window for
			// the next activity, unless shutting down or respawning too fast.
			delay, ok := respawnPolicy(r.ctx.Err(), time.Since(started))
			if ok {
				if delay > 0 {
					time.Sleep(delay)
				}
				if r.ctx.Err() == nil {
					r.showWindow()
				}
			}
		}
	}()
}

// close releases the DB and the instance lock.
func (r *runtime) close() error {
	r.cancel()
	return errors.Join(r.db.Close(), r.lock.Unlock())
}

// version is the build version, shown in Settings > About; release builds
// set it with -ldflags "-X main.version=...".
var version = "dev"

// appID is the reverse-DNS application ID; Gio sets it as the Wayland app_id
// so the desktop entry (io.github.raptordnj.Goti) matches the window.
const appID = "io.github.raptordnj.Goti"

// dataDirFor resolves the data directory: --data-dir, then GOTI_DATA_DIR,
// the deprecated legacy env var, and the default (migrating the legacy
// data dir). It runs before the DB is opened or any lock is taken.
func dataDirFor(flagDir string, env map[string]string, log *slog.Logger) string {
	if flagDir != "" {
		return flagDir
	}
	if log == nil {
		log = slog.Default()
	}
	return config.ResolveDataDir(env, log)
}

// keepAlive reports whether closing the window must not quit the app: always
// on mobile, and on desktop when a tray can bring the window back.
func keepAlive(mobile, closeToTray, trayAvailable bool) bool {
	return mobile || (closeToTray && trayAvailable)
}

// respawnPolicy decides whether to re-create the window on mobile and how
// long to wait first: never during shutdown, and back off 1s when the
// previous window lived under a second.
func respawnPolicy(ctxErr error, lived time.Duration) (delay time.Duration, ok bool) {
	if ctxErr != nil {
		return 0, false
	}
	if lived < time.Second {
		return time.Second, true
	}
	return 0, true
}

// ensureDownloadDir creates the download directory on mobile, where the
// default (under the app's private files dir) does not exist on first run.
func ensureDownloadDir(mobile bool, dir string) error {
	if !mobile || dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0o700)
}

// androidDataDir is the data directory under Gio's per-app files directory.
func androidDataDir(gioDataDir string) string { return filepath.Join(gioDataDir, "goti") }

// startupMessage is the text shown when the app cannot start.
func startupMessage(err error) string {
	if errors.Is(err, platform.ErrAlreadyRunning) {
		return platform.ErrAlreadyRunning.Error()
	}
	return "goti: " + err.Error()
}

// forwardSignals sends Quit for each signal until sigs is closed.
func forwardSignals(sigs <-chan os.Signal, send func(app.Command) bool, log *slog.Logger) {
	for range sigs {
		ui.SendQuit(send, log)
	}
}

func main() {
	flagDir := flag.String("data-dir", "", "data directory (env "+config.EnvDataDir+")")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("goti", version)
		return
	}
	gioapp.ID = appID
	env := map[string]string{}
	for _, k := range []string{config.EnvDataDir, config.LegacyEnvDataDir} {
		env[k] = os.Getenv(k)
	}
	var dataDir string
	if platform.Mobile {
		gd, err := gioapp.DataDir()
		if err != nil {
			fmt.Fprintln(os.Stderr, "goti: "+err.Error())
			ui.ShowFatal("goti: " + err.Error())
			os.Exit(1)
		}
		dataDir = androidDataDir(gd)
	} else {
		dataDir = dataDirFor(*flagDir, env, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	}
	rt, err := build(dataDir)
	if err != nil {
		msg := startupMessage(err)
		fmt.Fprintln(os.Stderr, msg)
		// GUI builds have no console on Windows/macOS: show it in a window too.
		ui.ShowFatal(msg) // exits 1 once closed
	}

	if !platform.Mobile {
		sigs := make(chan os.Signal, 2)
		signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
		go forwardSignals(sigs, rt.app.Send, rt.log)
	}

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
	if !platform.Mobile {
		go func() {
			ok, stop := tray.Start(tray.Deps{Send: rt.app.Send, Snapshot: rt.store.Load, Show: rt.showWindow})
			rt.log.Info("tray", "available", ok)
			rt.trayOK.Store(ok)
			trayStop <- stop
		}()
	}
	if platform.Mobile {
		rt.onAdd = mobile.Start(mobile.Deps{Ctx: rt.ctx, Load: rt.store.Load, KV: rt.db, Send: rt.app.Send,
			Invalidate: rt.invalidateWindow, Log: rt.log})
	}
	rt.showWindow()
	gioapp.Main()
}
