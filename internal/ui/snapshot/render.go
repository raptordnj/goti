//go:build snapshot

// Package snapshot renders SwiftFetch screens headlessly to PNG for visual
// review. It is development tooling, not used by the application.
package snapshot

import (
	"errors"
	"fmt"
	"image"

	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// ErrNoGPU wraps the error from creating the headless window.
var ErrNoGPU = errors.New("headless GPU unavailable")

// Render lays out lay in a w x h window (1 px per dp) and returns the screenshot.
func Render(w, h int, lay func(gtx layout.Context)) (*image.RGBA, error) {
	win, err := headless.NewWindow(w, h)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNoGPU, err)
	}
	defer win.Release()
	var ops op.Ops
	gtx := layout.Context{
		Ops:         &ops,
		Constraints: layout.Exact(image.Pt(w, h)),
		Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
	}
	lay(gtx)
	if err := win.Frame(&ops); err != nil {
		return nil, fmt.Errorf("frame: %w", err)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	if err := win.Screenshot(img); err != nil {
		return nil, fmt.Errorf("screenshot: %w", err)
	}
	return img, nil
}
