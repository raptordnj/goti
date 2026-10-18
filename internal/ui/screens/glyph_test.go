package screens

import (
	"testing"

	"github.com/raptordnj/goti/internal/ui/theme"
)

func assertGlyphs(t *testing.T, texts []string) {
	t.Helper()
	for _, s := range append(texts, "Remove…", "Change…", "[x] Also delete partial data", OfflineText) {
		for _, r := range s {
			if !theme.Covers(r) && !theme.CoversBengali(r) {
				t.Errorf("glyph %q in %q missing", r, s)
			}
		}
	}
}
