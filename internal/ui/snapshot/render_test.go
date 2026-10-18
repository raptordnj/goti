//go:build snapshot

package snapshot

import (
	"errors"
	"flag"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"gioui.org/layout"

	"github.com/raptordnj/goti/internal/ui/dialogs"
	"github.com/raptordnj/goti/internal/ui/screens"
	"github.com/raptordnj/goti/internal/ui/state"
	"github.com/raptordnj/goti/internal/ui/theme"
)

var update = flag.Bool("update", false, "write PNGs to docs/screenshots")

type scene struct {
	name string
	snap func() *state.Snapshot
	// build returns the layout for the scene given fresh screen state.
	build func(th *theme.Theme, snap *state.Snapshot) func(layout.Context)
}

func mainScene(sel string, dlg func(*dialogs.Add, *dialogs.Confirm, *state.Snapshot) func(layout.Context, *theme.Theme, *state.Snapshot)) func(*theme.Theme, *state.Snapshot) func(layout.Context) {
	return func(th *theme.Theme, snap *state.Snapshot) func(layout.Context) {
		var m screens.Main
		var add dialogs.Add
		var conf dialogs.Confirm
		m.Selected = sel
		var d func(layout.Context, *theme.Theme, *state.Snapshot)
		if dlg != nil {
			d = dlg(&add, &conf, snap)
		}
		return func(gtx layout.Context) {
			m.Layout(gtx, th, snap, Now)
			if d != nil {
				d(gtx, th, snap)
			}
		}
	}
}

func scenes() []scene {
	return []scene{
		{"main-empty", Empty, mainScene("", nil)},
		{"main-busy", Busy, mainScene("", nil)},
		{"details", Busy, mainScene("1", nil)},
		{"details-history", Busy, mainScene("4", nil)},
		{"add", Busy, mainScene("", func(a *dialogs.Add, _ *dialogs.Confirm, s *state.Snapshot) func(layout.Context, *theme.Theme, *state.Snapshot) {
			a.Open("https://example.com/big.iso", s.Settings.DownloadDir)
			return func(g layout.Context, th *theme.Theme, s *state.Snapshot) { a.Layout(g, th, s, Now) }
		})},
		{"confirm-remove", Busy, mainScene("", func(_ *dialogs.Add, c *dialogs.Confirm, s *state.Snapshot) func(layout.Context, *theme.Theme, *state.Snapshot) {
			c.Open(screens.ConfirmRemove, s.Tasks[0])
			return func(g layout.Context, th *theme.Theme, s *state.Snapshot) { c.Layout(g, th, s, Now) }
		})},
		{"settings", Busy, func(th *theme.Theme, snap *state.Snapshot) func(layout.Context) {
			var st screens.Settings
			st.Open(snap.Settings)
			return func(gtx layout.Context) { st.Layout(gtx, th, snap, Now) }
		}},
		{"settings-about", Busy, func(th *theme.Theme, snap *state.Snapshot) func(layout.Context) {
			st := screens.Settings{Version: "dev", DataDir: "/home/user/.local/share/goti"}
			st.Open(snap.Settings)
			st.ScrollToAbout()
			return func(gtx layout.Context) { st.Layout(gtx, th, snap, Now) }
		}},
		{"toasts", WithToasts, mainScene("", nil)},
		{"offline", Offline, mainScene("", nil)},
	}
}

func TestScreenshots(t *testing.T) {
	sizes := [][2]int{{1100, 720}, {720, 560}}
	themes := []struct {
		name string
		th   func() *theme.Theme
	}{
		{"light", func() *theme.Theme { return theme.New(theme.Light()) }},
		{"dark", func() *theme.Theme { return theme.New(theme.Dark()) }},
	}
	for _, sc := range scenes() {
		for _, tc := range themes {
			for _, sz := range sizes {
				name := fmt.Sprintf("%s-%s-%dx%d", sc.name, tc.name, sz[0], sz[1])
				t.Run(name, func(t *testing.T) {
					th := tc.th()
					snap := sc.snap()
					img, err := Render(sz[0], sz[1], sc.build(th, snap))
					if errors.Is(err, ErrNoGPU) {
						t.Skipf("headless rendering unavailable: %v", err)
					}
					if err != nil {
						t.Fatal(err)
					}
					if !*update {
						return
					}
					dir := filepath.Join("..", "..", "..", "docs", "screenshots")
					if err := os.MkdirAll(dir, 0o755); err != nil {
						t.Fatal(err)
					}
					f, err := os.Create(filepath.Join(dir, name+".png"))
					if err != nil {
						t.Fatal(err)
					}
					defer f.Close()
					if err := png.Encode(f, img); err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}
