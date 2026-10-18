package ui

import (
	"reflect"
	"testing"

	"gioui.org/io/key"

	"github.com/raptordnj/swiftfetch/internal/app"
	"github.com/raptordnj/swiftfetch/internal/ui/screens"
	"github.com/raptordnj/swiftfetch/internal/ui/state"
)

func TestShortcuts(t *testing.T) {
	tasks := []state.TaskView{{ID: "a", CanPause: true}, {ID: "b", CanResume: true}, {ID: "c"}}
	sel := func(id string) KeyContext { return KeyContext{Tasks: tasks, Selected: id} }
	ctrl := func(n key.Name) key.Event { return key.Event{Name: n, Modifiers: key.ModShortcut} }
	plain := func(n key.Name) key.Event { return key.Event{Name: n} }
	cases := []struct {
		name string
		ev   key.Event
		ctx  KeyContext
		want screens.Intent
	}{
		{"ctrl+n", ctrl("N"), KeyContext{}, screens.IntentOpenAdd{}},
		{"ctrl+v", ctrl("V"), KeyContext{}, IntentPasteAdd{}},
		{"ctrl+v in editor", ctrl("V"), KeyContext{InText: true}, nil},
		{"ctrl+v dialog", ctrl("V"), KeyContext{DialogOpen: true}, nil},
		{"space pause", plain(key.NameSpace), sel("a"), screens.IntentCommand{Cmd: app.PauseTask{ID: "a"}}},
		{"space resume", plain(key.NameSpace), sel("b"), screens.IntentCommand{Cmd: app.ResumeTask{ID: "b"}}},
		{"space none", plain(key.NameSpace), sel("c"), nil},
		{"space no sel", plain(key.NameSpace), KeyContext{Tasks: tasks}, nil},
		{"space in text", plain(key.NameSpace), KeyContext{Tasks: tasks, Selected: "a", InText: true}, nil},
		{"delete", plain(key.NameDeleteForward), sel("b"), screens.IntentConfirm{Kind: screens.ConfirmRemove, ID: "b"}},
		{"enter", plain(key.NameReturn), sel("b"), screens.IntentSelect{ID: "b"}},
		{"esc dialog", plain(key.NameEscape), KeyContext{DialogOpen: true, SettingsOpen: true, Selected: "a"}, screens.IntentCloseDialog{}},
		{"esc settings", plain(key.NameEscape), KeyContext{SettingsOpen: true, Selected: "a"}, screens.IntentCloseSettings{}},
		{"esc details", plain(key.NameEscape), sel("a"), screens.IntentSelect{}},
		{"esc nothing", plain(key.NameEscape), KeyContext{}, nil},
		{"ctrl+,", ctrl(","), KeyContext{}, screens.IntentOpenSettings{}},
		{"ctrl+q", ctrl("Q"), KeyContext{DialogOpen: true}, screens.IntentCommand{Cmd: app.Quit{}}},
		{"down", plain(key.NameDownArrow), sel("a"), screens.IntentSelect{ID: "b"}},
		{"down last", plain(key.NameDownArrow), sel("c"), screens.IntentSelect{ID: "c"}},
		{"down none", plain(key.NameDownArrow), KeyContext{Tasks: tasks}, screens.IntentSelect{ID: "a"}},
		{"up", plain(key.NameUpArrow), sel("b"), screens.IntentSelect{ID: "a"}},
		{"up first", plain(key.NameUpArrow), sel("a"), screens.IntentSelect{ID: "a"}},
		{"up empty", plain(key.NameUpArrow), KeyContext{}, nil},
		{"arrows in settings", plain(key.NameDownArrow), KeyContext{Tasks: tasks, SettingsOpen: true}, nil},
		{"release ignored", key.Event{Name: "N", Modifiers: key.ModShortcut, State: key.Release}, KeyContext{}, nil},
		{"plain n", plain("N"), KeyContext{}, nil},
	}
	for _, c := range cases {
		if got := Shortcut(c.ev, c.ctx); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %#v want %#v", c.name, got, c.want)
		}
	}
}
