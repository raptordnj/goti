package screens

import (
	"testing"

	"gioui.org/layout"

	"github.com/raptordnj/goti/internal/brand"
	"github.com/raptordnj/goti/internal/settings"
	"github.com/raptordnj/goti/internal/ui/state"
)

func TestEmptyStateShowsWordmark(t *testing.T) {
	recordTexts(t)
	var m Main
	hs := newHarness(800, 600)
	hs.frame(func(gtx layout.Context) { m.Layout(gtx, th, &state.Snapshot{}, now) })
	for _, s := range []string{"গতি", "Fast by design. Reliable by nature.", "Goti"} {
		if !hasText(t, s) {
			t.Errorf("missing %q in %v", s, RecordedTexts())
		}
	}
}

func TestAboutShowsBrand(t *testing.T) {
	recordTexts(t)
	st := Settings{Version: "1.2.3", DataDir: "/data/goti"}
	st.Open(settings.Default())
	hs := newHarness(800, 4000)
	hs.frame(func(gtx layout.Context) { st.Layout(gtx, th, &state.Snapshot{}, now) })
	for _, s := range []string{"About", brand.Name, brand.Wordmark, brand.Tagline, "Version 1.2.3", "Data folder: /data/goti"} {
		if !hasText(t, s) {
			t.Errorf("missing %q in %v", s, RecordedTexts())
		}
	}
}
