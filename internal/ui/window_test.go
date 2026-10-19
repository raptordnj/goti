package ui

import (
	"log/slog"
	"testing"
	"time"

	"gioui.org/io/event"

	"github.com/raptordnj/goti/internal/app"
	"github.com/raptordnj/goti/internal/domain"
	"github.com/raptordnj/goti/internal/settings"
	"github.com/raptordnj/goti/internal/ui/anim"
	"github.com/raptordnj/goti/internal/ui/screens"
	"github.com/raptordnj/goti/internal/ui/state"
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
	ok := SendQuit(func(app.Command) bool { n++; return n == 3 }, slog.Default())
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

func TestAddSelectsNewTask(t *testing.T) {
	c, _ := newCtl(true)
	now := time.Unix(1e6, 0)
	old := state.TaskView{ID: "a", CreatedAt: now.Add(-time.Hour)}
	snap := &state.Snapshot{AddResultSeq: 1, Tasks: []state.TaskView{old}}
	c.handle(screens.IntentOpenAdd{Prefill: "https://a/b"}, snap, now)
	c.handle(screens.IntentCommand{Cmd: app.AddDownload{URL: "https://a/b"}}, snap, now)
	nw := state.TaskView{ID: "b", CreatedAt: now}
	c.afterResults(&state.Snapshot{AddResultSeq: 2, Tasks: []state.TaskView{old, nw}})
	if c.main.Selected != "b" {
		t.Fatalf("selected %q, want b", c.main.Selected)
	}
	if c.main.ScrollTarget() != "b" {
		t.Fatalf("scroll target %q, want b", c.main.ScrollTarget())
	}
}

func TestOptimisticIconReverts(t *testing.T) {
	now := time.Unix(1e6, 0)
	v := state.TaskView{ID: "a", State: domain.StateDownloading, CanPause: true}
	snap := &state.Snapshot{Tasks: []state.TaskView{v}}

	c, _ := newCtl(true)
	c.handle(screens.IntentCommand{Cmd: app.PauseTask{ID: "a"}}, snap, now)
	if got := c.main.Optimistic("a"); got != "resume" {
		t.Fatalf("optimistic %q, want resume", got)
	}
	// The command later fails: an error toast reverts the icon.
	failed := &state.Snapshot{Tasks: []state.TaskView{v}, Toasts: []state.Toast{{Kind: state.ToastError, Text: "x", At: now.Add(time.Second)}}}
	c.syncOptimistic(failed)
	if got := c.main.Optimistic("a"); got != "" {
		t.Fatalf("after error toast %q, want reverted", got)
	}

	// A command the App refuses never shows the optimistic icon.
	b, _ := newCtl(false)
	b.handle(screens.IntentCommand{Cmd: app.PauseTask{ID: "a"}}, snap, now)
	if got := b.main.Optimistic("a"); got != "" {
		t.Fatalf("refused command left %q", got)
	}

	// A state change of the task clears it too.
	c.handle(screens.IntentCommand{Cmd: app.PauseTask{ID: "a"}}, failed, now)
	paused := v
	paused.State, paused.CanPause, paused.CanResume = domain.StatePaused, false, true
	c.syncOptimistic(&state.Snapshot{Tasks: []state.TaskView{paused}, Toasts: failed.Toasts})
	if got := c.main.Optimistic("a"); got != "" {
		t.Fatalf("after state change %q", got)
	}
}

func TestReduceMotionWired(t *testing.T) {
	prev := anim.Reduce
	t.Cleanup(func() { anim.Reduce = prev })
	s := settings.Default()
	wireReduceMotion(func() settings.Settings { return s })
	if anim.Reduce() {
		t.Fatal("reduce on with ReduceMotion off")
	}
	s.ReduceMotion = true
	if !anim.Reduce() {
		t.Fatal("reduce not following settings")
	}
}

func TestSettingsFolderPick(t *testing.T) {
	c, _ := newCtl(true)
	c.picker = fakePicker{path: "/tmp/dl"}
	snap := &state.Snapshot{Settings: settings.Default()}
	now := time.Unix(1e6, 0)
	c.handle(screens.IntentOpenSettings{}, snap, now)
	c.handle(screens.IntentPickFolder{}, snap, now)
	deadline := time.Now().Add(2 * time.Second)
	for c.settings.Dir() != "/tmp/dl" {
		if time.Now().After(deadline) {
			t.Fatalf("settings dir %q", c.settings.Dir())
		}
		time.Sleep(5 * time.Millisecond)
		c.pollPicked()
	}
}

func TestQuitOnDestroy(t *testing.T) {
	mobile := WindowDeps{KeepAlive: func(bool) bool { return true }}
	if quitOnDestroy(mobile, false) {
		t.Fatal("mobile must never quit on destroy, even with CloseToTray off")
	}
	desk := WindowDeps{KeepAlive: func(c bool) bool { return c }}
	if !quitOnDestroy(desk, false) || quitOnDestroy(desk, true) {
		t.Fatal("desktop policy wrong")
	}
	if !quitOnDestroy(WindowDeps{}, true) {
		t.Fatal("no tray -> quit")
	}
}
