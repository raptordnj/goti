package theme

import (
	"gioui.org/font"
	"github.com/raptordnj/swiftfetch/internal/domain"
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
		var pairs []struct {
			n      string
			fg, bg color.NRGBA
			min    float64
		}
		add := func(n string, fg, bg color.NRGBA, m float64) {
			pairs = append(pairs, struct {
				n      string
				fg, bg color.NRGBA
				min    float64
			}{n, fg, bg, m})
		}
		for bn, bg := range map[string]color.NRGBA{"Bg": p.Bg, "Surface": p.Surface, "Sunken": p.SurfaceSunken} {
			add("Text/"+bn, p.Text, bg, 4.5)
			add("TextMuted/"+bn, p.TextMuted, bg, 4.5)
		}
		add("OnAccent/Accent", p.OnAccent, p.Accent, 4.5)
		for n, c := range map[string]color.NRGBA{"Success": p.Success, "Warning": p.Warning, "Error": p.Error, "Accent": p.Accent} {
			add(n+"/Surface", c, p.Surface, 4.5)
		}
		add("TextFaint/Surface", p.TextFaint, p.Surface, 3.0)
		for _, pr := range pairs {
			if r := ratio(pr.fg, pr.bg); r < pr.min {
				t.Errorf("%s %s contrast %.2f < %.1f", name, pr.n, r, pr.min)
			}
		}
		soft := Mix(p.Surface, p.Accent, float32(p.AccentSoft.A)/255)
		if r := ratio(soft, p.Surface); r < 1.1 {
			t.Errorf("%s AccentSoft/Surface ratio %.3f < 1.1", name, r)
		}
		if p.AccentSoft.A == 0 || p.Focus.A == 0 || p.Focus.A == 255 {
			t.Errorf("%s alpha tokens unset", name)
		}
	}
}

func TestStateColor(t *testing.T) {
	p := Light()
	cases := []struct {
		s    domain.State
		intr bool
		want color.NRGBA
	}{
		{domain.StateDownloading, false, p.Accent},
		{domain.StateQueued, false, p.TextMuted},
		{domain.StateInspecting, false, p.TextMuted},
		{domain.StatePaused, false, p.Warning},
		{domain.StateQueued, true, p.Warning},
		{domain.StateCompleted, false, p.Success},
		{domain.StateFailed, false, p.Error},
		{domain.StateCancelled, false, p.Error},
	}
	for _, c := range cases {
		if got := StateColor(p, c.s, c.intr); got != c.want {
			t.Errorf("%s intr=%v: got %v want %v", c.s, c.intr, got, c.want)
		}
	}
}

func TestInterLoads(t *testing.T) {
	faces := InterFaces()
	if len(faces) != 3 {
		t.Fatalf("faces = %d", len(faces))
	}
	want := []font.Weight{font.Normal, font.Medium, font.SemiBold}
	for i, f := range faces {
		if f.Font.Weight != want[i] || f.Font.Typeface != "Inter" {
			t.Errorf("face %d = %+v", i, f.Font)
		}
	}
	for _, r := range "…×·›−" {
		if !Covers(r) {
			t.Errorf("Inter lacks %q", r)
		}
	}
}

func TestNewTheme(t *testing.T) {
	th := New(Dark())
	if th.Shaper == nil || th.Palette != Dark() {
		t.Fatal("theme not initialised")
	}
}
