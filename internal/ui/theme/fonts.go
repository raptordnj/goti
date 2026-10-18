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

//go:embed fonts/NotoSansBengali-Regular.ttf
var bengaliRegular []byte

//go:embed fonts/NotoSansBengali-Medium.ttf
var bengaliMedium []byte

// Faces returns the shaper collection: Inter first so Latin text keeps
// Inter, then Noto Sans Bengali (SIL OFL 1.1, fonts/OFL-NotoSansBengali.txt)
// which the shaper falls back to per rune for Bengali text such as the
// wordmark.
func Faces() []font.FontFace {
	out := InterFaces()
	for _, f := range []struct {
		w   font.Weight
		ttf []byte
	}{{font.Normal, bengaliRegular}, {font.Medium, bengaliMedium}} {
		face, err := opentype.Parse(f.ttf)
		if err != nil {
			panic(fmt.Sprintf("theme: embedded Noto Sans Bengali font invalid: %v", err))
		}
		out = append(out, font.FontFace{Font: font.Font{Typeface: "Noto Sans Bengali", Weight: f.w}, Face: face})
	}
	return out
}

// CoversBengali reports whether every embedded Noto Sans Bengali face has a
// glyph for r.
func CoversBengali(r rune) bool { return covers(r, bengaliRegular, bengaliMedium) }

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
func Covers(r rune) bool { return covers(r, interRegular, interMedium, interSemiBold) }

func covers(r rune, ttfs ...[]byte) bool {
	var buf sfnt.Buffer
	for _, ttf := range ttfs {
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
