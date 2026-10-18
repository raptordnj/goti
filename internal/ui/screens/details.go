package screens

import (
	"fmt"
	"image"
	"strconv"
	"time"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/raptordnj/goti/internal/app"
	"github.com/raptordnj/goti/internal/domain"
	"github.com/raptordnj/goti/internal/platform"
	"github.com/raptordnj/goti/internal/ui/components"
	"github.com/raptordnj/goti/internal/ui/icons"
	"github.com/raptordnj/goti/internal/ui/state"
	"github.com/raptordnj/goti/internal/ui/theme"
)

// maxRetriesShown caps the retry history in the details panel.
const maxRetriesShown = 10

// Details geometry.
const (
	detailsTile   unit.Dp = 36
	labelColumn   unit.Dp = 96
	detailsLabel  unit.Sp = 12
	detailsInsetX         = theme.S4
)

// Details is the details panel of one task (pane or sheet; Main decides).
type Details struct {
	ID string

	close, pause, resume, cancel, retry, restart, open, folder, copyURL, remove widget.Clickable
	list                                                                        widget.List
}

// Layout draws the details of task d.ID over the full constraints and
// returns intents. Close emits IntentSelect{ID: ""}. The caller paints the
// surface (pane or sheet chrome).
func (d *Details) Layout(gtx layout.Context, th *theme.Theme, snap *state.Snapshot, now time.Time) []Intent {
	var v *state.TaskView
	for i := range snap.Tasks {
		if snap.Tasks[i].ID == d.ID {
			v = &snap.Tasks[i]
		}
	}
	if v == nil {
		return nil
	}
	out := d.events(gtx, *v)
	gtx.Constraints.Min = gtx.Constraints.Max
	groups := d.groups(th, *v)
	d.list.Axis = layout.Vertical
	layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: theme.S4, Bottom: theme.S3, Left: detailsInsetX, Right: theme.S3}.Layout(gtx, d.header(th, *v, now))
		}),
		layout.Rigid(hairline(th)),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return d.list.Layout(gtx, len(groups), func(gtx layout.Context, i int) layout.Dimensions {
				in := layout.Inset{Top: theme.S4, Left: detailsInsetX, Right: detailsInsetX}
				if i == len(groups)-1 {
					in.Bottom = theme.S4
				}
				return in.Layout(gtx, groups[i])
			})
		}),
		layout.Rigid(hairline(th)),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: theme.S3, Bottom: theme.S3, Left: detailsInsetX, Right: theme.S3}.Layout(gtx, d.actions(th, *v))
		}),
	)
	return out
}

func (d *Details) events(gtx layout.Context, v state.TaskView) []Intent {
	var out []Intent
	cmd := func(c *widget.Clickable, ok bool, i Intent) {
		if c.Clicked(gtx) && ok {
			out = append(out, i)
		}
	}
	cmd(&d.close, true, IntentSelect{})
	cmd(&d.pause, v.CanPause, IntentCommand{Cmd: app.PauseTask{ID: v.ID}})
	cmd(&d.resume, v.CanResume, IntentCommand{Cmd: app.ResumeTask{ID: v.ID}})
	cmd(&d.cancel, v.CanCancel, IntentConfirm{Kind: ConfirmCancel, ID: v.ID})
	cmd(&d.retry, v.CanRetry, IntentCommand{Cmd: app.RetryTask{ID: v.ID}})
	cmd(&d.restart, v.CanRestart, IntentCommand{Cmd: app.RestartTask{ID: v.ID}})
	cmd(&d.open, v.CanOpen, IntentOpen{Path: v.DestPath})
	cmd(&d.folder, true, IntentOpen{Path: v.DestPath, Reveal: true})
	cmd(&d.copyURL, true, IntentCopy{Text: v.URL})
	cmd(&d.remove, true, IntentConfirm{Kind: ConfirmRemove, ID: v.ID})
	return out
}

// header: file tile, name (wraps to 2 lines), state dot + text, close.
func (d *Details) header(th *theme.Theme, v state.TaskView, now time.Time) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		col := theme.StateColor(th.Palette, v.State, v.Interrupted)
		stCol := th.TextMuted
		if v.State == domain.StateFailed {
			stCol = th.Error
		}
		return layout.Flex{Alignment: layout.Start}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				sz := image.Pt(gtx.Dp(detailsTile), gtx.Dp(detailsTile))
				paint.FillShape(gtx.Ops, th.SurfaceSunken, clip.UniformRRect(image.Rectangle{Max: sz}, gtx.Dp(theme.RadiusRow)).Op(gtx.Ops))
				gtx.Constraints = layout.Exact(sz)
				layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return icons.ForFile(v.FileName)(gtx, 18, th.TextMuted)
				})
				return layout.Dimensions{Size: sz}
			}),
			layout.Rigid(layout.Spacer{Width: theme.S3}.Layout),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						record(v.FileName)
						l := labelStyle(th, theme.Title, th.Text, v.FileName)
						l.Font.Weight = theme.StyleTitle.Weight
						l.MaxLines = 2
						return l.Layout(gtx)
					}),
					layout.Rigid(layout.Spacer{Height: theme.S1}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								r := gtx.Dp(6)
								paint.FillShape(gtx.Ops, col, clip.Ellipse{Max: image.Pt(r, r)}.Op(gtx.Ops))
								return layout.Dimensions{Size: image.Pt(r+gtx.Dp(6), r)}
							}),
							layout.Rigid(Label(th, theme.Caption, stCol, state.StatusText(v, now))),
						)
					}),
				)
			}),
			layout.Rigid(layout.Spacer{Width: theme.S2}.Layout),
			layout.Rigid(components.IconButton(th, &d.close, icons.Close, "Close details")),
		)
	}
}

