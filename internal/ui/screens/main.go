package screens

import (
	"fmt"
	"image"
	"image/color"
	"time"

	"gioui.org/font"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/raptordnj/goti/internal/app"
	"github.com/raptordnj/goti/internal/brand"
	"github.com/raptordnj/goti/internal/domain"
	"github.com/raptordnj/goti/internal/platform"
	"github.com/raptordnj/goti/internal/ui/components"
	"github.com/raptordnj/goti/internal/ui/icons"
	"github.com/raptordnj/goti/internal/ui/state"
	"github.com/raptordnj/goti/internal/ui/theme"
)

// Breakpoint is the width at and above which details show as a right pane.
const Breakpoint unit.Dp = 1000

// detailsPaneWidth is the width of the details pane in wide layouts.
const detailsPaneWidth unit.Dp = 340

// headerHeight is the main header's height.
const headerHeight unit.Dp = 52

// DetailsMode is how the details panel was laid out in the last frame.
type DetailsMode int

// Details layout modes.
const (
	DetailsNone DetailsMode = iota
	DetailsPane
	DetailsSheet
	// DetailsPage is the compact layout's full-window details page.
	DetailsPage
)

var sectionTitles = [state.NumSections]string{"Active", "Queued", "Paused", "Completed", "Failed"}

// Main is the main screen: header, offline banner, sectioned task list,
// details pane/sheet and toasts.
type Main struct {
	// Selected is the task whose details are shown ("" = none).
	Selected string
	// ShownSections lists the section headers laid out last frame.
	ShownSections []state.Section
	// DetailsMode records how details were laid out last frame.
	DetailsMode DetailsMode
	// ShownTasks lists the task rows laid out last frame (collapsed
	// sections omit theirs).
	ShownTasks []string
	// EmptyBounds is the window rect of the empty-state stack last frame.
	EmptyBounds image.Rectangle
	// PillShown reports whether the aggregate pill was drawn last frame.
	PillShown bool
	// AddBounds is the window rect (x extent) of the header Add button.
	AddBounds image.Rectangle
	// HeaderHeight is the header's height in px last frame.
	HeaderHeight int
	// FABBounds is the window rect of the floating Add button (compact
	// layouts only; empty otherwise).
	FABBounds image.Rectangle
	// PillBounds is the window rect of the aggregate pill when it sits on
	// its own line (compact layouts).
	PillBounds image.Rectangle

	compact   bool      // last frame used the compact layout
	pressID   string    // row being pressed (long-press tracking)
	pressAt   time.Time // when the press on pressID began
	longFired bool      // the press already opened the menu
	fab       widget.Clickable

	add, emptyAdd, pauseAll, settings widget.Clickable
	addW                              int // Add button width last frame
	scrim, sheetTag                   int // pointer tags: sheet scrim, sheet body
	list                              widget.List
	rows                              map[string]*components.RowWidgets
	menuFor                           string // task whose overflow menu is open
	collapsed                         [state.NumSections]widget.Bool
	listTop                           int   // window y of the task list (px)
	itemH                             []int // heights of list items laid out last frame
	details                           Details
	dismiss                           []widget.Clickable
	toastFades                        components.ToastFades
	pending                           map[string]optimistic // optimistic primary icons by task ID
	scrollTo                          string                // task to scroll into view next layout
}

// optimistic is a primary-action icon shown before the snapshot confirms a
// command; it lasts until the task's state changes.
type optimistic struct {
	icon        string
	st          domain.State
	interrupted bool
}

// SetOptimistic shows icon ("pause", "resume") as v's primary action until
// v's state changes or ClearOptimistic is called.
func (m *Main) SetOptimistic(v state.TaskView, icon string) {
	if m.pending == nil {
		m.pending = map[string]optimistic{}
	}
	m.pending[v.ID] = optimistic{icon, v.State, v.Interrupted}
}

// Optimistic returns the optimistic icon of task id ("" = none).
func (m *Main) Optimistic(id string) string { return m.pending[id].icon }

// ClearOptimistic drops the optimistic icon of id, or of every task when id
// is "".
func (m *Main) ClearOptimistic(id string) {
	if id == "" {
		clear(m.pending)
		return
	}
	delete(m.pending, id)
}

