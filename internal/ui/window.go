package ui

import (
	"errors"
	"io"
	"log/slog"
	"strings"
	"time"

	gioapp "gioui.org/app"
	"gioui.org/io/clipboard"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/transfer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/x/explorer"

	"github.com/raptordnj/swiftfetch/internal/app"
	"github.com/raptordnj/swiftfetch/internal/platform"
	"github.com/raptordnj/swiftfetch/internal/settings"
	"github.com/raptordnj/swiftfetch/internal/ui/dialogs"
	"github.com/raptordnj/swiftfetch/internal/ui/screens"
	"github.com/raptordnj/swiftfetch/internal/ui/state"
	"github.com/raptordnj/swiftfetch/internal/ui/theme"
)

const (
	busyText      = "Busy, try again"
	localToastTTL = 5 * time.Second
	textMIME      = "application/text"
)

// FolderPicker is the native folder chooser (gioui.org/x/explorer).
type FolderPicker interface {
	ListenEvents(event.Event)
	ChooseFolder() (string, error)
}

// WindowDeps are the collaborators of one window.
type WindowDeps struct {
	App   *app.App
	Store *state.Store
	// Explorer builds the folder picker for w; nil uses gioui.org/x/explorer.
	Explorer      func(w *gioapp.Window) FolderPicker
	TrayAvailable func() bool
	// OnClosed runs after the window is gone (nil = no-op).
	OnClosed func()
	// Window, when set, is used instead of a new *app.Window (its
	// Invalidate is also what state.Deps.Invalidate should call).
	Window *gioapp.Window
	Logger *slog.Logger
}

type dialogKind int

const (
	dialogNone dialogKind = iota
	dialogAdd
	dialogConfirm
)

type toastKey struct {
	text string
	at   time.Time
}

// controller is the window's UI state and intent handling, independent of
// the *app.Window so it can be tested.
type controller struct {
	send       func(app.Command) bool
	invalidate func()
	picker     FolderPicker
	log        *slog.Logger

	main     screens.Main
	settings screens.Settings
	add      dialogs.Add
	confirm  dialogs.Confirm

	dialog       dialogKind
	settingsOpen bool
	preview      settings.Theme // live theme while settings are open
	saved        settings.Theme // saved but not yet in the snapshot

	addPending bool
	addBase    uint64
	lastAddErr string

	local     []state.Toast
	dismissed map[toastKey]bool
	picked    chan string

	clipTag  int // address is the clipboard read tag
	wantClip bool
	clipReq  bool
	pickBusy bool
}

func newController(send func(app.Command) bool, invalidate func()) *controller {
	return &controller{send: send, invalidate: invalidate, log: slog.Default(),
		dismissed: map[toastKey]bool{}, picked: make(chan string, 1)}
}

// toasts merges live snapshot toasts (minus dismissed ones) with local ones.
func (c *controller) toasts(snap *state.Snapshot, now time.Time) []state.Toast {
	var out []state.Toast
	live := map[toastKey]bool{}
	for _, t := range snap.Toasts {
		k := toastKey{t.Text, t.At}
		live[k] = true
		if !c.dismissed[k] {
			out = append(out, t)
		}
	}
	for k := range c.dismissed { // forget expired entries
		if !live[k] {
			delete(c.dismissed, k)
		}
	}
	kept := c.local[:0]
	for _, t := range c.local {
		if now.Sub(t.At) < localToastTTL {
			kept = append(kept, t)
		}
	}
	c.local = kept
	return append(out, c.local...)
}

func (c *controller) toast(text string, now time.Time) {
	c.local = append(c.local, state.Toast{Kind: state.ToastError, Text: text, At: now})
	if c.invalidate != nil {
		time.AfterFunc(localToastTTL, c.invalidate)
	}
}

// themeName is the theme to draw with.
func (c *controller) themeName(snap *state.Snapshot) settings.Theme {
	if c.settingsOpen && c.preview != "" {
		return c.preview
	}
	if c.saved != "" {
		if snap.Settings.Theme == c.saved {
			c.saved = ""
		} else {
			return c.saved
		}
	}
	return snap.Settings.Theme
}

func (c *controller) findTask(snap *state.Snapshot, id string) (state.TaskView, bool) {
	for _, v := range snap.Tasks {
		if v.ID == id {
			return v, true
		}
	}
	return state.TaskView{}, false
}

