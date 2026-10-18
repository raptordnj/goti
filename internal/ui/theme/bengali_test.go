package theme

import (
	"testing"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/text"
	"golang.org/x/image/math/fixed"
)

func TestBengaliCovered(t *testing.T) {
	for _, r := range "গতি" {
		if !CoversBengali(r) {
			t.Errorf("rune %U not covered by Noto Sans Bengali", r)
		}
	}
	embeddedOnly := text.NewShaper(text.NoSystemFonts(), text.WithCollection(Faces()))
	for _, sh := range []*text.Shaper{New(Light()).Shaper, embeddedOnly} {
		shapeClean(t, sh)
	}
}

func shapeClean(t *testing.T, sh *text.Shaper) {
	t.Helper()
	for _, w := range []font.Weight{font.Normal, font.Medium} {
		sh.LayoutString(text.Parameters{
			Font: font.Font{Typeface: "Inter", Weight: w}, PxPerEm: fixed.I(16),
			MaxLines: 1, MaxWidth: 1000, Locale: layout.Context{}.Locale,
		}, "Goti গতি")
		n := 0
		for {
			g, ok := sh.NextGlyph()
			if !ok {
				break
			}
			n++
			// GlyphID packs face index and ppem above the low 32-bit GID.
			if gid := uint64(g.ID) & (1<<32 - 1); gid == 0 {
				t.Errorf("weight %v: .notdef glyph at cluster %d", w, n)
			}
		}
		if n < 5 {
			t.Errorf("weight %v: only %d glyphs", w, n)
		}
	}
}