// SyncOptimistic drops optimistic icons of tasks that changed state or are
// gone in snap.
func (m *Main) SyncOptimistic(snap *state.Snapshot) {
	if len(m.pending) == 0 {
		return
	}
	for id, p := range m.pending {
		v := m.findTask(snap, id)
		if v == nil || v.State != p.st || v.Interrupted != p.interrupted {
			delete(m.pending, id)
		}
	}
}

// ScrollTo scrolls task id into view on the next layout (expanding its
// section if collapsed).
func (m *Main) ScrollTo(id string) { m.scrollTo = id }

// ScrollTarget is the task a pending ScrollTo will reveal ("" = none).
func (m *Main) ScrollTarget() string { return m.scrollTo }

// OpenMenu opens task id's overflow menu (it is anchored once the row has
// been laid out).
func (m *Main) OpenMenu(snap *state.Snapshot, id string) {
	v := m.findTask(snap, id)
	if v == nil {
		return
	}
	m.CloseMenu()
	w := m.Row(id)
	m.menuFor = id
	w.Menu.SetItems(components.RowMenuItems(*v))
	w.Menu.Open = true
}

// Row returns the widget state of task id, creating it if needed.
func (m *Main) Row(id string) *components.RowWidgets {
	if m.rows == nil {
		m.rows = map[string]*components.RowWidgets{}
	}
	w := m.rows[id]
	if w == nil {
		w = &components.RowWidgets{}
		m.rows[id] = w
	}
	return w
}

// Collapsible returns the collapse toggle of sec (nil when the section is
// not collapsible: only Completed and Failed are).
func (m *Main) Collapsible(sec state.Section) *widget.Bool {
	if sec == state.SectionCompleted || sec == state.SectionFailed {
		return &m.collapsed[sec]
	}
	return nil
}

// MenuOpen reports whether a row's overflow menu is open (it then owns Esc
// and the arrow keys).
func (m *Main) MenuOpen() bool {
	if m.menuFor == "" {
		return false
	}
	w := m.rows[m.menuFor]
	return w != nil && w.Menu.Open
}

// CloseMenu closes any open overflow menu.
func (m *Main) CloseMenu() {
	if w := m.rows[m.menuFor]; w != nil {
		w.Menu.Open = false
	}
	m.menuFor = ""
}

// Layout draws the main screen and returns the user's intents.
func (m *Main) Layout(gtx layout.Context, th *theme.Theme, snap *state.Snapshot, now time.Time) []Intent {
	var out []Intent
	emit := func(i Intent) { out = append(out, i) }
	m.events(gtx, snap, emit)

	sel := m.selectedView(snap)
	wide := gtx.Constraints.Max.X >= gtx.Dp(Breakpoint)
	m.compact = Compact(gtx)
	m.DetailsMode = DetailsNone
	m.FABBounds, m.PillBounds = image.Rectangle{}, image.Rectangle{}
	if sel != nil {
		m.DetailsMode = DetailsSheet
		switch {
		case wide:
			m.DetailsMode = DetailsPane
		case m.compact:
			m.DetailsMode = DetailsPage
		}
		m.details.ID = sel.ID
	}
	m.details.Back = m.compact
	if m.DetailsMode == DetailsPage {
		m.page(gtx, th, snap, now, emit)
		return out
	}

	paint.Fill(gtx.Ops, th.Bg)
	m.ShownSections = m.ShownSections[:0]
	m.ShownTasks = m.ShownTasks[:0]
	m.listTop = -1
	m.EmptyBounds = image.Rectangle{}
	top := 0
	layout.Stack{Alignment: layout.SE}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					d := m.header(gtx, th, snap)
					top = d.Size.Y
					return d
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if !m.compact || !m.PillShown {
						return layout.Dimensions{}
					}
					d := m.pillLine(gtx, th, snap, top)
					top += d.Size.Y
					return d
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if !snap.Offline {
						return layout.Dimensions{}
					}
					d := offlineBanner(th)(gtx)
					top += d.Size.Y
					return d
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return m.body(gtx, th, snap, now, emit, top)
				}),
			)
		}),
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			if len(snap.Tasks) == 0 {
				m.EmptyBounds = layoutEmpty(gtx, th, &m.emptyAdd, m.compact)
			}
			return layout.Dimensions{Size: gtx.Constraints.Max} // full size: the stack aligns SE
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			if len(m.dismiss) < len(snap.Toasts) {
				m.dismiss = make([]widget.Clickable, len(snap.Toasts))
			}
			in := layout.UniformInset(theme.S4)
			if m.compact {
				in.Bottom += fabSize + theme.S3 // toasts stack above the FAB
			}
			return in.Layout(gtx, components.ToastStack(th, &m.toastFades, snap.Toasts, m.dismiss))
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			if !m.compact {
				return layout.Dimensions{}
			}
			return m.layoutFAB(gtx, th)
		}),
	)
	m.layoutMenu(gtx, th, snap, emit)
	return out
}

