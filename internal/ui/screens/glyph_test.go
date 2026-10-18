package screens

import (
	"testing"

	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/sfnt"
)

func assertGlyphs(t *testing.T, texts []string) {
	t.Helper()
	f, err := sfnt.Parse(goregular.TTF)
	if err != nil {
		t.Fatal(err)
	}
	var buf sfnt.Buffer
	for _, s := range append(texts, "Remove…", "Change…", "[x] Also delete partial data", OfflineText) {
		for _, r := range s {
			if g, _ := f.GlyphIndex(&buf, r); g == 0 {
				t.Errorf("glyph %q in %q missing", r, s)
			}
		}
	}
}
