// Package theme holds Goti's design tokens (spec §8): spacing, type
// scale, radii and the light/dark palettes.
package theme

import (
	"image/color"

	"gioui.org/font"
	"gioui.org/text"
	"gioui.org/unit"
	"github.com/raptordnj/goti/internal/domain"
)

// Spacing scale.
const (
	S0 unit.Dp = 2
	S1 unit.Dp = 4
	S2 unit.Dp = 8
	S3 unit.Dp = 12
	S4 unit.Dp = 16
	S5 unit.Dp = 20
	S6 unit.Dp = 24
)

// Type scale.
const (
	Caption unit.Sp = 11
	Body    unit.Sp = 13
	Title   unit.Sp = 15
	Display unit.Sp = 18
)

// TextStyle is a size plus weight.
type TextStyle struct {
	Size   unit.Sp
	Weight font.Weight
}

// Text styles.
var (
	StyleCaption    = TextStyle{Caption, font.Medium}
	StyleBody       = TextStyle{Body, font.Normal}
	StyleBodyStrong = TextStyle{Body, font.Medium}
	StyleTitle      = TextStyle{Title, font.SemiBold}
	StyleDisplay    = TextStyle{Display, font.SemiBold}
)

// Corner radii.
const (
	RadiusControl unit.Dp = 6
	RadiusRow     unit.Dp = 8
	RadiusPanel   unit.Dp = 12
)

// Palette is the set of color tokens.
type Palette struct {
	Bg, Surface, SurfaceSunken, Hairline, Text, TextMuted, TextFaint,
	Accent, AccentSoft, OnAccent, Success, Warning, Error, Focus color.NRGBA
}

func rgb(v uint32) color.NRGBA {
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xff}
}

// Light returns the light palette.
func Light() Palette {
	accent := rgb(0x3461F5)
	return Palette{
		Bg: rgb(0xF7F8FA), Surface: rgb(0xFFFFFF), SurfaceSunken: rgb(0xF1F3F6), Hairline: rgb(0xE4E7EC),
		Text: rgb(0x111418), TextMuted: rgb(0x5E6673), TextFaint: rgb(0x8A919C),
		Accent: accent, AccentSoft: WithAlpha(accent, 0x1A), OnAccent: rgb(0xFFFFFF),
		Success: rgb(0x12805C), Warning: rgb(0xA15C07), Error: rgb(0xC8322B),
		Focus: WithAlpha(accent, 0x8C), // 55 %
	}
}

// Dark returns the dark palette.
func Dark() Palette {
	accent := rgb(0x6C8CFF)
	return Palette{
		Bg: rgb(0x0E0F12), Surface: rgb(0x16181D), SurfaceSunken: rgb(0x1C1F25), Hairline: rgb(0x272B33),
		Text: rgb(0xECEEF1), TextMuted: rgb(0xA0A7B3), TextFaint: rgb(0x6F7682),
		Accent: accent, AccentSoft: WithAlpha(accent, 0x1A), OnAccent: rgb(0x0B1020),
		Success: rgb(0x3DD39A), Warning: rgb(0xF5B13D), Error: rgb(0xFF7A70),
		Focus: WithAlpha(accent, 0x99), // 60 %
	}
}

// StateColor is the semantic colour of a download state.
func StateColor(p Palette, s domain.State, interrupted bool) color.NRGBA {
	if interrupted {
		return p.Warning
	}
	switch s {
	case domain.StatePaused:
		return p.Warning
	case domain.StateCompleted:
		return p.Success
	case domain.StateFailed, domain.StateCancelled:
		return p.Error
	case domain.StateQueued, domain.StateInspecting:
		return p.TextMuted
	}
	return p.Accent
}

// Theme is a palette plus the text shaper.
type Theme struct {
	Palette
	Shaper *text.Shaper
}

// New builds a Theme with the embedded Inter fonts.
func New(p Palette) *Theme {
	return &Theme{Palette: p, Shaper: text.NewShaper(text.WithCollection(Faces()))}
}

// WithAlpha returns c with alpha a (for hover/pressed/disabled overlays).
func WithAlpha(c color.NRGBA, a uint8) color.NRGBA { c.A = a; return c }

// Mix blends a toward b by t in [0,1].
func Mix(a, b color.NRGBA, t float32) color.NRGBA {
	m := func(x, y uint8) uint8 { return uint8(float32(x) + (float32(y)-float32(x))*t) }
	return color.NRGBA{R: m(a.R, b.R), G: m(a.G, b.G), B: m(a.B, b.B), A: m(a.A, b.A)}
}
