package screens

import (
	"fmt"
	"strconv"
	"time"

	"gioui.org/layout"
	"gioui.org/widget"

	"github.com/raptordnj/swiftfetch/internal/app"
	"github.com/raptordnj/swiftfetch/internal/platform"
	"github.com/raptordnj/swiftfetch/internal/ui/components"
	"github.com/raptordnj/swiftfetch/internal/ui/state"
	"github.com/raptordnj/swiftfetch/internal/ui/theme"
)

// maxRetriesShown caps the retry history in the details panel.
const maxRetriesShown = 10

// Details is the details panel of one task (pane or sheet; Main decides).
type Details struct {
	ID string

	close, pause, resume, cancel, retry, restart, open, folder, copyURL, remove widget.Clickable
	list                                                                        widget.List
}

// Layout draws the details of task d.ID and returns intents. Close emits
// IntentSelect{ID: ""}.
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
	layout.Background{}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return fill(gtx, th.Surface)
	}, func(gtx layout.Context) layout.Dimensions {
		ws := d.content(th, *v, now)
		d.list.Axis = layout.Vertical
		return layout.UniformInset(theme.S4).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return d.list.Layout(gtx, len(ws), func(gtx layout.Context, i int) layout.Dimensions {
				return layout.Inset{Bottom: theme.S3}.Layout(gtx, ws[i])
			})
		})
	})
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

// field is a muted caption above a wrapping value.
func field(th *theme.Theme, name, value string) layout.Widget {
	return Column(theme.S1, Label(th, theme.Caption, th.TextMuted, name), Text(th, theme.Body, th.Text, value, 0))
}

func (d *Details) content(th *theme.Theme, v state.TaskView, now time.Time) []layout.Widget {
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
	ws := []layout.Widget{
		Row(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Max.X -= gtx.Dp(theme.S6 * 2)
			return bold(th, theme.Title, th.Text, v.FileName)(gtx)
		}, components.Button(th, &d.close, "Close", components.Ghost)),
		Label(th, theme.Caption, th.TextMuted, state.StatusText(v, now)),
		field(th, "URL", v.URL),
		field(th, "Destination", v.DestPath),
		field(th, "Size", size),
		field(th, "Downloaded", platform.FormatBytes(v.Downloaded)),
		field(th, "Speed", platform.FormatSpeed(v.SpeedBps)),
		field(th, "Connections", strconv.Itoa(v.Conns)),
	}
	if v.Strategy != "" {
		ws = append(ws, field(th, "Strategy", v.Strategy+metaSep+v.DecisionSummary))
	}
	ws = append(ws, field(th, "Range support", ranges))
	if v.ChunksTotal > 0 {
		ws = append(ws, field(th, "Chunks", fmt.Sprintf("%d of %d done", v.ChunksDone, v.ChunksTotal)))
	}
	ws = append(ws, field(th, "Resume", resume))
	if v.VerifyResult != "" {
		ws = append(ws, field(th, "Integrity", v.VerifyResult))
	}
	if v.LastError != "" {
		ws = append(ws, field(th, "Last error", v.LastError))
		if v.LastErrorAction != "" {
			ws = append(ws, field(th, "Suggested action", v.LastErrorAction))
		}
	}
	if n := len(v.Retries); n > 0 {
		ws = append(ws, Label(th, theme.Caption, th.TextMuted, "Retry history"))
		for _, a := range v.Retries[max(0, n-maxRetriesShown):] {
			ws = append(ws, Text(th, theme.Caption, th.Text,
				fmt.Sprintf("#%d %s%s%s", a.Attempt, a.Kind, metaSep, a.Message), 2))
		}
	}
	return append(ws, d.buttons(th, v))
}

// metaSep separates inline values (Go-font covered).
const metaSep = " · "

func (d *Details) buttons(th *theme.Theme, v state.TaskView) layout.Widget {
	var ws []layout.Widget
	add := func(ok bool, c *widget.Clickable, txt string, k components.ButtonKind) {
		if ok {
			ws = append(ws, components.Button(th, c, txt, k))
		}
	}
	add(v.CanPause, &d.pause, "Pause", components.Secondary)
	add(v.CanResume, &d.resume, "Resume", components.Primary)
	add(v.CanRetry, &d.retry, "Retry", components.Primary)
	add(v.CanRestart, &d.restart, "Restart", components.Secondary)
	add(v.CanOpen, &d.open, "Open file", components.Primary)
	add(v.CanCancel, &d.cancel, "Cancel", components.Danger)
	row1 := Row(ws...)
	row2 := Row(
		components.Button(th, &d.folder, "Open folder", components.Ghost),
		components.Button(th, &d.copyURL, "Copy URL", components.Ghost),
		components.Button(th, &d.remove, "Remove…", components.Danger),
	)
	return Column(theme.S2, row1, row2)
}