// layoutMenu draws the open row menu over the window, anchored to the row's
// more button, and turns a pick into an intent (re-checking CanX).
func (m *Main) layoutMenu(gtx layout.Context, th *theme.Theme, snap *state.Snapshot, emit func(Intent)) {
	if m.menuFor == "" {
		return
	}
	w := m.rows[m.menuFor]
	v := m.findTask(snap, m.menuFor)
	y, visible := m.rowY(snap, m.menuFor)
	if w == nil || v == nil || !visible {
		m.CloseMenu()
		return
	}
	w.Menu.SetItems(components.RowMenuItems(*v))
	w.Menu.Touch = m.compact
	w.Menu.Anchor = image.Pt(gtx.Dp(theme.S4)+w.MoreAt.X, y+w.MoreAt.Y)
	picked := w.Menu.Layout(gtx, th)
	if !w.Menu.Open {
		m.menuFor = ""
	}
	if picked != "" {
		menuIntent(picked, *v, emit)
	}
}

// menuIntent maps a picked menu key to its intent, honouring CanX.
func menuIntent(key string, v state.TaskView, emit func(Intent)) {
	switch key {
	case "open":
		if v.CanOpen {
			emit(IntentOpen{Path: v.DestPath})
		}
	case "folder":
		emit(IntentOpen{Path: v.DestPath, Reveal: true})
	case "copy-url":
		emit(IntentCopy{Text: v.URL})
	case "copy-path":
		emit(IntentCopy{Text: v.DestPath})
	case "restart":
		if v.CanRestart {
			emit(IntentCommand{Cmd: app.RestartTask{ID: v.ID}})
		}
	case "cancel":
		if v.CanCancel {
			emit(IntentConfirm{Kind: ConfirmCancel, ID: v.ID})
		}
	case "remove":
		emit(IntentConfirm{Kind: ConfirmRemove, ID: v.ID})
	}
}

func (m *Main) findTask(snap *state.Snapshot, id string) *state.TaskView {
	for i := range snap.Tasks {
		if snap.Tasks[i].ID == id {
			return &snap.Tasks[i]
		}
	}
	return nil
}

// rowY returns the window y of task id's row from last layout's list
// position and item heights; false when it is not on screen.
func (m *Main) rowY(snap *state.Snapshot, id string) (int, bool) {
	if m.listTop < 0 {
		return 0, false
	}
	items := m.items(snap)
	pos := m.list.Position
	y := m.listTop - pos.Offset
	for i := pos.First; i < len(items) && i < len(m.itemH); i++ {
		it := items[i]
		if !it.header && snap.Tasks[it.task].ID == id {
			return y, true
		}
		y += m.itemH[i]
	}
	return 0, false
}

func (m *Main) selectedView(snap *state.Snapshot) *state.TaskView {
	if m.Selected == "" {
		return nil
	}
	return m.findTask(snap, m.Selected)
}

