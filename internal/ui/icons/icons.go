// Package icons draws SwiftFetch's vector line icons with Gio paths.
//
// Icons are designed on a 16-unit grid with a 1.5-unit stroke (Lucide/
// Feather style) and scale with the requested size. Play and Pause are the
// only filled glyphs. This package must not import internal/ui/components.
package icons

import (
	"image"
	"image/color"
	"math"
	"path/filepath"
	"strings"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// Icon draws itself in a size×size square using colour c.
type Icon func(gtx layout.Context, size unit.Dp, c color.NRGBA) layout.Dimensions

const (
	grid   = 16
	stroke = 1.5
)

// pen builds a path in grid units, scaled to pixels.
type pen struct {
	p clip.Path
	s float32
}

func (q *pen) pt(x, y float32) f32.Point { return f32.Pt(x*q.s, y*q.s) }
func (q *pen) m(x, y float32)            { q.p.MoveTo(q.pt(x, y)) }
func (q *pen) l(x, y float32)            { q.p.LineTo(q.pt(x, y)) }
func (q *pen) quad(cx, cy, x, y float32) { q.p.QuadTo(q.pt(cx, cy), q.pt(x, y)) }

// poly draws an open polyline.
func (q *pen) poly(xy ...float32) {
	q.m(xy[0], xy[1])
	for i := 2; i+1 < len(xy); i += 2 {
		q.l(xy[i], xy[i+1])
	}
}

// closed draws a closed polygon.
func (q *pen) closed(xy ...float32) { q.poly(xy...); q.p.Close() }

// arc draws an arc of radius r around (cx,cy) starting at angle a0
// (radians, 0 = +x, positive = clockwise on screen) sweeping by sweep.
func (q *pen) arc(cx, cy, r, a0, sweep float32) {
	q.m(cx+r*cos(a0), cy+r*sin(a0))
	c := q.pt(cx, cy)
	q.p.ArcTo(c, c, sweep)
}

func (q *pen) circle(cx, cy, r float32) { q.arc(cx, cy, r, 0, 2*math.Pi); q.p.Close() }

// rrect draws a rounded rectangle.
func (q *pen) rrect(x0, y0, x1, y1, r float32) {
	q.m(x0+r, y0)
	q.l(x1-r, y0)
	q.quad(x1, y0, x1, y0+r)
	q.l(x1, y1-r)
	q.quad(x1, y1, x1-r, y1)
	q.l(x0+r, y1)
	q.quad(x0, y1, x0, y1-r)
	q.l(x0, y0+r)
	q.quad(x0, y0, x0+r, y0)
	q.p.Close()
}

func cos(a float32) float32 { return float32(math.Cos(float64(a))) }
func sin(a float32) float32 { return float32(math.Sin(float64(a))) }

// lined makes a stroked icon; fill, if non-nil, adds filled shapes.
func lined(draw func(q *pen), fill func(q *pen)) Icon {
	return func(gtx layout.Context, size unit.Dp, c color.NRGBA) layout.Dimensions {
		px := gtx.Dp(size)
		s := float32(px) / grid
		if draw != nil {
			q := &pen{s: s}
			q.p.Begin(gtx.Ops)
			draw(q)
			paint.FillShape(gtx.Ops, c, clip.Stroke{Path: q.p.End(), Width: stroke * s}.Op())
		}
		if fill != nil {
			q := &pen{s: s}
			q.p.Begin(gtx.Ops)
			fill(q)
			paint.FillShape(gtx.Ops, c, clip.Outline{Path: q.p.End()}.Op())
		}
		return layout.Dimensions{Size: image.Pt(px, px)}
	}
}

func filePath(q *pen) {
	q.closed(3.5, 1.5, 9.5, 1.5, 12.5, 4.5, 12.5, 14.5, 3.5, 14.5)
	q.poly(9.5, 1.5, 9.5, 4.5, 12.5, 4.5)
}

// Icon set.
var (
	Plus = lined(func(q *pen) { q.poly(8, 3, 8, 13); q.poly(3, 8, 13, 8) }, nil)

	Close = lined(func(q *pen) { q.poly(4, 4, 12, 12); q.poly(12, 4, 4, 12) }, nil)

	Check = lined(func(q *pen) { q.poly(3, 8.5, 6.5, 12, 13, 4.5) }, nil)

	ChevronRight = lined(func(q *pen) { q.poly(6, 3.5, 10.5, 8, 6, 12.5) }, nil)

	ChevronDown = lined(func(q *pen) { q.poly(3.5, 6, 8, 10.5, 12.5, 6) }, nil)

	ArrowDown = lined(func(q *pen) {
		q.poly(8, 2.5, 8, 13)
		q.poly(3.5, 8.5, 8, 13, 12.5, 8.5)
	}, nil)

	Play = lined(nil, func(q *pen) {
		q.m(4.5, 3)
		q.quad(4.5, 2, 5.4, 2.5)
		q.l(12.6, 7.1)
		q.quad(13.5, 8, 12.6, 8.9)
		q.l(5.4, 13.5)
		q.quad(4.5, 14, 4.5, 13)
		q.p.Close()
	})

	Pause = lined(nil, func(q *pen) {
		q.rrect(3.5, 2.5, 6.5, 13.5, 0.75)
		q.rrect(9.5, 2.5, 12.5, 13.5, 0.75)
	})

	Retry = lined(func(q *pen) {
		// Counter-clockwise rotate arrow: arc from the left through the
		// bottom and right to the top, curling back to the arrow head.
		q.arc(8, 8, 5.75, math.Pi, -1.5*math.Pi)
		q.quad(4.6, 2.25, 2.5, 5.5)
		q.poly(2.5, 2, 2.5, 5.5, 6, 5.5)
	}, nil)

	Restart = lined(func(q *pen) {
		q.arc(8, 8, 5.75, 0, 1.5*math.Pi)
		q.quad(11.4, 2.25, 13.5, 5.5)
		q.poly(13.5, 2, 13.5, 5.5, 10, 5.5)
	}, nil)

	Folder = lined(func(q *pen) {
		q.m(1.75, 4)
		q.quad(1.75, 2.75, 3, 2.75)
		q.l(5.75, 2.75)
		q.l(7.25, 4.5)
		q.l(13, 4.5)
		q.quad(14.25, 4.5, 14.25, 5.75)
		q.l(14.25, 12)
		q.quad(14.25, 13.25, 13, 13.25)
		q.l(3, 13.25)
		q.quad(1.75, 13.25, 1.75, 12)
		q.p.Close()
	}, nil)

	File = lined(filePath, nil)

	Doc = lined(func(q *pen) {
		filePath(q)
		q.poly(5.75, 8.25, 10.25, 8.25)
		q.poly(5.75, 11, 10.25, 11)
		q.poly(5.75, 5.5, 7, 5.5)
	}, nil)

	Archive = lined(func(q *pen) {
		q.rrect(1.75, 2.25, 14.25, 5.5, 0.75)
		q.m(2.75, 5.5)
		q.l(2.75, 12.5)
		q.quad(2.75, 13.75, 4, 13.75)
		q.l(12, 13.75)
		q.quad(13.25, 13.75, 13.25, 12.5)
		q.l(13.25, 5.5)
		q.poly(6.5, 8.5, 9.5, 8.5)
	}, nil)

	Video = lined(func(q *pen) {
		q.rrect(1.5, 4, 10.5, 12, 1.25)
		q.closed(10.5, 7, 14.5, 4.75, 14.5, 11.25, 10.5, 9)
	}, nil)

	Audio = lined(func(q *pen) {
		q.poly(6, 12, 6, 3.25, 13, 1.75, 13, 10.5)
		q.circle(4, 12, 2)
		q.circle(11, 10.5, 2)
	}, nil)

	Image = lined(func(q *pen) {
		q.rrect(2, 2, 14, 14, 1.5)
		q.circle(6, 6, 1.25)
		q.poly(14, 10, 10.75, 6.75, 3, 14)
	}, nil)

	Disk = lined(func(q *pen) {
		q.circle(8, 8, 6.25)
		q.circle(8, 8, 1.5)
	}, nil)

	More = lined(nil, func(q *pen) {
		q.circle(3, 8, 1.15)
		q.circle(8, 8, 1.15)
		q.circle(13, 8, 1.15)
	})

	Settings = lined(func(q *pen) {
		gear(q, 8, 8, 6.5, 4.9, 8)
		q.circle(8, 8, 2)
	}, nil)

	Alert = lined(func(q *pen) {
		q.circle(8, 8, 6.25)
		q.poly(8, 4.75, 8, 8.5)
	}, func(q *pen) { q.circle(8, 11.1, 0.85) })

	WifiOff = lined(func(q *pen) {
		q.arc(8, 13.25, 3.5, -0.75*math.Pi, 0.5*math.Pi)
		q.arc(8, 13.25, 7, -0.75*math.Pi, 0.5*math.Pi)
		q.poly(2, 2, 14, 14)
	}, func(q *pen) { q.circle(8, 13.25, 0.9) })
)

// gear draws an n-toothed gear outline centred at (cx,cy).
func gear(q *pen, cx, cy, ro, ri float32, n int) {
	step := 2 * math.Pi / float32(n)
	for i := 0; i < n; i++ {
		a := float32(i) * step
		// tooth top spans 30 % of the step, flanks 15 % each, valley 40 %
		pts := [][2]float32{
			{ri, a - 0.35*step}, {ro, a - 0.18*step}, {ro, a + 0.18*step}, {ri, a + 0.35*step},
		}
		for j, p := range pts {
			x, y := cx+p[0]*cos(p[1]), cy+p[0]*sin(p[1])
			if i == 0 && j == 0 {
				q.m(x, y)
			} else {
				q.l(x, y)
			}
		}
	}
	q.p.Close()
}

// File kinds returned by KindFor.
const (
	KindArchive = "archive"
	KindVideo   = "video"
	KindAudio   = "audio"
	KindImage   = "image"
	KindDoc     = "doc"
	KindDisk    = "disk"
	KindFile    = "file"
)

var extKinds = map[string]string{}

func init() {
	for kind, exts := range map[string]string{
		KindArchive: "zip rar 7z tar gz tgz bz2 xz zst",
		KindVideo:   "mp4 mkv avi mov webm flv wmv m4v",
		KindAudio:   "mp3 flac wav ogg m4a aac opus",
		KindImage:   "png jpg jpeg gif webp bmp svg tiff heic",
		KindDoc:     "pdf doc docx txt md odt rtf epub xls xlsx ppt pptx csv",
		KindDisk:    "iso img dmg vhd vmdk",
	} {
		for _, e := range strings.Fields(exts) {
			extKinds[e] = kind
		}
	}
}

// KindFor classifies a file name by extension (case-insensitive).
func KindFor(name string) string {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
	if k, ok := extKinds[ext]; ok {
		return k
	}
	return KindFile
}

// ForKind returns the icon for a kind from KindFor.
func ForKind(kind string) Icon {
	switch kind {
	case KindArchive:
		return Archive
	case KindVideo:
		return Video
	case KindAudio:
		return Audio
	case KindImage:
		return Image
	case KindDoc:
		return Doc
	case KindDisk:
		return Disk
	}
	return File
}

// ForFile picks an icon from the file name's extension.
func ForFile(name string) Icon { return ForKind(KindFor(name)) }