func (c *controller) openAdd(prefill string, snap *state.Snapshot) {
	c.add.Open(prefill, snap.Settings.DownloadDir)
	c.dialog, c.addPending, c.lastAddErr = dialogAdd, false, ""
}

// handle executes one intent.
func (c *controller) handle(in screens.Intent, snap *state.Snapshot, now time.Time) {
	switch in := in.(type) {
	case screens.IntentCommand:
		if !c.send(in.Cmd) {
			c.toast(busyText, now)
			return
		}
		switch cmd := in.Cmd.(type) {
		case app.AddDownload:
			c.addPending, c.addBase = true, snap.AddResultSeq
		case app.SaveSettings:
			c.saved = cmd.S.Theme
		}
	case screens.IntentOpenAdd:
		c.openAdd(in.Prefill, snap)
		c.wantClip = in.Prefill == ""
	case IntentPasteAdd:
		c.openAdd("", snap)
		c.wantClip = true
	case screens.IntentOpenSettings:
		c.settings.Open(snap.Settings)
		c.settingsOpen, c.preview = true, ""
	case screens.IntentCloseSettings:
		c.settingsOpen, c.preview = false, ""
	case screens.IntentPreviewTheme:
		c.preview = in.Theme
	case screens.IntentSelect:
		c.main.Selected = in.ID
	case screens.IntentConfirm:
		if v, ok := c.findTask(snap, in.ID); ok {
			c.confirm.Open(in.Kind, v)
			c.dialog = dialogConfirm
		}
	case screens.IntentCloseDialog:
		c.dialog, c.addPending, c.wantClip = dialogNone, false, false
	case screens.IntentPickFolder:
		if c.picker == nil || c.pickBusy {
			return
		}
		c.pickBusy = true
		p := c.picker
		go func() {
			path, err := p.ChooseFolder()
			if err != nil && !errors.Is(err, explorer.ErrUserDecline) {
				c.log.Warn("folder picker failed", "err", err)
			}
			if err == nil && path != "" {
				select {
				case c.picked <- path:
				default:
				}
			} else {
				select {
				case c.picked <- "":
				default:
				}
			}
			if c.invalidate != nil {
				c.invalidate()
			}
		}()
	case screens.IntentOpen:
		go func() {
			f := platform.OpenPath
			if in.Reveal {
				f = platform.RevealPath
			}
			if err := f(in.Path); err != nil {
				c.log.Warn("open failed", "err", err)
			}
		}()
	case screens.IntentDismissToast:
		ts := c.toasts(snap, now)
		if in.Index < 0 || in.Index >= len(ts) {
			return
		}
		nLive := len(ts) - len(c.local)
		if in.Index < nLive {
			t := ts[in.Index]
			c.dismissed[toastKey{t.Text, t.At}] = true
		} else {
			i := in.Index - nLive
			c.local = append(c.local[:i], c.local[i+1:]...)
		}
	}
}

// afterResults reacts to new AddDownload results while the dialog waits.
func (c *controller) afterResults(snap *state.Snapshot) {
	if !c.addPending || snap.AddResultSeq <= c.addBase {
		return
	}
	c.addPending = false
	if c.dialog != dialogAdd {
		return
	}
	if snap.AddErr != "" {
		c.lastAddErr = snap.AddErr
		c.add.SetError(snap.AddErr)
		return
	}
	c.dialog = dialogNone
}

// pollPicked applies a finished folder pick.
func (c *controller) pollPicked() {
	select {
	case p := <-c.picked:
		c.pickBusy = false
		if p != "" && c.dialog == dialogAdd {
			c.add.SetDestDir(p)
		}
	default:
	}
}

// keyEvents turns this frame's shortcut keys into intents.
func (c *controller) keyEvents(gtx layout.Context, snap *state.Snapshot) []screens.Intent {
	inText := !gtx.Focused(nil)
	filters := []event.Filter{shortcutKeys[0], shortcutKeys[2], shortcutKeys[3],
		key.Filter{Name: key.NameEscape}}
	if !inText {
		filters = filters[:0]
		for _, f := range shortcutKeys {
			filters = append(filters, f)
		}
	}
	kc := KeyContext{Tasks: snap.Tasks, Selected: c.main.Selected, DialogOpen: c.dialog != dialogNone,
		SettingsOpen: c.settingsOpen, InText: inText}
	var out []screens.Intent
	for {
		ev, ok := gtx.Event(filters...)
		if !ok {
			return out
		}
		if ke, ok := ev.(key.Event); ok {
			if in := Shortcut(ke, kc); in != nil {
				out = append(out, in)
			}
		}
	}
}

