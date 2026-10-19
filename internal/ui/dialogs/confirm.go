package dialogs

import (
	"time"

	"gioui.org/layout"
	"gioui.org/widget"

	"github.com/raptordnj/goti/internal/app"
	"github.com/raptordnj/goti/internal/domain"
	"github.com/raptordnj/goti/internal/platform"
	"github.com/raptordnj/goti/internal/ui/components"
	"github.com/raptordnj/goti/internal/ui/icons"
	"github.com/raptordnj/goti/internal/ui/screens"
	"github.com/raptordnj/goti/internal/ui/state"
	"github.com/raptordnj/goti/internal/ui/theme"
)

// Confirm is the Cancel/Remove confirmation dialog.
type Confirm struct {
	// ShowsDeletePartial reports whether the switch was laid out last frame.
	ShowsDeletePartial bool

	kind       screens.ConfirmKind
	task       state.TaskView
	partial    widget.Bool
	ok, cancel widget.Clickable
	shell      components.DialogState
}

// Open prepares the dialog for task v.
func (c *Confirm) Open(kind screens.ConfirmKind, v state.TaskView) {
	c.kind, c.task, c.partial = kind, v, widget.Bool{}
	c.shell = components.DialogState{Insets: c.shell.Insets}
}

func (c *Confirm) unfinished() bool {
	return c.kind == screens.ConfirmRemove && c.task.State != domain.StateCompleted
}

// Layout draws the dialog; confirming emits the command and IntentCloseDialog.
func (c *Confirm) Layout(gtx layout.Context, th *theme.Theme, _ *state.Snapshot, _ time.Time) []screens.Intent {
	var out []screens.Intent
	c.ShowsDeletePartial = c.unfinished()
	if c.cancel.Clicked(gtx) {
		out = append(out, screens.IntentCloseDialog{})
	}
	if c.ok.Clicked(gtx) {
		var cmd app.Command = app.CancelTask{ID: c.task.ID}
		if c.kind == screens.ConfirmRemove {
			cmd = app.RemoveTask{ID: c.task.ID, DeletePartial: c.partial.Value && c.ShowsDeletePartial}
		}
		out = append(out, screens.IntentCommand{Cmd: cmd}, screens.IntentCloseDialog{})
	}
	title, msg, okTxt := "Cancel download?", "The download stops and its partial data is kept until you remove it.", "Cancel download"
	if c.kind == screens.ConfirmRemove {
		title, msg, okTxt = "Remove download?", "It will be removed from the list. Completed files stay on disk.", "Remove"
	}
	size := "Size unknown"
	if c.task.TotalKnown {
		size = platform.FormatBytes(c.task.Total)
		if c.task.State != domain.StateCompleted {
			size = platform.FormatBytes(c.task.Downloaded) + " of " + size
		}
	}
	file := func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Background{}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			sz := gtx.Constraints.Min
			return components.RoundedFill(gtx, th.SurfaceSunken, sz, theme.RadiusRow)
		}, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(theme.S3).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return icons.ForFile(c.task.FileName)(gtx, 20, th.TextMuted)
					}),
					layout.Rigid(layout.Spacer{Width: theme.S3}.Layout),
					layout.Flexed(1, screens.Column(theme.S0,
						screens.Text(th, theme.Body, th.Text, c.task.FileName, 2),
						screens.Label(th, theme.Caption, th.TextMuted, size))),
				)
			})
		})
	}
	body := screens.Column(theme.S3,
		file,
		screens.Text(th, theme.Body, th.TextMuted, msg, 3),
		func(gtx layout.Context) layout.Dimensions {
			if !c.ShowsDeletePartial {
				return layout.Dimensions{}
			}
			screens.Note(DeletePartialLabel)
			return components.Switch(th, &c.partial, DeletePartialLabel)(gtx)
		},
	)
	actions := screens.Row(components.Button(th, &c.cancel, "Keep", components.Secondary),
		components.Button(th, &c.ok, okTxt, components.Danger))
	c.shell.Fade.Show(true)
	modal(gtx, th, components.Dialog(th, &c.shell, title, body, actions))
	return out
}

// DeletePartialLabel labels the remove dialog's partial-data switch.
const DeletePartialLabel = "Also delete partial data"

// SetInsets keeps the dialog inside the window's system insets.
func (c *Confirm) SetInsets(in layout.Inset) { c.shell.Insets = in }
