// Package dialogs holds SwiftFetch's modal dialogs. Like screens they are
// passive: they keep widget state and return screens.Intent values.
package dialogs

import (
	"errors"
	"net/url"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/widget"

	"github.com/raptordnj/swiftfetch/internal/app"
	"github.com/raptordnj/swiftfetch/internal/ui/components"
	"github.com/raptordnj/swiftfetch/internal/ui/screens"
	"github.com/raptordnj/swiftfetch/internal/ui/state"
	"github.com/raptordnj/swiftfetch/internal/ui/theme"
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
}

// Open resets the dialog with a prefilled URL and default folder.
func (a *Add) Open(prefill, defaultDir string) {
	a.url.SingleLine, a.url.Submit = true, true
	a.name.SingleLine, a.name.Submit = true, true
	a.url.SetText(prefill)
	a.name.SetText("")
	a.destDir = defaultDir
	a.urlErr, a.submitErr = "", ""
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
	body := screens.Column(theme.S3,
		components.TextField(th, &a.url, "URL", "https://", a.urlErr),
		recordIf(a.urlErr),
		screens.Column(theme.S1,
			screens.Label(th, theme.TextSm, th.TextMuted, "Destination folder"),
			screens.Row(screens.Label(th, theme.TextMd, th.Text, a.destDir),
				components.Button(th, &a.change, "Change…", components.Secondary)),
		),
		components.TextField(th, &a.name, "File name (optional)", "Detected from the server", ""),
		func(gtx layout.Context) layout.Dimensions {
			if a.submitErr == "" {
				return layout.Dimensions{}
			}
			return screens.Text(th, theme.TextSm, th.Error, a.submitErr, 3)(gtx)
		},
	)
	actions := screens.Row(components.Button(th, &a.cancel, "Cancel", components.Ghost),
		components.Button(th, &a.add, "Add", components.Primary))
	modal(gtx, th, components.DialogShell(th, "Add download", body, actions))
	return out
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

// modal dims the window and centers a dialog of at most 520 dp width.
func modal(gtx layout.Context, th *theme.Theme, w layout.Widget) layout.Dimensions {
	gtx.Constraints.Min = gtx.Constraints.Max
	return layout.Background{}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return screens.Fill(gtx, theme.WithAlpha(th.Text, 0x66))
	}, func(gtx layout.Context) layout.Dimensions {
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = 0
			gtx.Constraints.Max.X = min(gtx.Constraints.Max.X-gtx.Dp(theme.S6), gtx.Dp(520))
			return w(gtx)
		})
	})
}

// HasFocus reports whether one of the dialog's text fields has keyboard focus.
func (a *Add) HasFocus(gtx layout.Context) bool {
	return gtx.Focused(&a.url) || gtx.Focused(&a.name)
}

// URLEmpty reports whether the URL field is empty.
func (a *Add) URLEmpty() bool { return strings.TrimSpace(a.url.Text()) == "" }