func (m *Main) body(gtx layout.Context, th *theme.Theme, snap *state.Snapshot, now time.Time, emit func(Intent), top int) layout.Dimensions {
	details := func(gtx layout.Context) layout.Dimensions {
		for _, i := range m.details.Layout(gtx, th, snap, now) {
			m.handleDetails(i, emit)
		}
		return layout.Dimensions{Size: gtx.Constraints.Max}
	}
	switch m.DetailsMode {
	case DetailsSheet:
		return layout.Stack{}.Layout(gtx,
			layout.Expanded(func(gtx layout.Context) layout.Dimensions { return m.taskList(gtx, th, snap, now, top) }),
			layout.Expanded(func(gtx layout.Context) layout.Dimensions { return m.sheet(gtx, th, emit, details) }),
		)
	case DetailsPane:
		return layout.Flex{}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return m.taskList(gtx, th, snap, now, top) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				sz := image.Pt(gtx.Dp(detailsPaneWidth), gtx.Constraints.Max.Y)
				gtx.Constraints = layout.Exact(sz)
				return components.Raised(gtx, th, 0, func(gtx layout.Context) layout.Dimensions {
					paint.FillShape(gtx.Ops, th.Surface, clip.Rect{Max: sz}.Op())
					paint.FillShape(gtx.Ops, th.Hairline, clip.Rect{Max: image.Pt(max(1, gtx.Dp(1)), sz.Y)}.Op())
					return details(gtx)
				})
			}),
		)
	}
	return m.taskList(gtx, th, snap, now, top)
}

// sheetFrac is the share of the body height the narrow-layout sheet takes.
const sheetFrac = 0.72

// sheet draws a scrim (45 % dark / 30 % light; a press closes) over the
// list and a bottom sheet with Raised elevation and 12-dp top corners.
func (m *Main) sheet(gtx layout.Context, th *theme.Theme, emit func(Intent), details layout.Widget) layout.Dimensions {
	full := gtx.Constraints.Max
	scrimA := 0.30
	if int(th.Bg.R)+int(th.Bg.G)+int(th.Bg.B) < 3*0x80 {
		scrimA = 0.45
	}
	area := clip.Rect{Max: full}.Push(gtx.Ops)
	event.Op(gtx.Ops, &m.scrim)
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &m.scrim, Kinds: pointer.Press | pointer.Scroll, ScrollY: pointer.ScrollRange{Min: -1e6, Max: 1e6}})
		if !ok {
			break
		}
		if e, ok := ev.(pointer.Event); ok && e.Kind == pointer.Press {
			m.handleDetails(IntentSelect{}, emit)
		}
	}
	paint.FillShape(gtx.Ops, color.NRGBA{A: uint8(scrimA * 255)}, clip.Rect{Max: full}.Op())
	area.Pop()
	h := int(float32(full.Y) * sheetFrac)
	r := gtx.Dp(theme.RadiusPanel)
	defer op.Offset(image.Pt(0, full.Y-h)).Push(gtx.Ops).Pop()
	gtx.Constraints = layout.Exact(image.Pt(full.X, h))
	components.Raised(gtx, th, theme.RadiusPanel, func(gtx layout.Context) layout.Dimensions {
		rect := image.Rectangle{Max: gtx.Constraints.Max}
		shape := clip.RRect{Rect: rect, NW: r, NE: r}
		paint.FillShape(gtx.Ops, th.Surface, shape.Op(gtx.Ops))
		defer shape.Push(gtx.Ops).Pop()
		event.Op(gtx.Ops, &m.sheetTag) // presses on the sheet do not reach the scrim
		for {
			if _, ok := gtx.Event(pointer.Filter{Target: &m.sheetTag, Kinds: pointer.Press}); !ok {
				break
			}
		}
		return details(gtx)
	})
	return layout.Dimensions{Size: full}
}

// handleDetails forwards details intents, clearing the selection on close.
func (m *Main) handleDetails(i Intent, emit func(Intent)) {
	if s, ok := i.(IntentSelect); ok && s.ID == "" {
		m.Selected = ""
	}
	emit(i)
}

