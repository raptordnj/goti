package components

import "github.com/raptordnj/goti/internal/ui/state"

// Glyphs used by components. Each must exist in Inter faces
// (checked by TestGlyphsCovered); ⋯ and ✕ do not.
const (
	metaSep = " · "
)

// usedGlyphs lists every fixed string components render.
func usedGlyphs() []string {
	s := []string{"…", metaSep, "%", "/", "0123456789", " of ", " left", "KiB MiB GiB TiB PiB B/s"}
	for _, it := range RowMenuItems(state.TaskView{CanOpen: true, CanRestart: true, CanCancel: true}) {
		s = append(s, it.Label)
	}
	return s
}
