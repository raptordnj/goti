// Command genicons renders the Goti app mark (accent rounded square with a
// white down arrow) to PNG sizes, a multi-size ICO and a macOS iconset.
//
// Usage: go run ./packaging/cmd/genicons [-out packaging/icons]
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"

	"golang.org/x/image/vector"
)

var accent = color.NRGBA{R: 0x34, G: 0x61, B: 0xF5, A: 0xFF}

// render draws the mark at size×size pixels.
func render(size int) *image.NRGBA {
	s := float32(size)
	img := image.NewNRGBA(image.Rect(0, 0, size, size))

	// Rounded square, corner radius 22% of the side.
	r := 0.22 * s
	var bg vector.Rasterizer
	bg.Reset(size, size)
	roundRect(&bg, 0, 0, s, s, r)
	mask := image.NewAlpha(img.Bounds())
	bg.Draw(mask, mask.Bounds(), image.Opaque, image.Point{})
	draw.DrawMask(img, img.Bounds(), image.NewUniform(accent), image.Point{}, mask, image.Point{}, draw.Src)

	// White down arrow plus base bar, on a 100-unit grid.
	u := s / 100
	var fg vector.Rasterizer
	fg.Reset(size, size)
	poly(&fg, u,
		[2]float32{42, 24}, [2]float32{58, 24}, [2]float32{58, 46},
		[2]float32{72, 46}, [2]float32{50, 68}, [2]float32{28, 46},
		[2]float32{42, 46})
	rect(&fg, 28*u, 74*u, 72*u, 80*u)
	am := image.NewAlpha(img.Bounds())
	fg.Draw(am, am.Bounds(), image.Opaque, image.Point{})
	draw.DrawMask(img, img.Bounds(), image.NewUniform(color.NRGBA{255, 255, 255, 255}), image.Point{}, am, image.Point{}, draw.Over)
	return img
}

func poly(z *vector.Rasterizer, u float32, pts ...[2]float32) {
	z.MoveTo(pts[0][0]*u, pts[0][1]*u)
	for _, p := range pts[1:] {
		z.LineTo(p[0]*u, p[1]*u)
	}
	z.ClosePath()
}

func rect(z *vector.Rasterizer, x0, y0, x1, y1 float32) {
	z.MoveTo(x0, y0)
	z.LineTo(x1, y0)
	z.LineTo(x1, y1)
	z.LineTo(x0, y1)
	z.ClosePath()
}

// roundRect adds a rounded rectangle; corners are quadratic approximations of
// quarter circles, accurate enough at icon sizes.
func roundRect(z *vector.Rasterizer, x0, y0, x1, y1, r float32) {
	z.MoveTo(x0+r, y0)
	z.LineTo(x1-r, y0)
	z.QuadTo(x1, y0, x1, y0+r)
	z.LineTo(x1, y1-r)
	z.QuadTo(x1, y1, x1-r, y1)
	z.LineTo(x0+r, y1)
	z.QuadTo(x0, y1, x0, y1-r)
	z.LineTo(x0, y0+r)
	z.QuadTo(x0, y0, x0+r, y0)
	z.ClosePath()
}

func encodePNG(img image.Image) []byte {
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		panic(err)
	}
	return b.Bytes()
}

// ico packs PNG-encoded images (each ≤256 px) into an ICO container.
func ico(sizes []int) []byte {
	var head, data bytes.Buffer
	_ = binary.Write(&head, binary.LittleEndian, [3]uint16{0, 1, uint16(len(sizes))})
	off := 6 + 16*len(sizes)
	for _, sz := range sizes {
		p := encodePNG(render(sz))
		w := byte(sz)
		if sz >= 256 {
			w = 0
		}
		head.Write([]byte{w, w, 0, 0})
		_ = binary.Write(&head, binary.LittleEndian, [2]uint16{1, 32})
		_ = binary.Write(&head, binary.LittleEndian, [2]uint32{uint32(len(p)), uint32(off + data.Len())})
		data.Write(p)
	}
	return append(head.Bytes(), data.Bytes()...)
}

func main() {
	out := flag.String("out", "packaging/icons", "output directory")
	flag.Parse()
	iconset := filepath.Join(*out, "goti.iconset")
	must(os.MkdirAll(iconset, 0o755))
	for _, sz := range []int{16, 32, 48, 64, 128, 256, 512, 1024} {
		must(os.WriteFile(filepath.Join(*out, fmt.Sprintf("goti-%d.png", sz)), encodePNG(render(sz)), 0o644))
	}
	must(os.WriteFile(filepath.Join(*out, "goti.png"), encodePNG(render(512)), 0o644))
	must(os.WriteFile(filepath.Join(*out, "goti.ico"), ico([]int{16, 24, 32, 48, 64, 128, 256}), 0o644))
	for _, sz := range []int{16, 32, 128, 256, 512} {
		must(os.WriteFile(filepath.Join(iconset, fmt.Sprintf("icon_%dx%d.png", sz, sz)), encodePNG(render(sz)), 0o644))
		must(os.WriteFile(filepath.Join(iconset, fmt.Sprintf("icon_%dx%d@2x.png", sz, sz)), encodePNG(render(sz*2)), 0o644))
	}
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "genicons:", err)
		os.Exit(1)
	}
}