func (m *Main) header(gtx layout.Context, th *theme.Theme, snap *state.Snapshot) layout.Dimensions {
	h := gtx.Dp(headerHeight)
	gtx.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X, h))
	paint.FillShape(gtx.Ops, th.Surface, clip.Rect{Max: gtx.Constraints.Max}.Op())
	paint.FillShape(gtx.Ops, th.Hairline, clip.Rect{Min: image.Pt(0, h-max(1, gtx.Dp(1))), Max: gtx.Constraints.Max}.Op())
	m.PillShown = snap.ActiveCount > 0 || snap.TotalSpeedBps > 0
	m.HeaderHeight = h
	if m.compact {
		m.AddBounds = image.Rectangle{}
		return m.compactHeader(gtx, th)
	}
	allIcon, allDesc := icons.Pause, "Pause all"
	if snap.QueuePaused {
		allIcon, allDesc = icons.Play, "Resume all"
	}
	layout.Inset{Left: theme.S4, Right: theme.S4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.W.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min = image.Pt(gtx.Constraints.Max.X, 0)
			x := 0 // total width of the children left of Add
			track := func(w layout.Widget) layout.Widget {
				return func(gtx layout.Context) layout.Dimensions { d := w(gtx); x += d.Size.X; return d }
			}
			fd := layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(track(func(gtx layout.Context) layout.Dimensions { return appMark(gtx, th) })),
				layout.Rigid(track(layout.Spacer{Width: theme.S2}.Layout)),
				layout.Rigid(track(bold(th, theme.Title, th.Text, brand.Name))),
				layout.Rigid(track(layout.Spacer{Width: theme.S4}.Layout)),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					// Consume all spare width so the controls sit at the right edge.
					w := gtx.Constraints.Max.X
					gtx.Constraints.Min = image.Point{}
					var d layout.Dimensions
					if m.PillShown {
						d = aggregatePill(gtx, th, snap)
					}
					x += w
					return layout.Dimensions{Size: image.Pt(w, d.Size.Y)}
				}),
				layout.Rigid(track(components.IconButton(th, &m.pauseAll, allIcon, allDesc))),
				layout.Rigid(track(layout.Spacer{Width: theme.S1}.Layout)),
				layout.Rigid(track(components.IconButton(th, &m.settings, icons.Settings, "Settings"))),
				layout.Rigid(track(layout.Spacer{Width: theme.S3}.Layout)),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					Note("Add")
					d := components.Button(th, &m.add, "Add", components.Primary, components.Small(), components.WithIcon(icons.Plus))(gtx)
					m.addW = d.Size.X
					return d
				}),
			)
			// Flex lays out rigid children before the flexed one, so x
			// (everything left of Add) is only complete here.
			m.AddBounds = image.Rect(gtx.Dp(theme.S4)+x, 0, gtx.Dp(theme.S4)+x+m.addW, h)
			return fd
		})
	})
	return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, h)}
}

// appMark is the 28-dp accent rounded square with the arrow-down glyph.
func appMark(gtx layout.Context, th *theme.Theme) layout.Dimensions {
	sz := image.Pt(gtx.Dp(28), gtx.Dp(28))
	paint.FillShape(gtx.Ops, th.Accent, clip.UniformRRect(image.Rectangle{Max: sz}, gtx.Dp(theme.RadiusRow)).Op(gtx.Ops))
	gtx.Constraints = layout.Exact(sz)
	layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return icons.ArrowDown(gtx, 16, th.OnAccent) })
	return layout.Dimensions{Size: sz}
}

// aggregatePill shows "3 active · 8.4 MB/s"; the speed sits in a slot as
// wide as its zero-digit form so the pill does not jitter.
func aggregatePill(gtx layout.Context, th *theme.Theme, snap *state.Snapshot) layout.Dimensions {
	txt := fmt.Sprintf("%d active%s%s", snap.ActiveCount, metaSep, platform.FormatSpeed(snap.TotalSpeedBps))
	record(txt)
	m := op.Record(gtx.Ops)
	d := layout.Inset{Left: theme.S2 + theme.S0, Right: theme.S2 + theme.S0, Top: theme.S0 + 1, Bottom: theme.S0 + 1}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				r := gtx.Dp(6)
				paint.FillShape(gtx.Ops, th.Accent, clip.Ellipse{Max: image.Pt(r, r)}.Op(gtx.Ops))
				return layout.Dimensions{Size: image.Pt(r+gtx.Dp(6), r)}
			}),
			layout.Rigid(slot(th, theme.Caption, th.Text, txt)),
		)
	})
	call := m.Stop()
	paint.FillShape(gtx.Ops, th.SurfaceSunken, clip.UniformRRect(image.Rectangle{Max: d.Size}, d.Size.Y/2).Op(gtx.Ops))
	call.Add(gtx.Ops)
	return d
}

