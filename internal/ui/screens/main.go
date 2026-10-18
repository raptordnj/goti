package screens

import (
	"fmt"
	"image"
	"image/color"
	"time"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/raptordnj/goti/internal/app"
	"github.com/raptordnj/goti/internal/brand"
	"github.com/raptordnj/goti/internal/platform"
	"github.com/raptordnj/goti/internal/ui/components"
	"github.com/raptordnj/goti/internal/ui/state"
	"github.com/raptordnj/goti/internal/ui/theme"
)

// Breakpoint is the width at and above which details show as a right pane.
const Breakpoint unit.Dp = 1000

// detailsPaneWidth is the width of the details pane in wide layouts.
const detailsPaneWidth unit.Dp = 380

// DetailsMode is how the details panel was laid out in the last frame.
type DetailsMode int

// Details layout modes.
const (
	DetailsNone DetailsMode = iota
	DetailsPane
	DetailsSheet
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

	add, pauseAll, settings widget.Clickable
	list                    widget.List
	rows                    map[string]*components.RowWidgets
	menuFor                 string // task whose overflow menu is open
	collapsed               [state.NumSections]widget.Bool
	listTop                 int   // window y of the task list (px)
	itemH                   []int // heights of list items laid out last frame
	details                 Details
	dismiss                 []widget.Clickable
	toastFades              components.ToastFades
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
	m.DetailsMode = DetailsNone
	if sel != nil {
		m.DetailsMode = DetailsSheet
		if wide {
			m.DetailsMode = DetailsPane
		}
		m.details.ID = sel.ID
	}

	paint.Fill(gtx.Ops, th.Bg)
	m.ShownSections = m.ShownSections[:0]
	m.ShownTasks = m.ShownTasks[:0]
	m.listTop = -1
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
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			if len(m.dismiss) < len(snap.Toasts) {
				m.dismiss = make([]widget.Clickable, len(snap.Toasts))
			}
			return layout.UniformInset(theme.S4).Layout(gtx, components.ToastStack(th, &m.toastFades, snap.Toasts, m.dismiss))
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
	switch m.DetailsMode {
	case DetailsSheet:
		for _, i := range m.details.Layout(gtx, th, snap, now) {
			m.handleDetails(i, emit)
		}
		return layout.Dimensions{Size: gtx.Constraints.Max}
	case DetailsPane:
		return layout.Flex{}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return m.taskList(gtx, th, snap, now, top) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(detailsPaneWidth), gtx.Constraints.Max.Y))
				for _, i := range m.details.Layout(gtx, th, snap, now) {
					m.handleDetails(i, emit)
				}
				return layout.Dimensions{Size: gtx.Constraints.Max}
			}),
		)
	}
	return m.taskList(gtx, th, snap, now, top)
}

// handleDetails forwards details intents, clearing the selection on close.
func (m *Main) handleDetails(i Intent, emit func(Intent)) {
	if s, ok := i.(IntentSelect); ok && s.ID == "" {
		m.Selected = ""
	}
	emit(i)
}

func (m *Main) header(gtx layout.Context, th *theme.Theme, snap *state.Snapshot) layout.Dimensions {
	agg := fmt.Sprintf("%d active · %s", snap.ActiveCount, platform.FormatSpeed(snap.TotalSpeedBps))
	allTxt := "Pause all"
	if snap.QueuePaused {
		allTxt = "Resume all"
	}
	return layout.UniformInset(theme.S4).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(bold(th, theme.Display, th.Text, brand.Name)),
			layout.Rigid(layout.Spacer{Width: theme.S3}.Layout),
			layout.Flexed(1, Label(th, theme.Caption, th.TextMuted, agg)),
			layout.Rigid(Row(
				components.Button(th, &m.add, "Add download", components.Primary),
				components.Button(th, &m.pauseAll, allTxt, components.Secondary),
				components.Button(th, &m.settings, "Settings", components.Ghost),
			)),
		)
	})
}

// listItem is a flattened list entry: a section header or a task row.
type listItem struct {
	header bool
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
	return out
}

func (m *Main) taskList(gtx layout.Context, th *theme.Theme, snap *state.Snapshot, now time.Time, top int) layout.Dimensions {
	if len(snap.Tasks) == 0 {
		return emptyState(th, &m.add)(gtx)
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

func (m *Main) listItem(gtx layout.Context, th *theme.Theme, snap *state.Snapshot, now time.Time, it listItem, first bool) layout.Dimensions {
	if it.header {
		in := layout.Inset{Top: theme.S3}
		if first {
			in.Top = theme.S1
		}
		return in.Layout(gtx, components.SectionHeader(th, sectionTitles[it.sec], snap.Counts[it.sec], m.Collapsible(it.sec)))
	}
	v := snap.Tasks[it.task]
	return layout.Inset{Bottom: theme.S0}.Layout(gtx, components.TaskRow(th, v, m.Row(v.ID), now, v.ID == m.Selected))
}

// events converts last frame's clicks into intents.
func (m *Main) events(gtx layout.Context, snap *state.Snapshot, emit func(Intent)) {
	if m.add.Clicked(gtx) {
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
	if w.Select.Clicked(gtx) {
		m.Selected = v.ID
		emit(IntentSelect{ID: v.ID})
	}
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
