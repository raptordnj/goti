package dialogs

import (
	"image"
	"slices"
	"testing"

	"gioui.org/io/key"
	"gioui.org/layout"

	"github.com/raptordnj/goti/internal/app"
	"github.com/raptordnj/goti/internal/domain"
	"github.com/raptordnj/goti/internal/ui/screens"
	"github.com/raptordnj/goti/internal/ui/state"
)

func TestAddValidation(t *testing.T) {
	recordTexts(t)
	var a Add
	a.Open("ftp://x", "/dl")
	hs := newHarness(800, 600)
	s := &state.Snapshot{}
	lay := func(gtx layout.Context) { a.Layout(gtx, th, s, now) }
	hs.frame(lay)
	hs.click(&a.add, lay)
	var got []screens.Intent
	hs.frame(func(gtx layout.Context) { got = a.Layout(gtx, th, s, now) })
	if len(got) != 0 {
		t.Fatalf("bad URL produced %#v", got)
	}
	hs.frame(lay)
	if !hasText(t, BadURL) {
		t.Fatalf("error not shown: %v", screens.RecordedTexts())
	}

	a.url.SetText("https://example.com/f.iso")
	a.SetDestDir("/picked")
	a.name.SetText("g.iso")
	hs.click(&a.add, lay)
	hs.frame(func(gtx layout.Context) { got = a.Layout(gtx, th, s, now) })
	want := screens.IntentCommand{Cmd: app.AddDownload{URL: "https://example.com/f.iso", DestDir: "/picked", FileName: "g.iso"}}
	if !slices.Contains(got, screens.Intent(want)) {
		t.Fatalf("intents %#v", got)
	}
}

func TestAddValidURL(t *testing.T) {
	for u, ok := range map[string]bool{"https://a.b/x": true, "http://h": true, "https://": false, "/rel": false, "ftp://h/x": false, "": false} {
		if (validURL(u) == nil) != ok {
			t.Errorf("%q: ok=%v", u, !ok)
		}
	}
}

func TestAddChangeFolder(t *testing.T) {
	var a Add
	a.Open("", "/dl")
	hs := newHarness(800, 600)
	s := &state.Snapshot{}
	lay := func(gtx layout.Context) { a.Layout(gtx, th, s, now) }
	hs.frame(lay)
	hs.click(&a.change, lay)
	var got []screens.Intent
	hs.frame(func(gtx layout.Context) { got = a.Layout(gtx, th, s, now) })
	if !slices.Contains(got, screens.Intent(screens.IntentPickFolder{})) {
		t.Fatalf("intents %#v", got)
	}
}

func TestConfirmRemoveDeletePartial(t *testing.T) {
	for _, c := range []struct {
		st   domain.State
		show bool
	}{{domain.StatePaused, true}, {domain.StateFailed, true}, {domain.StateCompleted, false}} {
		var d Confirm
		v := task("a", c.st, state.SectionPaused)
		d.Open(screens.ConfirmRemove, v)
		hs := newHarness(800, 600)
		s := snapOf(v)
		lay := func(gtx layout.Context) { d.Layout(gtx, th, s, now) }
		hs.frame(lay)
		if d.ShowsDeletePartial != c.show {
			t.Errorf("%s: checkbox shown=%v", c.st, d.ShowsDeletePartial)
		}
		if c.show {
			hs.click(&d.partial, lay)
			hs.frame(lay)
			hs.frame(lay)
		}
		hs.click(&d.ok, lay)
		var got []screens.Intent
		hs.frame(func(gtx layout.Context) { got = d.Layout(gtx, th, s, now) })
		want := screens.IntentCommand{Cmd: app.RemoveTask{ID: "a", DeletePartial: c.show}}
		if !slices.Contains(got, screens.Intent(want)) {
			t.Errorf("%s: intents %#v", c.st, got)
		}
	}
}

func TestConfirmCancel(t *testing.T) {
	var d Confirm
	v := task("a", domain.StateDownloading, state.SectionActive)
	d.Open(screens.ConfirmCancel, v)
	hs := newHarness(800, 600)
	s := snapOf(v)
	lay := func(gtx layout.Context) { d.Layout(gtx, th, s, now) }
	hs.frame(lay)
	hs.click(&d.ok, lay)
	var got []screens.Intent
	hs.frame(func(gtx layout.Context) { got = d.Layout(gtx, th, s, now) })
	if !slices.Contains(got, screens.Intent(screens.IntentCommand{Cmd: app.CancelTask{ID: "a"}})) || d.ShowsDeletePartial {
		t.Fatalf("intents %#v", got)
	}
}

func TestAddURLErrorClearsOnEdit(t *testing.T) {
	var a Add
	a.Open("bad", "/dl")
	hs := newHarness(800, 600)
	s := &state.Snapshot{}
	lay := func(gtx layout.Context) { a.Layout(gtx, th, s, now) }
	hs.frame(lay)
	hs.click(&a.add, lay)
	hs.frame(lay)
	if a.urlErr == "" {
		t.Fatal("expected error")
	}
	hs.gtx.Execute(key.FocusCmd{Tag: &a.url})
	hs.frame(lay)
	hs.r.Queue(key.EditEvent{Text: "x"})
	hs.frame(lay)
	if a.urlErr != "" {
		t.Fatalf("error not cleared: %q", a.urlErr)
	}
}

func TestConfirmRemoveSwitch(t *testing.T) {
	recordTexts(t)
	var d Confirm
	v := task("a", domain.StatePaused, state.SectionPaused)
	v.Total, v.TotalKnown = 3<<20, true
	d.Open(screens.ConfirmRemove, v)
	hs := newHarness(800, 600)
	s := snapOf(v)
	lay := func(gtx layout.Context) { d.Layout(gtx, th, s, now) }
	hs.frame(lay)
	for _, w := range []string{"a.iso", "0 B of 3.0 MiB", "Also delete partial data"} {
		if !hasText(t, w) {
			t.Errorf("missing %q in %v", w, screens.RecordedTexts())
		}
	}
	hs.click(&d.partial, lay)
	hs.frame(lay)
	if !d.partial.Value {
		t.Fatal("switch did not toggle")
	}
	hs.click(&d.partial, lay)
	hs.frame(lay)
	if d.partial.Value {
		t.Fatal("switch did not toggle back")
	}
}

func TestDialogNarrow(t *testing.T) {
	var a Add
	a.Open("https://example.com/f.iso", "/home/user/Downloads/some/really/deep/folder/structure")
	a.urlErr, a.submitErr = BadURL, "server said no"
	for _, sz := range [][2]int{{640, 420}, {640, 220}} {
		hs := newHarness(sz[0], sz[1])
		s := &state.Snapshot{}
		lay := func(gtx layout.Context) { a.Layout(gtx, th, s, now) }
		hs.frame(lay)
		hs.frame(lay)
		r := a.shell.Card
		win := image.Rect(16, 16, sz[0]-16, sz[1]-16)
		if r.Empty() || !r.In(win) {
			t.Fatalf("%v: card %v outside %v", sz, r, win)
		}
		if r.Dx() != 440 {
			t.Errorf("%v: card width %d, want 440", sz, r.Dx())
		}
		if sz[1] == 220 && !a.shell.List.Position.BeforeEnd {
			t.Errorf("%v: body not scrollable: %+v", sz, a.shell.List.Position)
		}
	}
	hs := newHarness(300, 420)
	hs.frame(func(gtx layout.Context) { a.Layout(gtx, th, &state.Snapshot{}, now) })
	if w := a.shell.Card.Dx(); w != 300-32 {
		t.Errorf("narrow width %d, want %d", w, 300-32)
	}
}
