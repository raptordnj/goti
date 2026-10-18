//go:build snapshot

// Package snapshot renders SwiftFetch screens headlessly to PNG for visual
// review. It is development tooling, not used by the application.
package snapshot

import (
	"errors"
	"fmt"
	"image"

	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"github.com/raptordnj/swiftfetch/internal/ui/anim"
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
	// Without an input source gtx.Enabled() is false and every control
	// renders disabled (the washed-out baseline buttons).
	var r input.Router
	// Screenshots show settled frames: no fades or eased transitions.
	prev := anim.Reduce
	anim.Reduce = func() bool { return true }
	defer func() { anim.Reduce = prev }()
	var ops op.Ops
	gtx := layout.Context{
		Source:      r.Source(),
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
