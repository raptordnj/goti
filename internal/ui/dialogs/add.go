// Package dialogs holds Goti's modal dialogs. Like screens they are
// passive: they keep widget state and return screens.Intent values.
package dialogs

import (
	"errors"
	"net/url"
	"strings"
	"time"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/widget"

	"github.com/raptordnj/goti/internal/app"
	"github.com/raptordnj/goti/internal/ui/components"
	"github.com/raptordnj/goti/internal/ui/icons"
	"github.com/raptordnj/goti/internal/ui/screens"
	"github.com/raptordnj/goti/internal/ui/state"
	"github.com/raptordnj/goti/internal/ui/theme"
)

// BadURL is the inline URL validation message.
const BadURL = "Use an absolute http or https URL"

// validURL accepts absolute http/https URLs with a host.
func validURL(s string) error {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New(BadURL)
	}
	return nil
}

// Add is the Add download dialog.
type Add struct {
	url, name           widget.Editor
	destDir             string
	urlErr, submitErr   string
	add, cancel, change widget.Clickable
	shell               components.DialogState
	pasted              string // prefill, for the paste-detected hint
	focusPending        bool
}

// PastedHint is shown under the URL field while it holds the prefilled link.
const PastedHint = "Link detected from the clipboard"

// Open resets the dialog with a prefilled URL and default folder.
func (a *Add) Open(prefill, defaultDir string) {
	a.url.SingleLine, a.url.Submit = true, true
	a.name.SingleLine, a.name.Submit = true, true
	a.url.SetText(prefill)
	a.name.SetText("")
	a.destDir = defaultDir
	a.urlErr, a.submitErr = "", ""
	a.shell = components.DialogState{}
	a.pasted, a.focusPending = prefill, true
}

// SetDestDir sets the destination folder (folder picker result).
func (a *Add) SetDestDir(path string) { a.destDir = path }

// SetError shows an error returned by the App for the submitted command.
func (a *Add) SetError(msg string) { a.submitErr = msg }

// editorEvents drains e's events, reporting submit and text change.
func editorEvents(gtx layout.Context, e *widget.Editor) (submit, changed bool) {
	for {
		ev, ok := e.Update(gtx)
		if !ok {
			return submit, changed
		}
		switch ev.(type) {
		case widget.SubmitEvent:
			submit = true
		case widget.ChangeEvent:
			changed = true
		}
	}
}

// Layout draws the dialog and returns intents.
func (a *Add) Layout(gtx layout.Context, th *theme.Theme, _ *state.Snapshot, _ time.Time) []screens.Intent {
	var out []screens.Intent
	if a.change.Clicked(gtx) {
		out = append(out, screens.IntentPickFolder{})
	}
	if a.cancel.Clicked(gtx) {
		out = append(out, screens.IntentCloseDialog{})
	}
	sub, changed := editorEvents(gtx, &a.url)
	if changed {
		a.urlErr = ""
	}
	sub2, _ := editorEvents(gtx, &a.name)
	sub = sub || sub2
	if a.add.Clicked(gtx) || sub {
		u := strings.TrimSpace(a.url.Text())
		if err := validURL(u); err != nil {
			a.urlErr = err.Error()
		} else {
			a.urlErr, a.submitErr = "", ""
			out = append(out, screens.IntentCommand{Cmd: app.AddDownload{URL: u, DestDir: a.destDir, FileName: strings.TrimSpace(a.name.Text())}})
		}
	}
	if a.focusPending {
		a.focusPending = false
		gtx.Execute(key.FocusCmd{Tag: &a.url})
	}
	urlHint := func(gtx layout.Context) layout.Dimensions {
		if a.urlErr != "" || a.pasted == "" || a.url.Text() != a.pasted {
			return layout.Dimensions{}
		}
		return layout.Inset{Top: theme.S1}.Layout(gtx, screens.Label(th, theme.Caption, th.TextMuted, PastedHint))
	}
	body := screens.Column(theme.S4,
		screens.Column(0, components.TextField(th, &a.url, "URL", "https://example.com/file.iso", a.urlErr), recordIf(a.urlErr), urlHint),
		screens.Column(theme.S1+2,
			screens.Label(th, theme.Caption, th.TextMuted, "Save to"),
			a.destRow(th),
		),
		components.TextField(th, &a.name, "File name (optional)", "Detected from the server", ""),
		func(gtx layout.Context) layout.Dimensions {
			if a.submitErr == "" {
				return layout.Dimensions{}
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return icons.Alert(gtx, 13, th.Error) }),
				layout.Rigid(layout.Spacer{Width: theme.S1 + 1}.Layout),
				layout.Flexed(1, screens.Text(th, theme.Caption, th.Error, a.submitErr, 3)),
			)
		},
	)
	actions := screens.Row(components.Button(th, &a.cancel, "Cancel", components.Secondary),
		components.Button(th, &a.add, "Add", components.Primary, components.WithIcon(icons.Plus)))
	a.shell.Fade.Show(true)
	modal(gtx, th, components.Dialog(th, &a.shell, "Add download", body, actions))
	return out
}

// destRow is the destination folder: a sunken strip with a folder icon,
// the middle-ellipsized path and a "Change…" button.
func (a *Add) destRow(th *theme.Theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		screens.Note(a.destDir)
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return icons.Folder(gtx, 18, th.TextMuted) }),
			layout.Rigid(layout.Spacer{Width: theme.S2}.Layout),
			layout.Flexed(1, components.PathLabel(th, theme.StyleBody, th.Text, a.destDir)),
			layout.Rigid(layout.Spacer{Width: theme.S2}.Layout),
			layout.Rigid(components.Button(th, &a.change, "Change…", components.Secondary, components.Small())),
		)
	}
}

// recordIf records s for test text assertions (TextField draws it).
func recordIf(s string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		if s != "" {
			screens.Note(s)
		}
		return layout.Dimensions{}
	}
}

// modal lays out a full-window dialog shell (scrim + centred card).
func modal(gtx layout.Context, _ *theme.Theme, w layout.Widget) layout.Dimensions {
	gtx.Constraints.Min = gtx.Constraints.Max
	return w(gtx)
}

// HasFocus reports whether one of the dialog's text fields has keyboard focus.
func (a *Add) HasFocus(gtx layout.Context) bool {
	return gtx.Focused(&a.url) || gtx.Focused(&a.name)
}

// URLEmpty reports whether the URL field is empty.
func (a *Add) URLEmpty() bool { return strings.TrimSpace(a.url.Text()) == "" }
