package app

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"sync"
	"time"

	"github.com/raptordnj/goti/internal/config"
	"github.com/raptordnj/goti/internal/downloader"
	"github.com/raptordnj/goti/internal/events"
	"github.com/raptordnj/goti/internal/ratelimit"
	"github.com/raptordnj/goti/internal/services"
	"github.com/raptordnj/goti/internal/settings"
)

const (
	cmdBuffer       = 64
	commandTimeout  = 30 * time.Second
	shutdownTimeout = 10 * time.Second
)

// Deps are the App's collaborators.
type Deps struct {
	Engine     *downloader.Engine
	Queue      *services.Queue
	Settings   *settings.Repo
	Bandwidth  *ratelimit.Bandwidth
	Bus        *events.Bus
	Logger     *slog.Logger
	BaseConfig config.Config
}

// App executes commands one at a time.
type App struct {
	d    Deps
	cmds chan Command

	mu  sync.Mutex
	cur settings.Settings

	hook func(Command) // test hook, runs before dispatch
}

// New builds an App and loads the current settings once.
func New(d Deps) *App {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	return &App{d: d, cmds: make(chan Command, cmdBuffer), cur: d.Settings.Load(context.Background())}
}

// Send enqueues c without blocking; false means the buffer is full.
func (a *App) Send(c Command) bool {
	select {
	case a.cmds <- c:
		return true
	default:
		return false
	}
}

// Settings returns the cached current settings.
func (a *App) Settings() settings.Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cur
}

// Run processes commands until Quit (nil) or ctx is done.
func (a *App) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			a.shutdown()
			return ctx.Err()
		case c := <-a.cmds:
			if _, quit := c.(Quit); quit {
				err := a.shutdown()
				a.publish(c, "", err)
				return nil
			}
			a.handle(c)
		}
	}
}

func (a *App) shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return a.d.Queue.Shutdown(ctx)
}

func (a *App) publish(c Command, id string, err error) {
	a.d.Bus.Publish(events.CommandResult{Cmd: reflect.TypeOf(c).Name(), ID: id, Err: err})
}

func (a *App) handle(c Command) {
	var id string
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("command panicked: %v", r)
				a.d.Logger.Error("command panic", "cmd", reflect.TypeOf(c).Name(), "panic", r)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		if a.hook != nil {
			a.hook(c)
		}
		id, err = a.exec(ctx, c)
	}()
	a.publish(c, id, err)
}

func (a *App) exec(ctx context.Context, c Command) (string, error) {
	q, e := a.d.Queue, a.d.Engine
	switch c := c.(type) {
	case AddDownload:
		dir := c.DestDir
		if dir == "" {
			dir = a.Settings().DownloadDir
		}
		t, err := e.Add(ctx, downloader.AddRequest{URL: c.URL, DestDir: dir, FileName: c.FileName})
		if err != nil {
			return "", err
		}
		q.Enqueue(t.ID)
		return t.ID, nil
	case PauseTask:
		return c.ID, q.Pause(ctx, c.ID)
	case ResumeTask:
		return c.ID, q.Resume(ctx, c.ID)
	case RetryTask:
		return c.ID, q.Resume(ctx, c.ID)
	case RestartTask:
		if err := e.AllowRestart(ctx, c.ID); err != nil {
			return c.ID, err
		}
		return c.ID, q.Resume(ctx, c.ID)
	case CancelTask:
		err := e.Cancel(ctx, c.ID)
		q.Enqueue(c.ID)
		return c.ID, err
	case RemoveTask:
		err := e.Remove(ctx, c.ID, c.DeletePartial)
		q.Enqueue(c.ID)
		return c.ID, err
	case PauseAll:
		return "", q.PauseAll(ctx)
	case ResumeAll:
		return "", q.ResumeAll(ctx)
	case SaveSettings:
		return "", a.save(ctx, c.S)
	case MarkTrayNoticeShown:
		s := a.Settings()
		s.TrayNoticeShown = true
		return "", a.save(ctx, s)
	}
	return "", fmt.Errorf("unknown command %T", c)
}

func (a *App) save(ctx context.Context, s settings.Settings) error {
	if err := s.Validate(); err != nil {
		return err
	}
	if err := a.d.Settings.Save(ctx, s); err != nil {
		return err
	}
	a.mu.Lock()
	a.cur = s
	a.mu.Unlock()
	a.d.Bandwidth.SetLimit(s.GlobalRateLimit)
	a.d.Queue.SetMaxSimultaneous(s.MaxSimultaneous)
	a.d.Engine.SetConfig(s.Apply(a.d.BaseConfig))
	return nil
}
