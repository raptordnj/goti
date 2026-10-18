// Package ui is Goti's Gio window loop: it lays out screens over the
// latest state.Snapshot and turns their Intents into App commands, pickers,
// clipboard operations and so on.
package ui

import (
	"gioui.org/io/key"

	"github.com/raptordnj/goti/internal/app"
	"github.com/raptordnj/goti/internal/ui/screens"
	"github.com/raptordnj/goti/internal/ui/state"
)

// IntentPasteAdd opens the Add dialog prefilled from the clipboard (Ctrl+V
// outside text fields).
type IntentPasteAdd struct{}

// KeyContext is what Shortcut needs to know about the window.
type KeyContext struct {
	Tasks         []state.TaskView // in display order
	Selected      string
	DialogOpen    bool
	SettingsOpen  bool
	InText        bool // a text field has focus
	ButtonFocused bool // a non-text widget has focus: Space/Enter are its own
	MenuOpen      bool // a popover menu owns Esc, arrows, Space and Enter
}

// shortcutKeys are the keys the window registers filters for.
var shortcutKeys = []key.Filter{
	{Name: "N", Required: key.ModShortcut},
	{Name: "V", Required: key.ModShortcut},
	{Name: ",", Required: key.ModShortcut},
	{Name: "Q", Required: key.ModShortcut},
	{Name: key.NameSpace},
	{Name: key.NameDeleteForward},
	{Name: key.NameReturn},
	{Name: key.NameEnter},
	{Name: key.NameEscape},
	{Name: key.NameUpArrow},
	{Name: key.NameDownArrow},
}

// Shortcut maps a key press to an Intent per spec §8; nil means none.
func Shortcut(ev key.Event, c KeyContext) screens.Intent {
	if ev.State != key.Press {
		return nil
	}
	if ev.Modifiers.Contain(key.ModShortcut) {
		switch ev.Name {
		case "Q":
			return screens.IntentCommand{Cmd: app.Quit{}}
		case "N":
			if !c.DialogOpen {
				return screens.IntentOpenAdd{}
			}
		case "V":
			if !c.DialogOpen && !c.InText {
				return IntentPasteAdd{}
			}
		case ",":
			if !c.DialogOpen {
				return screens.IntentOpenSettings{}
			}
		}
		return nil
	}
	if ev.Modifiers != 0 || c.MenuOpen {
		return nil
	}
	if ev.Name == key.NameEscape {
		switch {
		case c.DialogOpen:
			return screens.IntentCloseDialog{}
		case c.SettingsOpen:
			return screens.IntentCloseSettings{}
		case c.Selected != "":
			return screens.IntentSelect{}
		}
		return nil
	}
	if c.DialogOpen || c.SettingsOpen || c.InText {
		return nil
	}
	if c.ButtonFocused && (ev.Name == key.NameSpace || ev.Name == key.NameReturn || ev.Name == key.NameEnter) {
		return nil
	}
	idx := -1
	for i := range c.Tasks {
		if c.Tasks[i].ID == c.Selected {
			idx = i
		}
	}
	switch ev.Name {
	case key.NameUpArrow, key.NameDownArrow:
		if len(c.Tasks) == 0 {
			return nil
		}
		switch {
		case idx < 0:
			idx = 0
		case ev.Name == key.NameUpArrow && idx > 0:
			idx--
		case ev.Name == key.NameDownArrow && idx < len(c.Tasks)-1:
			idx++
		}
		return screens.IntentSelect{ID: c.Tasks[idx].ID}
	}
	if idx < 0 {
		return nil
	}
	v := c.Tasks[idx]
	switch ev.Name {
	case key.NameSpace:
		switch {
		case v.CanPause:
			return screens.IntentCommand{Cmd: app.PauseTask{ID: v.ID}}
		case v.CanResume:
			return screens.IntentCommand{Cmd: app.ResumeTask{ID: v.ID}}
		}
	case key.NameDeleteForward:
		return screens.IntentConfirm{Kind: screens.ConfirmRemove, ID: v.ID}
	case key.NameReturn, key.NameEnter:
		return screens.IntentSelect{ID: v.ID}
	}
	return nil
}
