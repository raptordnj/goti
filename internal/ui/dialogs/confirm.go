package dialogs

import (
	"time"

	"gioui.org/layout"
	"gioui.org/widget"

	"github.com/raptordnj/swiftfetch/internal/app"
	"github.com/raptordnj/swiftfetch/internal/domain"
	"github.com/raptordnj/swiftfetch/internal/ui/components"
	"github.com/raptordnj/swiftfetch/internal/ui/screens"
	"github.com/raptordnj/swiftfetch/internal/ui/state"
	"github.com/raptordnj/swiftfetch/internal/ui/theme"
)

// Confirm is the Cancel/Remove confirmation dialog.
type Confirm struct {
	// ShowsDeletePartial reports whether the checkbox was laid out last frame.
	ShowsDeletePartial bool

	kind                screens.ConfirmKind
	task                state.TaskView
	deletePartial       bool
	ok, cancel, partial widget.Clickable
}

// Open prepares the dialog for task v.
func (c *Confirm) Open(kind screens.ConfirmKind, v state.TaskView) {
	c.kind, c.task, c.deletePartial = kind, v, false
}

func (c *Confirm) unfinished() bool {
	return c.kind == screens.ConfirmRemove && c.task.State != domain.StateCompleted
}

// Layout draws the dialog; confirming emits the command and IntentCloseDialog.
func (c *Confirm) Layout(gtx layout.Context, th *theme.Theme, _ *state.Snapshot, _ time.Time) []screens.Intent {
	var out []screens.Intent
	c.ShowsDeletePartial = c.unfinished()
	if c.partial.Clicked(gtx) && c.ShowsDeletePartial {
		c.deletePartial = !c.deletePartial
	}
	if c.cancel.Clicked(gtx) {
		out = append(out, screens.IntentCloseDialog{})
	}
	if c.ok.Clicked(gtx) {
		var cmd app.Command = app.CancelTask{ID: c.task.ID}
		if c.kind == screens.ConfirmRemove {
			cmd = app.RemoveTask{ID: c.task.ID, DeletePartial: c.deletePartial && c.ShowsDeletePartial}
		}
		out = append(out, screens.IntentCommand{Cmd: cmd}, screens.IntentCloseDialog{})
	}
	title, msg, okTxt := "Cancel download?", "Stop downloading "+c.task.FileName+"?", "Cancel download"
	if c.kind == screens.ConfirmRemove {
		title, msg, okTxt = "Remove download?", "Remove "+c.task.FileName+" from the list?", "Remove"
	}
	body := screens.Column(theme.S3,
		screens.Text(th, theme.TextMd, th.Text, msg, 3),
		func(gtx layout.Context) layout.Dimensions {
			if !c.ShowsDeletePartial {
				return layout.Dimensions{}
			}
			box := "[ ]"
			if c.deletePartial {
				box = "[x]"
			}
			return components.Button(th, &c.partial, box+" Also delete partial data", components.Ghost)(gtx)
		},
	)
	actions := screens.Row(components.Button(th, &c.cancel, "Keep", components.Ghost),
		components.Button(th, &c.ok, okTxt, components.Danger))
	modal(gtx, th, components.DialogShell(th, title, body, actions))
	return out
}