// clipboardEvents requests and consumes clipboard text for the Add dialog.
func (c *controller) clipboardEvents(gtx layout.Context, snap *state.Snapshot) {
	tag := &c.clipTag
	for {
		ev, ok := gtx.Event(transfer.TargetFilter{Target: tag, Type: textMIME})
		if !ok {
			break
		}
		de, ok := ev.(transfer.DataEvent)
		if !ok {
			continue
		}
		rc := de.Open()
		b, _ := io.ReadAll(io.LimitReader(rc, 8<<10))
		_ = rc.Close()
		s := strings.TrimSpace(string(b))
		if c.clipReq && c.dialog == dialogAdd && (strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")) {
			c.add.Open(s, snap.Settings.DownloadDir)
		}
		c.clipReq = false
	}
	if c.wantClip {
		c.wantClip, c.clipReq = false, true
		gtx.Execute(clipboard.ReadCmd{Tag: tag})
	}
}

// frame lays out one frame.
func (c *controller) frame(gtx layout.Context, th *theme.Theme, snap *state.Snapshot, now time.Time) {
	c.pollPicked()
	c.afterResults(snap)
	view := *snap
	view.Toasts = c.toasts(snap, now)

	intents := c.keyEvents(gtx, snap)
	c.clipboardEvents(gtx, snap)

	if c.settingsOpen {
		intents = append(intents, c.settings.Layout(gtx, th, &view, now)...)
	} else {
		intents = append(intents, c.main.Layout(gtx, th, &view, now)...)
	}
	switch c.dialog {
	case dialogAdd:
		intents = append(intents, c.add.Layout(gtx, th, &view, now)...)
	case dialogConfirm:
		intents = append(intents, c.confirm.Layout(gtx, th, &view, now)...)
	}
	for _, in := range intents {
		if cp, ok := in.(screens.IntentCopy); ok {
			gtx.Execute(clipboard.WriteCmd{Type: textMIME, Data: io.NopCloser(strings.NewReader(cp.Text))})
			continue
		}
		c.handle(in, snap, now)
	}
	if len(intents) > 0 {
		gtx.Execute(op.InvalidateCmd{})
	}
}

// RunWindow opens the SwiftFetch window and runs its event loop until the
// window is closed. Closing sends Quit unless CloseToTray is on and a tray
// is available.
func RunWindow(d WindowDeps) error {
	w := d.Window
	if w == nil {
		w = new(gioapp.Window)
	}
	w.Option(gioapp.Title("SwiftFetch"), gioapp.MinSize(unit.Dp(640), unit.Dp(420)), gioapp.Size(unit.Dp(960), unit.Dp(640)))
	var picker FolderPicker
	if d.Explorer != nil {
		picker = d.Explorer(w)
	} else {
		picker = explorer.NewExplorer(w)
	}
	c := newController(d.App.Send, w.Invalidate)
	c.picker = picker
	if d.Logger != nil {
		c.log = d.Logger
	}
	light, dark := theme.New(theme.Light()), theme.New(theme.Dark())
	var ops op.Ops
	for {
		ev := w.Event()
		picker.ListenEvents(ev)
		switch e := ev.(type) {
		case gioapp.DestroyEvent:
			if e.Err != nil {
				c.log.Error("window error", "err", e.Err)
			}
			if !(d.App.Settings().CloseToTray && d.TrayAvailable != nil && d.TrayAvailable()) {
				for !d.App.Send(app.Quit{}) {
					time.Sleep(10 * time.Millisecond)
				}
			}
			if d.OnClosed != nil {
				d.OnClosed()
			}
			return e.Err
		case gioapp.FrameEvent:
			gtx := gioapp.NewContext(&ops, e)
			snap := d.Store.Load()
			th := light
			if c.themeName(snap) == settings.ThemeDark {
				th = dark
			}
			c.frame(gtx, th, snap, time.Now())
			e.Frame(gtx.Ops)
		}
	}
}
