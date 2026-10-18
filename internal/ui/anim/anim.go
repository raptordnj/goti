// Package anim provides small frame-time animation primitives for Gio.
// It must not import internal/ui/components.
package anim

import (
	"math"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
)

// Reduce reports whether motion should be reduced; when true all
// animations jump to their target instantly.
var Reduce = func() bool { return false }

// invalidate requests another frame. Replaced in tests.
var invalidate = func(gtx layout.Context) { gtx.Execute(op.InvalidateCmd{}) }

// tau is the time constant of the exponential approach: 99 % of the
// distance is covered in ~180 ms (ln 100 ≈ 4.605).
const tau = 0.180 / 4.605170

const settleEps = 0.001

// Value eases toward a target with a critically damped exponential approach.
type Value struct {
	x, target float32
	last      time.Time
	init      bool
}

// Set changes the target value. The first Set on a Value never read
// (via Get) or snapped jumps directly, so initial state does not animate.
func (v *Value) Set(target float32) {
	if !v.init {
		v.x, v.init = target, true
	}
	v.target = target
}

// Snap sets both current and target value without animation.
func (v *Value) Snap(x float32) { v.x, v.target, v.init = x, x, true }

// Get advances the animation to gtx.Now and returns the current value,
// requesting a redraw while unsettled.
func (v *Value) Get(gtx layout.Context) float32 {
	v.init = true
	now := gtx.Now
	dt := 0.0
	if !v.last.IsZero() {
		dt = now.Sub(v.last).Seconds()
	}
	v.last = now
	if Reduce() {
		v.x = v.target
		return v.x
	}
	if dt > 0 {
		v.x += (v.target - v.x) * float32(1-math.Exp(-dt/tau))
	}
	if d := v.target - v.x; d < settleEps && d > -settleEps {
		v.x = v.target
		return v.x
	}
	invalidate(gtx)
	return v.x
}

// FadeDuration is the length of a Fade transition.
const FadeDuration = 150 * time.Millisecond

// Fade is a 150 ms ease-out-cubic opacity transition.
type Fade struct {
	shown bool
	p     float32 // linear progress 0 (hidden) .. 1 (shown)
	last  time.Time
}

// Show sets whether the faded element should be visible.
func (f *Fade) Show(b bool) { f.shown = b }

// Visible reports whether Alpha would be non-zero or is heading there.
func (f *Fade) Visible() bool { return f.shown || f.p > 0 }

// Alpha advances the fade to gtx.Now and returns opacity in [0,1].
func (f *Fade) Alpha(gtx layout.Context) float32 {
	now := gtx.Now
	dt := float32(0)
	if !f.last.IsZero() {
		dt = float32(now.Sub(f.last).Seconds() / FadeDuration.Seconds())
	}
	f.last = now
	target := float32(0)
	if f.shown {
		target = 1
	}
	if Reduce() {
		f.p = target
	} else if f.p < target {
		f.p = min(target, f.p+dt)
	} else if f.p > target {
		f.p = max(target, f.p-dt)
	}
	if f.p != target {
		invalidate(gtx)
	}
	if f.shown {
		return easeOut(f.p)
	}
	return 1 - easeOut(1-f.p)
}

func easeOut(t float32) float32 { u := 1 - t; return 1 - u*u*u }
