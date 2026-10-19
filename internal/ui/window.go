package ui

import (
	"image"
	"io"
	"log/slog"
	"strings"
	"time"

	gioapp "gioui.org/app"
	"gioui.org/io/clipboard"
	"gioui.org/io/event"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/x/explorer"

	"github.com/raptordnj/goti/internal/app"
	"github.com/raptordnj/goti/internal/brand"
	"github.com/raptordnj/goti/internal/platform"
	"github.com/raptordnj/goti/internal/settings"
	"github.com/raptordnj/goti/internal/ui/anim"
	"github.com/raptordnj/goti/internal/ui/dialogs"
	"github.com/raptordnj/goti/internal/ui/screens"
	"github.com/raptordnj/goti/internal/ui/state"
	"github.com/raptordnj/goti/internal/ui/theme"
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
	Explorer func(w *gioapp.Window) FolderPicker
	// KeepAlive reports whether destroying the window must not quit, given
	// the CloseToTray setting. Nil falls back to CloseToTray && TrayAvailable.
	KeepAlive     func(closeToTray bool) bool
	TrayAvailable func() bool
	// OnClosed runs after the window is gone (nil = no-op).
	OnClosed func()
	// Window, when set, is used instead of a new *app.Window (its
	// Invalidate is also what state.Deps.Invalidate should call).
	Window *gioapp.Window
	Logger *slog.Logger
	// Version and DataDir are shown in Settings > About.
	Version, DataDir string
	// TakeSharedURL returns a URL shared into the app once (Android), or
	// "". Checked each frame; non-empty opens the Add dialog prefilled.
	TakeSharedURL func() string
	// OnAddSubmit runs after each Add submit (nil = no-op).
	OnAddSubmit func()
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
	addKnown   map[string]bool // task IDs present when the last Add was sent
	selectNew  bool            // select the task the last Add created once it appears
	errSeen    time.Time       // newest error toast already seen

	local     []state.Toast
	dismissed map[toastKey]bool
	picked    chan string

	clipTag  int // address is the clipboard read tag
	wantClip bool
	clipReq  bool
	pickBusy bool

	takeShared func() string
	onAdd      func()

	// insets are the window's system-bar and keyboard insets; the UI is
	// laid out inside them and the bars are painted to match.
	insets layout.Inset
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

// pollShared opens the Add dialog for a URL shared into the app.
func (c *controller) pollShared(snap *state.Snapshot) {
	if c.takeShared == nil {
		return
	}
	if u := c.takeShared(); u != "" {
		c.openAdd(u, snap)
		c.add.OpenShared(u, snap.Settings.DownloadDir)
		c.wantClip = false
	}
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
			if c.onAdd != nil {
				c.onAdd()
			}
			c.addPending, c.addBase = true, snap.AddResultSeq
			c.addKnown = make(map[string]bool, len(snap.Tasks))
			for _, v := range snap.Tasks {
				c.addKnown[v.ID] = true
			}
		case app.PauseTask:
			c.optimistic(snap, cmd.ID, "resume")
		case app.ResumeTask:
			c.optimistic(snap, cmd.ID, "pause")
		case app.RetryTask:
			c.optimistic(snap, cmd.ID, "pause")
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
		c.startPick()
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

// optimistic shows icon as task id's primary action until it changes.
func (c *controller) optimistic(snap *state.Snapshot, id, icon string) {
	if v, ok := c.findTask(snap, id); ok {
		c.main.SetOptimistic(v, icon)
	}
}

// syncOptimistic drops optimistic icons of tasks that changed, and all of
// them when a new error toast arrives (a command may have failed).
func (c *controller) syncOptimistic(snap *state.Snapshot) {
	for _, t := range snap.Toasts {
		if t.Kind == state.ToastError && t.At.After(c.errSeen) {
			c.errSeen = t.At
			c.main.ClearOptimistic("")
		}
	}
	c.main.SyncOptimistic(snap)
}

// selectNewTask selects and reveals the newest task created since the last
// Add was sent, once it is in snap.
func (c *controller) selectNewTask(snap *state.Snapshot) {
	if !c.selectNew {
		return
	}
	var best *state.TaskView
	for i := range snap.Tasks {
		v := &snap.Tasks[i]
		if !c.addKnown[v.ID] && (best == nil || v.CreatedAt.After(best.CreatedAt)) {
			best = v
		}
	}
	if best == nil {
		return
	}
	c.selectNew, c.addKnown = false, nil
	c.main.Selected = best.ID
	c.main.ScrollTo(best.ID)
}

// afterResults reacts to new AddDownload results while the dialog waits.
func (c *controller) afterResults(snap *state.Snapshot) {
	defer c.selectNewTask(snap)
	if !c.addPending || snap.AddResultSeq <= c.addBase {
		return
	}
	c.addPending = false
	if snap.AddErr == "" {
		c.selectNew = true
	}
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

// frame lays out one frame.
func (c *controller) frame(gtx layout.Context, th *theme.Theme, snap *state.Snapshot, now time.Time) {
	c.pollPicked()
	c.pollShared(snap)
	c.afterResults(snap)
	c.syncOptimistic(snap)
	view := *snap
	view.Toasts = c.toasts(snap, now)

	intents := c.keyEvents(gtx, snap)
	c.clipboardEvents(gtx, snap)

	full := gtx
	gtx, pop := c.safeArea(gtx, th)
	if c.settingsOpen {
		intents = append(intents, c.settings.Layout(gtx, th, &view, now)...)
	} else {
		intents = append(intents, c.main.Layout(gtx, th, &view, now)...)
	}
	pop()
	gtx = full
	c.add.SetInsets(c.insets)
	c.confirm.SetInsets(c.insets)
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
	if c.dialog != dialogNone || c.settingsOpen {
		c.main.CloseMenu() // a modal surface supersedes the row menu
	}
	if len(intents) > 0 {
		gtx.Execute(op.InvalidateCmd{})
	}
}

// safeArea paints the system-bar bands to match the screen under them and
// returns gtx shrunk and offset to the area inside c.insets, plus the pop
// of that offset. With no insets (desktop) it returns gtx unchanged and
// paints nothing.
func (c *controller) safeArea(gtx layout.Context, th *theme.Theme) (layout.Context, func()) {
	in := c.insets
	if in == (layout.Inset{}) {
		return gtx, func() {}
	}
	full := gtx.Constraints.Max
	t, b := gtx.Dp(in.Top), gtx.Dp(in.Bottom)
	l, r := gtx.Dp(in.Left), gtx.Dp(in.Right)
	topC, botC := th.Surface, th.Bg // header above, list below
	switch {
	case c.settingsOpen:
		topC, botC = th.Bg, th.Surface // page above, sticky footer below
	case c.main.DetailsMode == screens.DetailsPage:
		topC, botC = th.Surface, th.Surface
	}
	paint.FillShape(gtx.Ops, botC, clip.Rect{Max: full}.Op())
	paint.FillShape(gtx.Ops, topC, clip.Rect{Max: image.Pt(full.X, t)}.Op())
	st := op.Offset(image.Pt(l, t)).Push(gtx.Ops)
	gtx.Constraints = layout.Exact(image.Pt(max(full.X-l-r, 0), max(full.Y-t-b, 0)))
	return gtx, st.Pop
}

// wireReduceMotion makes animations follow the saved Reduce motion setting.
func wireReduceMotion(cur func() settings.Settings) {
	anim.Reduce = func() bool { return cur().ReduceMotion }
}

// RunWindow opens the Goti window and runs its event loop until the
// window is closed. Closing sends Quit unless CloseToTray is on and a tray
// is available.
func RunWindow(d WindowDeps) error {
	w := d.Window
	if w == nil {
		w = new(gioapp.Window)
	}
	w.Option(gioapp.Title(brand.Name), gioapp.MinSize(unit.Dp(640), unit.Dp(420)), gioapp.Size(unit.Dp(960), unit.Dp(640)))
	var picker FolderPicker
	if d.Explorer != nil {
		picker = d.Explorer(w)
	} else {
		picker = explorer.NewExplorer(w)
	}
	c := newController(d.App.Send, w.Invalidate)
	c.takeShared, c.onAdd = d.TakeSharedURL, d.OnAddSubmit
	c.picker = picker
	c.settings.Version, c.settings.DataDir = d.Version, d.DataDir
	c.settings.Mobile = platform.Mobile
	if d.Logger != nil {
		c.log = d.Logger
	}
	wireReduceMotion(d.App.Settings)
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
			if quitOnDestroy(d, d.App.Settings().CloseToTray) {
				SendQuit(d.App.Send, c.log)
			}
			if d.OnClosed != nil {
				d.OnClosed()
			}
			return e.Err
		case gioapp.FrameEvent:
			gtx := gioapp.NewContext(&ops, e)
			c.insets = layout.Inset{Top: e.Insets.Top, Bottom: e.Insets.Bottom, Left: e.Insets.Left, Right: e.Insets.Right}
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

// quitOnDestroy decides whether destroying the window quits the app.
func quitOnDestroy(d WindowDeps, closeToTray bool) bool {
	if d.KeepAlive != nil {
		return !d.KeepAlive(closeToTray)
	}
	return !(closeToTray && d.TrayAvailable != nil && d.TrayAvailable())
}
