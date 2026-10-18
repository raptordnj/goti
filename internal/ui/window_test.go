package ui

import (
	"log/slog"
	"testing"
	"time"

	"gioui.org/io/event"

	"github.com/raptordnj/swiftfetch/internal/app"
	"github.com/raptordnj/swiftfetch/internal/settings"
	"github.com/raptordnj/swiftfetch/internal/ui/screens"
	"github.com/raptordnj/swiftfetch/internal/ui/state"
)

type fakePicker struct{ path string }

func (fakePicker) ListenEvents(event.Event)        {}
func (p fakePicker) ChooseFolder() (string, error) { return p.path, nil }

func newCtl(accept bool) (*controller, *[]app.Command) {
	var sent []app.Command
	c := newController(func(cmd app.Command) bool {
		if accept {
			sent = append(sent, cmd)
		}
		return accept
	}, func() {})
	return c, &sent
}

func TestBusyToast(t *testing.T) {
	c, _ := newCtl(false)
	now := time.Unix(1e6, 0)
	snap := &state.Snapshot{Toasts: []state.Toast{{Text: "x", At: now}}}
	c.handle(screens.IntentCommand{Cmd: app.PauseAll{}}, snap, now)
	ts := c.toasts(snap, now)
	if len(ts) != 2 || ts[1].Text != "Busy, try again" || ts[1].Kind != state.ToastError {
		t.Fatalf("toasts %+v", ts)
	}
	c.handle(screens.IntentDismissToast{Index: 0}, snap, now)
	if ts := c.toasts(snap, now); len(ts) != 1 || ts[0].Text != "Busy, try again" {
		t.Fatalf("after dismiss %+v", ts)
	}
	c.handle(screens.IntentDismissToast{Index: 0}, snap, now)
	if ts := c.toasts(snap, now); len(ts) != 0 {
		t.Fatalf("after second dismiss %+v", ts)
	}
	c.handle(screens.IntentCommand{Cmd: app.PauseAll{}}, snap, now)
	if ts := c.toasts(snap, now.Add(6*time.Second)); len(ts) != 0 {
		t.Fatalf("local toast did not expire: %+v", ts)
	}
}

func TestAddResultReachesDialog(t *testing.T) {
	c, sent := newCtl(true)
	now := time.Unix(1e6, 0)
	snap := &state.Snapshot{AddResultSeq: 3}
	c.handle(screens.IntentOpenAdd{Prefill: "https://a/b"}, snap, now)
	if c.dialog != dialogAdd {
		t.Fatal("add dialog not open")
	}
	c.handle(screens.IntentCommand{Cmd: app.AddDownload{URL: "https://a/b"}}, snap, now)
	if len(*sent) != 1 {
		t.Fatalf("sent %v", *sent)
	}
	c.afterResults(snap)
	if c.dialog != dialogAdd || !c.addPending {
		t.Fatal("closed before result")
	}
	c.afterResults(&state.Snapshot{AddResultSeq: 4, AddErr: "boom"})
	if c.dialog != dialogAdd || c.addPending || c.lastAddErr != "boom" {
		t.Fatalf("error not delivered: %v %v %q", c.dialog, c.addPending, c.lastAddErr)
	}
	c.handle(screens.IntentCommand{Cmd: app.AddDownload{URL: "https://a/b"}}, &state.Snapshot{AddResultSeq: 4}, now)
	c.afterResults(&state.Snapshot{AddResultSeq: 5})
	if c.dialog != dialogNone {
		t.Fatal("dialog not closed on success")
	}
}

func TestThemePreviewRevert(t *testing.T) {
	c, _ := newCtl(true)
	now := time.Unix(1e6, 0)
	snap := &state.Snapshot{Settings: settings.Settings{Theme: settings.ThemeLight}}
	c.handle(screens.IntentOpenSettings{}, snap, now)
	c.handle(screens.IntentPreviewTheme{Theme: settings.ThemeDark}, snap, now)
	if c.themeName(snap) != settings.ThemeDark {
		t.Fatal("preview not applied")
	}
	c.handle(screens.IntentCloseSettings{}, snap, now)
	if c.themeName(snap) != settings.ThemeLight {
		t.Fatal("preview not reverted")
	}
	// Save keeps the new theme until the snapshot catches up.
	c.handle(screens.IntentOpenSettings{}, snap, now)
	c.handle(screens.IntentPreviewTheme{Theme: settings.ThemeDark}, snap, now)
	s := snap.Settings
	s.Theme = settings.ThemeDark
	c.handle(screens.IntentCommand{Cmd: app.SaveSettings{S: s}}, snap, now)
	c.handle(screens.IntentCloseSettings{}, snap, now)
	if c.themeName(snap) != settings.ThemeDark {
		t.Fatal("saved theme reverted")
	}
	if c.themeName(&state.Snapshot{Settings: s}) != settings.ThemeDark {
		t.Fatal("saved theme lost")
	}
}

func TestFolderPickerResult(t *testing.T) {
	c, _ := newCtl(true)
	c.picker = fakePicker{path: "/tmp/x"}
	now := time.Unix(1e6, 0)
	c.handle(screens.IntentOpenAdd{}, &state.Snapshot{}, now)
	c.handle(screens.IntentPickFolder{}, &state.Snapshot{}, now)
	select {
	case p := <-c.picked:
		if p != "/tmp/x" {
			t.Fatal(p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no picker result")
	}
}

func TestSendQuitBounded(t *testing.T) {
	n := 0
	ok := sendQuit(func(app.Command) bool { n++; return n == 3 }, slog.Default())
	if !ok || n != 3 {
		t.Fatalf("ok=%v n=%d", ok, n)
	}
}

func TestAddURLEmpty(t *testing.T) {
	c, _ := newCtl(true)
	c.handle(screens.IntentOpenAdd{}, &state.Snapshot{}, time.Unix(1e6, 0))
	if !c.add.URLEmpty() {
		t.Fatal("expected empty")
	}
	c.handle(screens.IntentOpenAdd{Prefill: "https://a/b"}, &state.Snapshot{}, time.Unix(1e6, 0))
	if c.add.URLEmpty() {
		t.Fatal("expected non-empty: clipboard must not overwrite")
	}
}
