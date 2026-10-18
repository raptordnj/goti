// Package snapshot renders SwiftFetch screens headlessly to PNG for visual
// review. It is development tooling, not used by the application.
package snapshot

import (
	"image"

	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"

	"github.com/raptordnj/swiftfetch/internal/ui/theme"
)

// Render lays out lay in a w x h window and returns the screenshot.
func Render(w, h int, th *theme.Theme, lay func(gtx layout.Context)) (*image.RGBA, error) {
	win, err := headless.NewWindow(w, h)
	if err != nil {
		return nil, err
	}
	defer win.Release()
	var ops op.Ops
	gtx := layout.Context{
		Ops:         &ops,
		Constraints: layout.Exact(image.Pt(w, h)),
		Metric:      unitMetric(),
	}
	_ = th
	lay(gtx)
	if err := win.Frame(&ops); err != nil {
		return nil, err
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	if err := win.Screenshot(img); err != nil {
		return nil, err
	}
	return img, nil
}
