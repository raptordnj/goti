package mobile

import (
	"testing"
	"time"
)

func TestControllerStartsOnPending(t *testing.T) {
	h := &fakeHost{}
	c := NewController(h)
	c.Tick(snap(1, 0, 0, 0), t0)
	if len(h.calls) != 2 || h.calls[0] != "start" || h.calls[1] != "update" {
		t.Fatalf("%v", h.calls)
	}
	c2 := NewController(&fakeHost{})
	c2.Tick(snap(0, 0, 0, 0), t0)
}

func TestControllerThrottlesUpdates(t *testing.T) {
	h := &fakeHost{}
	c := NewController(h)
	for i := 0; i < 10; i++ {
		c.Tick(snap(1, 0, 0, 0), t0.Add(time.Duration(i)*90*time.Millisecond))
	}
	if h.count("update") != 1 {
		t.Fatalf("%v", h.calls)
	}
	c.Tick(snap(1, 0, 0, 0), t0.Add(time.Second))
	if h.count("update") != 2 {
		t.Fatalf("%v", h.calls)
	}
	c.Tick(snap(2, 0, 0, 0), t0.Add(1100*time.Millisecond)) // title changed
	if h.count("update") != 3 {
		t.Fatalf("%v", h.calls)
	}
}

func TestControllerStopsWhenIdle(t *testing.T) {
	h := &fakeHost{}
	c := NewController(h)
	c.Tick(snap(1, 0, 0, 5), t0)
	c.Tick(snap(0, 0, 0, 7), t0.Add(time.Second))
	if len(h.stopped) != 1 || h.stopped[0] != 2 {
		t.Fatalf("%v", h.stopped)
	}
	c.Tick(snap(0, 0, 0, 7), t0.Add(2*time.Second))
	if len(h.stopped) != 1 {
		t.Fatalf("stopped twice")
	}
}

func TestControllerPausedTimeout(t *testing.T) {
	h := &fakeHost{}
	c := NewController(h)
	c.Tick(snap(1, 0, 0, 0), t0.Add(-time.Minute))
	c.Tick(snap(0, 0, 1, 0), t0)
	c.Tick(snap(0, 0, 1, 0), t0.Add(10*time.Minute-time.Second))
	if len(h.stopped) != 0 {
		t.Fatal("stopped early")
	}
	last := h.updates[len(h.updates)-1]
	if !last.Paused {
		t.Fatalf("%+v", last)
	}
	c.Tick(snap(0, 0, 1, 0), t0.Add(10*time.Minute))
	if len(h.stopped) != 1 {
		t.Fatal("not stopped")
	}
}

func TestControllerRestartsAfterStop(t *testing.T) {
	h := &fakeHost{}
	c := NewController(h)
	c.Tick(snap(1, 0, 0, 0), t0)
	c.Tick(snap(0, 0, 0, 1), t0.Add(time.Second))
	c.Tick(snap(0, 1, 0, 1), t0.Add(2*time.Second))
	if h.count("start") != 2 {
		t.Fatalf("%v", h.calls)
	}
	c.Tick(snap(0, 0, 0, 2), t0.Add(3*time.Second))
	if h.stopped[1] != 1 {
		t.Fatalf("%v", h.stopped)
	}
}
