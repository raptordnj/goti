package components

// Glyphs used by components. Each must exist in gofont.Collection()
// (checked by TestGlyphsCovered); ⋯ and ✕ do not.
const (
	glyphMenu    = "…" // U+2026
	glyphDismiss = "×" // U+00D7
	metaSep      = " · "
)

// usedGlyphs lists every fixed string components render.
func usedGlyphs() []string {
	s := []string{glyphMenu, glyphDismiss, metaSep, "%", "/", "0123456789"}
	for _, b := range rowButtonSpecs(&RowWidgets{}) {
		s = append(s, b.glyph)
	}
	for _, g := range []string{"ZIP", "ISO", "VID", "AUD", "IMG", "DOC", "FILE"} {
		s = append(s, g)
	}
	return s
}
