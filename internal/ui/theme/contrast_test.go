package theme

import (
	"image/color"
	"math"
	"testing"
)

func lin(c uint8) float64 {
	v := float64(c) / 255
	if v <= 0.03928 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

func lum(c color.NRGBA) float64 {
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

func ratio(a, b color.NRGBA) float64 {
	la, lb := lum(a), lum(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func TestContrast(t *testing.T) {
	for name, p := range map[string]Palette{"light": Light(), "dark": Dark()} {
		pairs := []struct {
			n      string
			fg, bg color.NRGBA
		}{
			{"Text/Bg", p.Text, p.Bg},
			{"Text/Surface", p.Text, p.Surface},
			{"TextMuted/Surface", p.TextMuted, p.Surface},
			{"OnAccent/Accent", p.OnAccent, p.Accent},
			{"Error/Surface", p.Error, p.Surface},
		}
		for _, pr := range pairs {
			if r := ratio(pr.fg, pr.bg); r < 4.5 {
				t.Errorf("%s %s contrast %.2f < 4.5", name, pr.n, r)
			}
		}
	}
}

func TestNewTheme(t *testing.T) {
	th := New(Dark())
	if th.Shaper == nil || th.Palette != Dark() {
		t.Fatal("theme not initialised")
	}
}
