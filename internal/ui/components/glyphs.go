package components

// Glyphs used by components. Each must exist in Inter faces
// (checked by TestGlyphsCovered); ⋯ and ✕ do not.
const (
	metaSep = " · "
)

// usedGlyphs lists every fixed string components render.
func usedGlyphs() []string {
	s := []string{"…", metaSep, "%", "/", "0123456789"}
	for _, g := range []string{"ZIP", "ISO", "VID", "AUD", "IMG", "DOC", "FILE"} {
		s = append(s, g)
	}
	return s
}