// listItem is a flattened list entry: a section header or a task row.
type listItem struct {
	header bool
	last   bool // the final item of the list
	sec    state.Section
	task   int // index in snap.Tasks
}

func (m *Main) items(snap *state.Snapshot) []listItem {
	var out []listItem
	last := state.Section(-1)
	for i, v := range snap.Tasks {
		if v.Section != last && snap.Counts[v.Section] > 0 {
			out = append(out, listItem{header: true, sec: v.Section})
			last = v.Section
		}
		if c := m.Collapsible(v.Section); c != nil && c.Value {
			continue
		}
		out = append(out, listItem{sec: v.Section, task: i})
	}
	if len(out) > 0 {
		out[len(out)-1].last = true
	}
	return out
}

func (m *Main) taskList(gtx layout.Context, th *theme.Theme, snap *state.Snapshot, now time.Time, top int) layout.Dimensions {
	if len(snap.Tasks) == 0 {
		return layout.Dimensions{Size: gtx.Constraints.Max} // drawn window-centred by Layout
	}
	if m.scrollTo != "" {
		m.reveal(snap)
	}
	items := m.items(snap)
	for _, it := range items {
		if it.header {
			m.ShownSections = append(m.ShownSections, it.sec)
		} else {
			m.ShownTasks = append(m.ShownTasks, snap.Tasks[it.task].ID)
		}
	}
	m.listTop = top
	if cap(m.itemH) < len(items) {
		m.itemH = make([]int, len(items))
	}
	m.itemH = m.itemH[:len(items)]
	m.list.Axis = layout.Vertical
	return layout.Inset{Left: theme.S4, Right: theme.S4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return m.list.Layout(gtx, len(items), func(gtx layout.Context, i int) layout.Dimensions {
			d := m.listItem(gtx, th, snap, now, items[i], i == 0)
			m.itemH[i] = d.Size.Y
			return d
		})
	})
}

// reveal expands the section of m.scrollTo and scrolls its row (with the
// section header above it when that is the previous item) into view.
func (m *Main) reveal(snap *state.Snapshot) {
	id := m.scrollTo
	m.scrollTo = ""
	v := m.findTask(snap, id)
	if v == nil {
		return
	}
	if c := m.Collapsible(v.Section); c != nil {
		c.Value = false
	}
	for i, it := range m.items(snap) {
		if !it.header && snap.Tasks[it.task].ID == id {
			if i > 0 && m.items(snap)[i-1].header {
				i--
			}
			m.list.Position.First, m.list.Position.Offset = i, 0
			m.list.Position.BeforeEnd = true
			return
		}
	}
}

func (m *Main) listItem(gtx layout.Context, th *theme.Theme, snap *state.Snapshot, now time.Time, it listItem, first bool) layout.Dimensions {
	if it.header {
		in := layout.Inset{Top: theme.S3}
		if first {
			in.Top = theme.S1
		}
		return in.Layout(gtx, components.SectionHeader(th, sectionTitles[it.sec], snap.Counts[it.sec], m.Collapsible(it.sec)))
	}
	v := snap.Tasks[it.task]
	m.Row(v.ID).Optimistic = m.pending[v.ID].icon
	w := m.Row(v.ID)
	w.Compact = m.compact
	in := layout.Inset{Bottom: theme.S0}
	if m.compact && it.last {
		in.Bottom = fabSize + 2*theme.S4 // the last row scrolls clear of the FAB
	}
	return in.Layout(gtx, components.TaskRow(th, v, w, now, v.ID == m.Selected))
}

