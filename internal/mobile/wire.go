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
	pending    []string // actions received before Bind, oldest first
	adds       []string // AddNow URLs received before Bind, oldest first
}

// maxPending caps actions queued before the app is bound (newest kept).
const maxPending = 8

// DefaultInbox is the inbox the JNI exports deliver to.
var DefaultInbox = &Inbox{}

// Bind sets the command sink, the window wake-up and the logger (nil = keep).
// Actions dispatched while unbound are delivered in order on the first Bind
// with a send.
func (i *Inbox) Bind(send func(app.Command) bool, invalidate func(), log *slog.Logger) {
	i.mu.Lock()
	var flush, adds []string
	if send != nil {
		i.send = send
		flush, i.pending = i.pending, nil
		adds, i.adds = i.adds, nil
	}
	if invalidate != nil {
		i.invalidate = invalidate
	}
	if log != nil {
		i.log = log
	}
	i.mu.Unlock()
	for _, name := range flush {
		i.Action(name, send)
	}
	for _, u := range adds {
		i.add(u, send)
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

// Dispatch runs Action with the bound command sink, or queues the action
// until Bind when the app is not bound yet (e.g. a notification tap that
// restarted the process before Go main wired the app).
func (i *Inbox) Dispatch(name string) {
	i.mu.Lock()
	send := i.send
	if send == nil {
		i.pending = append(i.pending, name)
		if len(i.pending) > maxPending {
			i.pending = i.pending[len(i.pending)-maxPending:]
		}
		i.mu.Unlock()
		return
	}
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

// AddNow adds u as a download immediately (no Add sheet), using the default
// download folder. Used by the debug-only add intent of the smoke test.
// URLs received before Bind are queued (newest maxPending kept).
func (i *Inbox) AddNow(u string) {
	if u == "" {
		return
	}
	i.mu.Lock()
	send := i.send
	if send == nil {
		i.adds = append(i.adds, u)
		if len(i.adds) > maxPending {
			i.adds = i.adds[len(i.adds)-maxPending:]
		}
		i.mu.Unlock()
		return
	}
	i.mu.Unlock()
	i.add(u, send)
}

func (i *Inbox) add(u string, send func(app.Command) bool) {
	if !send(app.AddDownload{URL: u}) {
		i.logger().Warn("add not delivered", "url", u)
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
