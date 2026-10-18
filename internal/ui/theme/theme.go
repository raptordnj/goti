// Package theme holds SwiftFetch's design tokens (spec §8): spacing, type
// scale, radii and the light/dark palettes.
package theme

import (
	"image/color"

	"gioui.org/font/gofont"
	"gioui.org/text"
	"gioui.org/unit"
)

// Spacing scale.
const (
	S1 unit.Dp = 4
	S2 unit.Dp = 8
	S3 unit.Dp = 12
	S4 unit.Dp = 16
	S5 unit.Dp = 24
	S6 unit.Dp = 32
)

// Type scale.
const (
	TextSm unit.Sp = 12
	TextMd unit.Sp = 14
	TextLg unit.Sp = 16
	TextXl unit.Sp = 20
)

// Corner radii.
const (
	RadiusSm unit.Dp = 6
	RadiusMd unit.Dp = 10
)

// Palette is the set of color tokens.
type Palette struct {
	Bg, Surface, SurfaceAlt, Text, TextMuted, Accent, OnAccent, Border,
	Success, Warning, Error, Focus color.NRGBA
}

func rgb(v uint32) color.NRGBA {
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xff}
}

// Light returns the light palette.
func Light() Palette {
	return Palette{
		Bg: rgb(0xF6F7F9), Surface: rgb(0xFFFFFF), SurfaceAlt: rgb(0xEEF0F3),
		Text: rgb(0x1A1D23), TextMuted: rgb(0x5B6270),
		Accent: rgb(0x1F5FD1), OnAccent: rgb(0xFFFFFF), Border: rgb(0xD3D7DE),
		Success: rgb(0x1E7B34), Warning: rgb(0x8A5A00), Error: rgb(0xB42318), Focus: rgb(0x1F5FD1),
	}
}

// Dark returns the dark palette.
func Dark() Palette {
	return Palette{
		Bg: rgb(0x111317), Surface: rgb(0x1A1D23), SurfaceAlt: rgb(0x242830),
		Text: rgb(0xE8EAED), TextMuted: rgb(0xA3A9B5),
		Accent: rgb(0x6EA2FF), OnAccent: rgb(0x0B1220), Border: rgb(0x343944),
		Success: rgb(0x4ADE80), Warning: rgb(0xFBBF24), Error: rgb(0xFF8A80), Focus: rgb(0x8AB4FF),
	}
}

// Theme is a palette plus the text shaper.
type Theme struct {
	Palette
	Shaper *text.Shaper
}

// New builds a Theme with the embedded Go fonts.
func New(p Palette) *Theme {
	return &Theme{Palette: p, Shaper: text.NewShaper(text.WithCollection(gofont.Collection()))}
}

// WithAlpha returns c with alpha a (for hover/pressed/disabled overlays).
func WithAlpha(c color.NRGBA, a uint8) color.NRGBA { c.A = a; return c }

// Mix blends a toward b by t in [0,1].
func Mix(a, b color.NRGBA, t float32) color.NRGBA {
	m := func(x, y uint8) uint8 { return uint8(float32(x) + (float32(y)-float32(x))*t) }
	return color.NRGBA{R: m(a.R, b.R), G: m(a.G, b.G), B: m(a.B, b.B), A: m(a.A, b.A)}
}