// kv is one key/value pair; slot values keep a fixed (zero-digit) width.
type kv struct {
	key, value string
	numeric    bool
}

// group is a titled block of key/value rows.
func group(th *theme.Theme, title string, rows []kv) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		ch := []layout.FlexChild{
			layout.Rigid(bold(th, theme.Caption, th.TextMuted, title)),
			layout.Rigid(layout.Spacer{Height: theme.S2}.Layout),
		}
		for i, r := range rows {
			if i > 0 {
				ch = append(ch, layout.Rigid(layout.Spacer{Height: theme.S2}.Layout))
			}
			ch = append(ch, layout.Rigid(kvRow(th, r)))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
	}
}

func kvRow(th *theme.Theme, r kv) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				w := gtx.Dp(labelColumn)
				gtx.Constraints.Min.X, gtx.Constraints.Max.X = w, w
				return Label(th, detailsLabel, th.TextMuted, r.key)(gtx)
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				if r.numeric {
					return slot(th, theme.Body, th.Text, r.value)(gtx)
				}
				return Text(th, theme.Body, th.Text, r.value, 0)(gtx)
			}),
		)
	}
}

func (d *Details) groups(th *theme.Theme, v state.TaskView) []layout.Widget {
	size := "Unknown"
	if v.TotalKnown {
		size = platform.FormatBytes(v.Total)
	}
	ranges, resume := "No", "Not supported"
	if v.RangesSupported {
		ranges = "Yes"
	}
	if v.Resumable {
		resume = "Supported"
	}
	transfer := []kv{
		{"Size", size, true},
		{"Downloaded", platform.FormatBytes(v.Downloaded), true},
		{"Speed", platform.FormatSpeed(v.SpeedBps), true},
		{"Connections", strconv.Itoa(v.Conns), true},
	}
	if v.ChunksTotal > 0 {
		transfer = append(transfer, kv{"Chunks", fmt.Sprintf("%d of %d done", v.ChunksDone, v.ChunksTotal), true})
	}
	if v.Strategy != "" {
		strat := v.Strategy
		if v.DecisionSummary != "" {
			strat += metaSep + v.DecisionSummary
		}
		transfer = append(transfer, kv{"Strategy", strat, false})
	}
	source := []kv{
		{"URL", v.URL, false},
		{"Destination", v.DestPath, false},
		{"Range support", ranges, false},
		{"Resume", resume, false},
	}
	verify := v.VerifyResult
	if verify == "" {
		verify = "Not verified"
	}
	ws := []layout.Widget{
		group(th, "Transfer", transfer),
		group(th, "Source", source),
		group(th, "Integrity", []kv{{"Checksum", verify, false}}),
	}
	var hist []kv
	if v.LastError != "" {
		hist = append(hist, kv{"Last error", v.LastError, false})
		if v.LastErrorAction != "" {
			hist = append(hist, kv{"Suggested action", v.LastErrorAction, false})
		}
	}
	if n := len(v.Retries); n > 0 {
		for _, a := range v.Retries[max(0, n-maxRetriesShown):] {
			hist = append(hist, kv{fmt.Sprintf("Attempt %d", a.Attempt), a.Kind + metaSep + a.Message, false})
		}
	}
	if len(hist) > 0 {
		ws = append(ws, group(th, "History", hist))
	}
	return ws
}

// metaSep separates inline values (Inter covers the middle dot).
const metaSep = " · "

// actions is the bottom action row: CanX-gated text buttons on the left,
// always-available icon actions on the right.
func (d *Details) actions(th *theme.Theme, v state.TaskView) layout.Widget {
	var ws []layout.Widget
	add := func(ok bool, c *widget.Clickable, txt string, k components.ButtonKind, ic icons.Icon) {
		if ok {
			ws = append(ws, components.Button(th, c, txt, k, components.Small(), components.WithIcon(ic)))
		}
	}
	add(v.CanResume, &d.resume, "Resume", components.Primary, icons.Play)
	add(v.CanRetry, &d.retry, "Retry", components.Primary, icons.Retry)
	add(v.CanOpen, &d.open, "Open", components.Primary, icons.File)
	add(v.CanPause, &d.pause, "Pause", components.Secondary, icons.Pause)
	add(v.CanRestart, &d.restart, "Restart", components.Secondary, icons.Restart)
	add(v.CanCancel, &d.cancel, "Cancel", components.Secondary, icons.Close)
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(Row(ws...)),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Dimensions{Size: image.Pt(gtx.Constraints.Min.X, 0)}
			}),
			layout.Rigid(components.IconButton(th, &d.folder, icons.Folder, "Open folder")),
			layout.Rigid(components.IconButton(th, &d.copyURL, icons.Copy, "Copy URL")),
			layout.Rigid(components.IconButton(th, &d.remove, icons.Trash, "Remove…")),
		)
	}
}

// hairline is a full-width 1-dp divider.
func hairline(th *theme.Theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		sz := image.Pt(gtx.Constraints.Max.X, max(1, gtx.Dp(1)))
		paint.FillShape(gtx.Ops, th.Hairline, clip.Rect{Max: sz}.Op())
		return layout.Dimensions{Size: sz}
	}
}
