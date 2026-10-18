package theme

import (
	_ "embed"
	"fmt"

	"gioui.org/font"
	"gioui.org/font/opentype"
	"golang.org/x/image/font/sfnt"
)

//go:embed fonts/Inter-Regular.ttf
var interRegular []byte

//go:embed fonts/Inter-Medium.ttf
var interMedium []byte

//go:embed fonts/Inter-SemiBold.ttf
var interSemiBold []byte

// InterFaces returns the embedded Inter faces (Regular, Medium, SemiBold).
// Inter is licensed under the SIL OFL 1.1 (fonts/OFL.txt).
func InterFaces() []font.FontFace {
	var out []font.FontFace
	for _, f := range []struct {
		w   font.Weight
		ttf []byte
	}{{font.Normal, interRegular}, {font.Medium, interMedium}, {font.SemiBold, interSemiBold}} {
		face, err := opentype.Parse(f.ttf)
		if err != nil {
			panic(fmt.Sprintf("theme: embedded Inter font invalid: %v", err))
		}
		out = append(out, font.FontFace{Font: font.Font{Typeface: "Inter", Weight: f.w}, Face: face})
	}
	return out
}

// Covers reports whether every embedded Inter face has a glyph for r.
func Covers(r rune) bool {
	var buf sfnt.Buffer
	for _, ttf := range [][]byte{interRegular, interMedium, interSemiBold} {
		f, err := sfnt.Parse(ttf)
		if err != nil {
			return false
		}
		if g, _ := f.GlyphIndex(&buf, r); g == 0 {
			return false
		}
	}
	return true
}
