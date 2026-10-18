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

// rowMenu is the overflow menu of one row.
type rowMenu struct {
	openFile, openFolder, copyURL, copyPath, remove widget.Clickable
}

// Main is the main screen: header, offline banner, sectioned task list,
// details pane/sheet and toasts.
type Main struct {
	// Selected is the task whose details are shown ("" = none).
	Selected string
	// ShownSections lists the section headers laid out last frame.
	ShownSections []state.Section
	// DetailsMode records how details were laid out last frame.
	DetailsMode DetailsMode

	add, pauseAll, settings widget.Clickable
	list                    widget.List
	rows                    map[string]*components.RowWidgets
	menuFor                 string
	menu                    rowMenu
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
	layout.Stack{Alignment: layout.SE}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return m.header(gtx, th, snap) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if !snap.Offline {
						return layout.Dimensions{}
					}
					return offlineBanner(th)(gtx)
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return m.body(gtx, th, snap, now, emit)
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
	return out
}

func (m *Main) selectedView(snap *state.Snapshot) *state.TaskView {
	if m.Selected == "" {
		return nil
	}
	for i := range snap.Tasks {
		if snap.Tasks[i].ID == m.Selected {
			return &snap.Tasks[i]
		}
	}
	return nil
}

func (m *Main) body(gtx layout.Context, th *theme.Theme, snap *state.Snapshot, now time.Time, emit func(Intent)) layout.Dimensions {
	switch m.DetailsMode {
	case DetailsSheet:
		for _, i := range m.details.Layout(gtx, th, snap, now) {
			m.handleDetails(i, emit)
		}
		return layout.Dimensions{Size: gtx.Constraints.Max}
	case DetailsPane:
		return layout.Flex{}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return m.taskList(gtx, th, snap, now) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(detailsPaneWidth), gtx.Constraints.Max.Y))
				for _, i := range m.details.Layout(gtx, th, snap, now) {
					m.handleDetails(i, emit)
				}
				return layout.Dimensions{Size: gtx.Constraints.Max}
			}),
		)
	}
	return m.taskList(gtx, th, snap, now)
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
		out = append(out, listItem{sec: v.Section, task: i})
	}
	return out
}

func (m *Main) taskList(gtx layout.Context, th *theme.Theme, snap *state.Snapshot, now time.Time) layout.Dimensions {
	if len(snap.Tasks) == 0 {
		return emptyState(th, &m.add)(gtx)
	}
	items := m.items(snap)
	for _, it := range items {
		if it.header {
			m.ShownSections = append(m.ShownSections, it.sec)
		}
	}
	m.list.Axis = layout.Vertical
	return layout.Inset{Left: theme.S4, Right: theme.S4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return m.list.Layout(gtx, len(items), func(gtx layout.Context, i int) layout.Dimensions {
			it := items[i]
			if it.header {
				return components.SectionHeader(th, sectionTitles[it.sec], snap.Counts[it.sec])(gtx)
			}
			v := snap.Tasks[it.task]
			return layout.Inset{Bottom: theme.S2}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(components.TaskRow(th, v, m.Row(v.ID), now)),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if m.menuFor != v.ID {
							return layout.Dimensions{}
						}
						return m.menuStrip(gtx, th, v)
					}),
				)
			})
		})
	})
}

func (m *Main) menuStrip(gtx layout.Context, th *theme.Theme, v state.TaskView) layout.Dimensions {
	ws := []layout.Widget{}
	if v.CanOpen {
		ws = append(ws, components.Button(th, &m.menu.openFile, "Open file", components.Ghost))
	}
	ws = append(ws,
		components.Button(th, &m.menu.openFolder, "Open folder", components.Ghost),
		components.Button(th, &m.menu.copyURL, "Copy URL", components.Ghost),
		components.Button(th, &m.menu.copyPath, "Copy path", components.Ghost),
		components.Button(th, &m.menu.remove, "Remove…", components.Danger),
	)
	return layout.Inset{Top: theme.S1, Left: theme.S3}.Layout(gtx, Row(ws...))
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
	cmd(&w.Restart, v.CanRestart, func() app.Command { return app.RestartTask{ID: v.ID} })
	if w.Cancel.Clicked(gtx) && v.CanCancel {
		emit(IntentConfirm{Kind: ConfirmCancel, ID: v.ID})
	}
	if w.Open.Clicked(gtx) && v.CanOpen {
		emit(IntentOpen{Path: v.DestPath})
	}
	if w.Select.Clicked(gtx) {
		m.Selected = v.ID
		emit(IntentSelect{ID: v.ID})
	}
	if w.Menu.Clicked(gtx) {
		if m.menuFor == v.ID {
			m.menuFor = ""
		} else {
			m.menuFor = v.ID
		}
	}
	if m.menuFor != v.ID {
		return
	}
	mu := &m.menu
	switch {
	case mu.openFile.Clicked(gtx) && v.CanOpen:
		emit(IntentOpen{Path: v.DestPath})
	case mu.openFolder.Clicked(gtx):
		emit(IntentOpen{Path: v.DestPath, Reveal: true})
	case mu.copyURL.Clicked(gtx):
		emit(IntentCopy{Text: v.URL})
	case mu.copyPath.Clicked(gtx):
		emit(IntentCopy{Text: v.DestPath})
	case mu.remove.Clicked(gtx):
		emit(IntentConfirm{Kind: ConfirmRemove, ID: v.ID})
	default:
		return
	}
	m.menuFor = ""
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