// events converts last frame's clicks into intents.
func (m *Main) events(gtx layout.Context, snap *state.Snapshot, emit func(Intent)) {
	if m.add.Clicked(gtx) || m.emptyAdd.Clicked(gtx) || m.fab.Clicked(gtx) {
		emit(IntentOpenAdd{})
	}
	if m.pauseAll.Clicked(gtx) {
		if snap.QueuePaused {
			emit(IntentCommand{Cmd: app.ResumeAll{}})
		} else {
			emit(IntentCommand{Cmd: app.PauseAll{}})
		}
	}
	if m.settings.Clicked(gtx) {
		emit(IntentOpenSettings{})
	}
	for i := range m.dismiss {
		if m.dismiss[i].Clicked(gtx) {
			emit(IntentDismissToast{Index: i})
		}
	}
	live := map[string]bool{}
	for _, v := range snap.Tasks {
		live[v.ID] = true
		m.rowEvents(gtx, v, emit)
	}
	for id := range m.rows {
		if !live[id] {
			delete(m.rows, id)
		}
	}
	if m.Selected != "" && !live[m.Selected] {
		m.Selected = ""
	}
}

func (m *Main) rowEvents(gtx layout.Context, v state.TaskView, emit func(Intent)) {
	w := m.Row(v.ID)
	cmd := func(c *widget.Clickable, ok bool, mk func() app.Command) {
		if c.Clicked(gtx) && ok {
			emit(IntentCommand{Cmd: mk()})
		}
	}
	cmd(&w.Pause, v.CanPause, func() app.Command { return app.PauseTask{ID: v.ID} })
	cmd(&w.Resume, v.CanResume, func() app.Command { return app.ResumeTask{ID: v.ID} })
	cmd(&w.Retry, v.CanRetry, func() app.Command { return app.RetryTask{ID: v.ID} })
	if w.Open.Clicked(gtx) && v.CanOpen {
		emit(IntentOpen{Path: v.DestPath})
	}
	if w.Cancel.Clicked(gtx) && v.CanCancel {
		emit(IntentConfirm{Kind: ConfirmCancel, ID: v.ID})
	}
	for {
		c, ok := w.Select.Update(gtx)
		if !ok {
			break
		}
		if m.longFired && m.pressID == v.ID {
			continue // the release that ends a long press is not a tap
		}
		// Double-click opens a finished file; otherwise a click selects.
		if c.NumClicks >= 2 && v.State == domain.StateCompleted && v.CanOpen {
			emit(IntentOpen{Path: v.DestPath})
			continue
		}
		m.Selected = v.ID
		emit(IntentSelect{ID: v.ID})
	}
	m.longPress(gtx, v, w)
	if w.More.Clicked(gtx) {
		if m.menuFor == v.ID && w.Menu.Open {
			m.CloseMenu()
		} else {
			m.CloseMenu()
			m.menuFor = v.ID
			w.Menu.SetItems(components.RowMenuItems(v))
			w.Menu.Open = true
		}
	}
}

// fill paints c over the minimum constraints and returns that size.
func fill(gtx layout.Context, c color.NRGBA) layout.Dimensions {
	sz := gtx.Constraints.Min
	paint.FillShape(gtx.Ops, c, clip.Rect{Max: sz}.Op())
	return layout.Dimensions{Size: sz}
}

// bold returns a semibold single-line label.
func bold(th *theme.Theme, size unit.Sp, c color.NRGBA, s string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		record(s)
		l := labelStyle(th, size, c, s)
		l.Font.Weight = font.SemiBold
		return l.Layout(gtx)
	}
}

// Fill paints c over the minimum constraints (exported for dialogs).
func Fill(gtx layout.Context, c color.NRGBA) layout.Dimensions { return fill(gtx, c) }

// longPress opens v's overflow menu once its row has been held for
// longPressFor ms (compact layouts), and schedules the frame that fires it.
func (m *Main) longPress(gtx layout.Context, v state.TaskView, w *components.RowWidgets) {
	if !w.Select.Pressed() {
		if m.pressID == v.ID {
			m.pressID, m.longFired = "", false
		}
		return
	}
	if !m.compact {
		return
	}
	if m.pressID != v.ID {
		m.pressID, m.pressAt, m.longFired = v.ID, gtx.Now, false
	}
	if m.longFired {
		return
	}
	at := m.pressAt.Add(longPressFor * time.Millisecond)
	if gtx.Now.Before(at) {
		gtx.Execute(op.InvalidateCmd{At: at})
		return
	}
	m.longFired = true
	m.CloseMenu()
	m.menuFor = v.ID
	w.Menu.SetItems(components.RowMenuItems(v))
	w.Menu.Open = true
}

