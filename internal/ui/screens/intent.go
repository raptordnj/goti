// Package screens holds SwiftFetch's passive Gio screens. Screens keep only
// widget state, lay out a *state.Snapshot and return Intents; the window loop
// turns Intents into App commands, pickers, clipboard writes and so on.
// Screens import internal/app for command types only.
package screens

import (
	"github.com/raptordnj/swiftfetch/internal/app"
	"github.com/raptordnj/swiftfetch/internal/settings"
)

// Intent is something a screen wants the window loop to do.
type Intent interface{}

// ConfirmKind selects the confirmation dialog variant.
type ConfirmKind int

// Confirmation kinds.
const (
	ConfirmCancel ConfirmKind = iota
	ConfirmRemove
)

type (
	// IntentCommand sends Cmd to the App.
	IntentCommand struct{ Cmd app.Command }
	// IntentOpenAdd opens the Add dialog, optionally prefilled.
	IntentOpenAdd struct{ Prefill string }
	// IntentOpenSettings opens the settings screen.
	IntentOpenSettings struct{}
	// IntentCloseSettings closes the settings screen.
	IntentCloseSettings struct{}
	// IntentSelect selects a task (empty ID clears the selection).
	IntentSelect struct{ ID string }
	// IntentConfirm opens the confirmation dialog for a task.
	IntentConfirm struct {
		Kind ConfirmKind
		ID   string
	}
	// IntentPickFolder opens the native folder picker for the Add dialog.
	IntentPickFolder struct{}
	// IntentOpen opens Path, or reveals it in the file manager.
	IntentOpen struct {
		Path   string
		Reveal bool
	}
	// IntentCopy writes Text to the clipboard.
	IntentCopy struct{ Text string }
	// IntentDismissToast dismisses Snapshot.Toasts[Index].
	IntentDismissToast struct{ Index int }
	// IntentPreviewTheme applies Theme live while settings are open.
	IntentPreviewTheme struct{ Theme settings.Theme }
	// IntentCloseDialog closes the open dialog without action.
	IntentCloseDialog struct{}
)
