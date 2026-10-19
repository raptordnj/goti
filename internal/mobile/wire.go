package mobile

import (
	"context"
	"errors"
	"log/slog"
	"mime"
	"path/filepath"
	"sync"
	"time"

	"github.com/raptordnj/goti/internal/app"
	"github.com/raptordnj/goti/internal/ui/state"
)

// Inbox carries calls from the Android host (JNI natives) into the app:
// notification actions and shared URLs.
type Inbox struct {
	mu         sync.Mutex
	url        string
	send       func(app.Command) bool
	invalidate func()
	log        *slog.Logger
}

// DefaultInbox is the inbox the JNI exports deliver to.
var DefaultInbox = &Inbox{}

// Bind sets the command sink, the window wake-up and the logger (nil = keep).
func (i *Inbox) Bind(send func(app.Command) bool, invalidate func(), log *slog.Logger) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if send != nil {
		i.send = send
	}
	if invalidate != nil {
		i.invalidate = invalidate
	}
	if log != nil {
		i.log = log
	}
}

func (i *Inbox) logger() *slog.Logger {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.log == nil {
		return slog.Default()
	}
	return i.log
}

// Action maps a notification action name to a command and sends it;
// unknown names are logged and ignored.
func (i *Inbox) Action(name string, send func(app.Command) bool) {
	var cmd app.Command
	switch name {
	case "pause_all":
		cmd = app.PauseAll{}
	case "resume_all":
		cmd = app.ResumeAll{}
	default:
		i.logger().Warn("unknown notification action", "action", name)
		return
	}
	if send == nil || !send(cmd) {
		i.logger().Warn("notification action not delivered", "action", name)
	}
}

// Dispatch runs Action with the bound command sink.
func (i *Inbox) Dispatch(name string) {
	i.mu.Lock()
	send := i.send
	i.mu.Unlock()
	i.Action(name, send)
}

// SetSharedURL stores a shared URL for the window and wakes it.
func (i *Inbox) SetSharedURL(u string) {
	i.mu.Lock()
	i.url = u
	inv := i.invalidate
	i.mu.Unlock()
	if inv != nil {
		inv()
	}
}

// TakeSharedURL returns the pending shared URL once, then "".
func (i *Inbox) TakeSharedURL() string {
	i.mu.Lock()
	defer i.mu.Unlock()
	u := i.url
	i.url = ""
	return u
}

// Once wraps f so it runs at most once per process.
func Once(f func()) func() {
	var o sync.Once
	return func() { o.Do(f) }
}

// Deps wire the Android integration into a running app.
type Deps struct {
	Ctx        context.Context
	Load       func() *state.Snapshot
	KV         KV
	Send       func(app.Command) bool
	Invalidate func() // current window's Invalidate (may be a no-op)
	Log        *slog.Logger
}

// Start wires the platform host (no-op off Android): binds DefaultInbox,
// installs platform.AndroidOpen/AndroidShare and starts the service loop.
// It returns the "first Add" hook (requests notification permission once),
// or nil when there is no host.
func Start(d Deps) func() {
	h := platformHost(d.Log)
	if h == nil {
		return nil
	}
	return startWith(d, h)
}

func startWith(d Deps, h uriHost) func() {
	DefaultInbox.Bind(d.Send, d.Invalidate, d.Log)
	viaURI := func(f func(uri, mime string) bool) func(string) error {
		return func(p string) error {
			uri := URIFor(d.Ctx, d.KV, p)
			if uri == "" {
				return errors.ErrUnsupported
			}
			if !f(uri, mime.TypeByExtension(filepath.Ext(p))) {
				return errors.New("no app can handle this file")
			}
			return nil
		}
	}
	installOpeners(viaURI(h.OpenURI), viaURI(h.ShareURI))
	go Run(d.Ctx, d.Load, h, NewPublisher(h, d.KV, d.Log), time.Second)
	return Once(h.RequestNotificationPermission)
}

// uriHost is a Host that can also open and share content URIs.
type uriHost interface {
	Host
	OpenURI(uri, mime string) bool
	ShareURI(uri, mime string) bool
}