// compactHeader is the phone header: mark and name left, settings right.
func (m *Main) compactHeader(gtx layout.Context, th *theme.Theme) layout.Dimensions {
	h := gtx.Constraints.Max.Y
	layout.Inset{Left: theme.S4, Right: theme.S2}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.W.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min = image.Pt(gtx.Constraints.Max.X, 0)
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return appMark(gtx, th) }),
				layout.Rigid(layout.Spacer{Width: theme.S2}.Layout),
				layout.Rigid(bold(th, theme.Title, th.Text, brand.Name)),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, 0)}
				}),
				layout.Rigid(components.IconButtonSize(th, &m.settings, icons.Settings, "Settings", touchButton)),
			)
		})
	})
	return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, h)}
}

// pillLine is the compact layout's aggregate pill on its own line under
// the header; top is its window y.
func (m *Main) pillLine(gtx layout.Context, th *theme.Theme, snap *state.Snapshot, top int) layout.Dimensions {
	in := layout.Inset{Left: theme.S4, Right: theme.S4, Top: theme.S3, Bottom: theme.S1}
	return in.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min = image.Point{}
		d := aggregatePill(gtx, th, snap)
		x, y := gtx.Dp(theme.S4), top+gtx.Dp(theme.S3)
		m.PillBounds = image.Rect(x, y, x+d.Size.X, y+d.Size.Y)
		return d
	})
}

// layoutFAB draws the 56-dp floating Add button 16 dp from the bottom-right
// corner of gtx's area (the stack aligns it SE).
func (m *Main) layoutFAB(gtx layout.Context, th *theme.Theme) layout.Dimensions {
	full := gtx.Constraints.Max
	in := gtx.Dp(theme.S4)
	sz := gtx.Dp(fabSize)
	m.FABBounds = image.Rect(full.X-in-sz, full.Y-in-sz, full.X-in, full.Y-in)
	return layout.UniformInset(theme.S4).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = layout.Exact(image.Pt(sz, sz))
		return components.Raised(gtx, th, fabSize/2, func(gtx layout.Context) layout.Dimensions {
			return m.fab.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				semantic.DescriptionOp("Add download").Add(gtx.Ops)
				pointer.CursorPointer.Add(gtx.Ops)
				c := th.Accent
				if m.fab.Pressed() {
					c = theme.Mix(th.Accent, th.Text, 0.15)
				}
				paint.FillShape(gtx.Ops, c, clip.Ellipse{Max: image.Pt(sz, sz)}.Op(gtx.Ops))
				layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return icons.Plus(gtx, 24, th.OnAccent) })
				return layout.Dimensions{Size: image.Pt(sz, sz)}
			})
		})
	})
}

// page is the compact full-window details page (back arrow in its header).
func (m *Main) page(gtx layout.Context, th *theme.Theme, snap *state.Snapshot, now time.Time, emit func(Intent)) {
	m.listTop = -1
	m.HeaderHeight = 0
	paint.Fill(gtx.Ops, th.Surface)
	layout.Stack{Alignment: layout.S}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			for _, i := range m.details.Layout(gtx, th, snap, now) {
				m.handleDetails(i, emit)
			}
			return layout.Dimensions{Size: gtx.Constraints.Max}
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			if len(m.dismiss) < len(snap.Toasts) {
				m.dismiss = make([]widget.Clickable, len(snap.Toasts))
			}
			in := layout.UniformInset(theme.S4)
			in.Bottom += fabSize // clear the action bar
			return in.Layout(gtx, components.ToastStack(th, &m.toastFades, snap.Toasts, m.dismiss))
		}),
	)
	m.layoutMenu(gtx, th, snap, emit)
}
