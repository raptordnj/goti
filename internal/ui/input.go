package ui

import (
	"errors"
	"io"
	"log/slog"
	"strings"
	"time"

	"gioui.org/io/clipboard"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/transfer"
	"gioui.org/layout"
	"gioui.org/x/explorer"

	"github.com/raptordnj/swiftfetch/internal/app"
	"github.com/raptordnj/swiftfetch/internal/ui/screens"
	"github.com/raptordnj/swiftfetch/internal/ui/state"
)

// quitRetryFor bounds how long a window close retries a full command buffer.
const quitRetryFor = 5 * time.Second

// sendQuit sends Quit, retrying a full buffer for up to quitRetryFor.
func sendQuit(send func(app.Command) bool, log *slog.Logger) bool {
	deadline := time.Now().Add(quitRetryFor)
	for !send(app.Quit{}) {
		if time.Now().After(deadline) {
			log.Error("could not send Quit: command buffer full")
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
	return true
}

// startPick runs the folder picker in a goroutine; the result arrives on
// c.picked ("" = cancelled/failed) followed by an Invalidate.
func (c *controller) startPick() {
	if c.picker == nil || c.pickBusy {
		return
	}
	c.pickBusy = true
	p, log := c.picker, c.log
	go func() {
		path, err := p.ChooseFolder()
		if err != nil {
			if !errors.Is(err, explorer.ErrUserDecline) {
				log.Warn("folder picker failed", "err", err)
			}
			path = ""
		}
		select {
		case c.picked <- path:
		default:
		}
		if c.invalidate != nil {
			c.invalidate()
		}
	}()
}

// pollPicked applies a finished folder pick.
func (c *controller) pollPicked() {
	select {
	case p := <-c.picked:
		c.pickBusy = false
		if p != "" && c.dialog == dialogAdd {
			c.add.SetDestDir(p)
		}
	default:
	}
}

// focus reports whether a text field, or any widget, has keyboard focus.
func (c *controller) focus(gtx layout.Context) (inText, any bool) {
	inText = (c.dialog == dialogAdd && c.add.HasFocus(gtx)) || (c.settingsOpen && c.settings.HasFocus(gtx))
	return inText, !gtx.Focused(nil)
}

// keyFilters selects the shortcut filters for the current focus: in a text
// field only modifier shortcuts and Esc (so editors keep their keys); with a
// button focused, everything but Space/Enter (left to the button).
func keyFilters(inText, buttonFocused bool) []event.Filter {
	var out []event.Filter
	for _, f := range shortcutKeys {
		switch {
		case inText && f.Required == 0 && f.Name != key.NameEscape:
			continue
		case inText && f.Name == "V":
			continue
		case buttonFocused && (f.Name == key.NameSpace || f.Name == key.NameReturn || f.Name == key.NameEnter):
			continue
		}
		out = append(out, f)
	}
	return out
}

// keyEvents turns this frame's shortcut keys into intents.
func (c *controller) keyEvents(gtx layout.Context, snap *state.Snapshot) []screens.Intent {
	inText, anyFocus := c.focus(gtx)
	kc := KeyContext{Tasks: snap.Tasks, Selected: c.main.Selected, DialogOpen: c.dialog != dialogNone,
		SettingsOpen: c.settingsOpen, InText: inText, ButtonFocused: anyFocus && !inText}
	filters := keyFilters(kc.InText, kc.ButtonFocused)
	var out []screens.Intent
	for {
		ev, ok := gtx.Event(filters...)
		if !ok {
			return out
		}
		if ke, ok := ev.(key.Event); ok {
			if in := Shortcut(ke, kc); in != nil {
				out = append(out, in)
			}
		}
	}
}

// clipboardEvents requests and consumes clipboard text for the Add dialog.
// A URL is applied only while the URL field is still empty.
func (c *controller) clipboardEvents(gtx layout.Context, snap *state.Snapshot) {
	tag := &c.clipTag
	for {
		ev, ok := gtx.Event(transfer.TargetFilter{Target: tag, Type: textMIME})
		if !ok {
			break
		}
		de, ok := ev.(transfer.DataEvent)
		if !ok {
			continue
		}
		rc := de.Open()
		b, _ := io.ReadAll(io.LimitReader(rc, 8<<10))
		_ = rc.Close()
		if s := strings.TrimSpace(string(b)); c.clipReq && c.dialog == dialogAdd && c.add.URLEmpty() && isHTTPURL(s) {
			c.add.Open(s, snap.Settings.DownloadDir)
		}
		c.clipReq = false
	}
	if c.wantClip {
		c.wantClip, c.clipReq = false, true
		gtx.Execute(clipboard.ReadCmd{Tag: tag})
	}
}

func isHTTPURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}
