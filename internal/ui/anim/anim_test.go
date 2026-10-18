package anim

import (
	"math"
	"testing"
	"time"

	"gioui.org/layout"
)

func withCounter(t *testing.T) *int {
	n := 0
	old := invalidate
	invalidate = func(layout.Context) { n++ }
	t.Cleanup(func() { invalidate = old })
	return &n
}

func TestValueConverges(t *testing.T) {
	withCounter(t)
	t0 := time.Unix(1000, 0)
	var v Value
	v.Get(layout.Context{Now: t0})
	v.Set(1)
	var x float32
	for ms := 0; ms <= 250; ms += 16 {
		x = v.Get(layout.Context{Now: t0.Add(time.Duration(ms) * time.Millisecond)})
	}
	x = v.Get(layout.Context{Now: t0.Add(250 * time.Millisecond)})
	if math.Abs(float64(1-x)) > 0.01 {
		t.Fatalf("x=%v after 250ms", x)
	}
	mid := Value{}
	mid.Get(layout.Context{Now: t0})
	mid.Set(1)
	if y := mid.Get(layout.Context{Now: t0.Add(30 * time.Millisecond)}); y <= 0 || y >= 0.9 {
		t.Fatalf("not eased: %v", y)
	}
}

func TestValueSettlesNoInvalidate(t *testing.T) {
	n := withCounter(t)
	t0 := time.Unix(1000, 0)
	var v Value
	v.Get(layout.Context{Now: t0})
	v.Set(1)
	for ms := 16; ms <= 1000; ms += 16 {
		v.Get(layout.Context{Now: t0.Add(time.Duration(ms) * time.Millisecond)})
	}
	if *n == 0 {
		t.Fatal("expected invalidates while animating")
	}
	*n = 0
	if x := v.Get(layout.Context{Now: t0.Add(1100 * time.Millisecond)}); x != 1 {
		t.Fatalf("not snapped: %v", x)
	}
	if *n != 0 {
		t.Fatalf("invalidated %d times after settle", *n)
	}
}

func TestReduceInstant(t *testing.T) {
	n := withCounter(t)
	old := Reduce
	Reduce = func() bool { return true }
	defer func() { Reduce = old }()
	t0 := time.Unix(1000, 0)
	var v Value
	v.Set(0.7)
	if x := v.Get(layout.Context{Now: t0}); x != 0.7 {
		t.Fatalf("got %v", x)
	}
	var f Fade
	f.Show(true)
	if a := f.Alpha(layout.Context{Now: t0}); a != 1 {
		t.Fatalf("alpha %v", a)
	}
	if *n != 0 {
		t.Fatal("invalidated under Reduce")
	}
}

func TestSnap(t *testing.T) {
	withCounter(t)
	var v Value
	v.Set(1)
	v.Snap(0.3)
	if x := v.Get(layout.Context{Now: time.Unix(5, 0)}); x != 0.3 {
		t.Fatalf("got %v", x)
	}
}

func TestFade(t *testing.T) {
	n := withCounter(t)
	t0 := time.Unix(1000, 0)
	var f Fade
	if a := f.Alpha(layout.Context{Now: t0}); a != 0 {
		t.Fatalf("initial %v", a)
	}
	f.Show(true)
	f.Alpha(layout.Context{Now: t0})
	mid := f.Alpha(layout.Context{Now: t0.Add(75 * time.Millisecond)})
	if mid <= 0.5 || mid >= 1 { // ease-out: past halfway at half time
		t.Fatalf("mid %v", mid)
	}
	if a := f.Alpha(layout.Context{Now: t0.Add(150 * time.Millisecond)}); a != 1 {
		t.Fatalf("end %v", a)
	}
	*n = 0
	f.Alpha(layout.Context{Now: t0.Add(200 * time.Millisecond)})
	if *n != 0 {
		t.Fatal("invalidate after done")
	}
	f.Show(false)
	f.Alpha(layout.Context{Now: t0.Add(300 * time.Millisecond)})
	if a := f.Alpha(layout.Context{Now: t0.Add(450 * time.Millisecond)}); a != 0 {
		t.Fatalf("hide end %v", a)
	}
}
