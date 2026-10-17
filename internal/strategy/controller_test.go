package strategy

import (
	"testing"
	"time"
)

// good returns a clean 2 s sample at rate bytes/s with conns connections.
func good(conns int, rate float64) Sample {
	return Sample{Window: 2 * time.Second, UsefulBytes: int64(rate * 2), Conns: conns}
}

func expect(t *testing.T, got Action, kind ActionKind, target int) {
	t.Helper()
	if got.Kind != kind || got.Target != target {
		t.Fatalf("action = %+v, want kind %v target %d", got, kind, target)
	}
}

func adaptive(initial, maxN int) *Controller {
	return NewController(ControllerConfig{Initial: initial, Max: maxN, Adaptive: true})
}

func TestIncreaseRequiresStableWindows(t *testing.T) {
	c := adaptive(2, 8)
	expect(t, c.Observe(good(2, 1e6)), Hold, 2)
	expect(t, c.Observe(good(2, 1e6)), Increase, 3)
	if c.Current() != 3 {
		t.Fatalf("Current = %d", c.Current())
	}
}

func TestErrorsResetStability(t *testing.T) {
	c := adaptive(2, 8)
	c.Observe(good(2, 1e6))
	s := good(2, 1e6)
	s.Errors = 1
	expect(t, c.Observe(s), Hold, 2)
	expect(t, c.Observe(good(2, 1e6)), Hold, 2)
	expect(t, c.Observe(good(2, 1e6)), Increase, 3)
}

func TestRevertWithoutGain(t *testing.T) {
	c := adaptive(2, 8)
	c.Observe(good(2, 1e6))
	expect(t, c.Observe(good(2, 1e6)), Increase, 3)
	expect(t, c.Observe(good(3, 1.05e6)), Hold, 3)
	expect(t, c.Observe(good(3, 1.05e6)), Decrease, 2)
	for i := range cooldownAfterRevert {
		if a := c.Observe(good(2, 5e6)); a.Kind != Hold {
			t.Fatalf("cooldown window %d: %+v", i, a)
		}
	}
	// Cooldown over: two fresh stable windows are still required.
	expect(t, c.Observe(good(2, 1e6)), Hold, 2)
	expect(t, c.Observe(good(2, 1e6)), Increase, 3)
}

func TestKeepOnGain(t *testing.T) {
	c := adaptive(2, 8)
	c.Observe(good(2, 1e6))
	c.Observe(good(2, 1e6))
	c.Observe(good(3, 1.3e6))
	expect(t, c.Observe(good(3, 1.3e6)), Hold, 3)
	expect(t, c.Observe(good(3, 1.3e6)), Hold, 3)
	expect(t, c.Observe(good(3, 1.3e6)), Increase, 4)
}

func TestThrottleHalves(t *testing.T) {
	c := adaptive(8, 8)
	s := good(8, 1e6)
	s.Throttled, s.RetryAfter = 1, 20*time.Second
	a := c.Observe(s)
	if a != (Action{Kind: Backoff, Target: 4, Delay: 20 * time.Second}) {
		t.Fatalf("action = %+v", a)
	}
	s.RetryAfter = time.Hour
	a = c.Observe(s)
	if a.Kind != Backoff || a.Target != 2 || a.Delay != maxRetryAfter {
		t.Fatalf("action = %+v, want Backoff 2 capped %s", a, maxRetryAfter)
	}
	for range cooldownAfterThrottle + stableWindowsBeforeProbe - 1 {
		if a := c.Observe(good(2, 1e6)); a.Kind != Hold {
			t.Fatalf("increase during throttle cooldown: %+v", a)
		}
	}
	expect(t, c.Observe(good(2, 1e6)), Increase, 3)
}

func TestTimeoutsDecrease(t *testing.T) {
	c := adaptive(8, 8)
	bad := func() Sample { s := good(c.Current(), 1e6); s.TimeoutsResets = 2; return s }
	expect(t, c.Observe(bad()), Decrease, 7)
	expect(t, c.Observe(bad()), Decrease, 6)
	a := c.Observe(bad())
	if a.Kind != SwitchConservative || a.Target > 2 {
		t.Fatalf("action = %+v, want SwitchConservative target <= 2", a)
	}
	// Conservative: never above 2 until 10 stable windows.
	for i := range stableWindowsToRecover + 5 {
		c.Observe(good(c.Current(), 1e6))
		if i < stableWindowsToRecover-1 && c.Current() > 2 {
			t.Fatalf("window %d: Current %d > 2 while conservative", i, c.Current())
		}
	}
	// One timeout window alone does not reset the streak logic.
	c2 := adaptive(4, 8)
	s := good(4, 1e6)
	s.TimeoutsResets = 1
	expect(t, c2.Observe(s), Hold, 4)
}

func TestTimeoutStreakBroken(t *testing.T) {
	c := adaptive(8, 8)
	bad := func() Sample { s := good(c.Current(), 1e6); s.TimeoutsResets = 3; return s }
	c.Observe(bad())
	c.Observe(bad())
	c.Observe(good(c.Current(), 1e6))
	if a := c.Observe(bad()); a.Kind != Decrease {
		t.Fatalf("streak not broken: %+v", a)
	}
}

func TestDiskBoundNeverIncreases(t *testing.T) {
	c := adaptive(2, 8)
	for i := range 50 {
		s := good(2, 1e6*float64(i+1))
		s.WriteBlockedFrac = 0.6
		if a := c.Observe(s); a.Kind == Increase {
			t.Fatalf("increased while disk-bound: %+v", a)
		}
	}
}

func TestNeverExceedsMax(t *testing.T) {
	c := adaptive(2, 4)
	rate := 1e6
	for range 200 {
		rate *= 1.5
		if rate > 1e12 {
			rate = 1e12
		}
		c.Observe(good(c.Current(), rate))
		if c.Current() > 4 {
			t.Fatalf("Current %d > max 4", c.Current())
		}
	}
	if c := NewController(ControllerConfig{Initial: 20, Max: 4, Adaptive: true}); c.Current() != 4 {
		t.Fatalf("initial not clamped: %d", c.Current())
	}
}

func TestNeverBelowOne(t *testing.T) {
	c := adaptive(1, 8)
	for range 20 {
		s := good(c.Current(), 1e5)
		s.Throttled, s.TimeoutsResets = 1, 5
		if a := c.Observe(s); a.Target < 1 {
			t.Fatalf("target < 1: %+v", a)
		}
		if c.Current() < 1 {
			t.Fatalf("Current %d", c.Current())
		}
	}
	if c := NewController(ControllerConfig{}); c.Current() != 1 {
		t.Fatalf("zero config Current = %d", c.Current())
	}
}

func TestManualHolds(t *testing.T) {
	c := NewController(ControllerConfig{Initial: 6, Max: 6})
	for i := range 20 {
		s := good(6, 1e6*float64(i+1))
		if i%3 == 0 {
			s.TimeoutsResets = 4
		}
		expect(t, c.Observe(s), Hold, 6)
	}
	s := good(6, 1e6)
	s.Throttled = 2
	expect(t, c.Observe(s), Backoff, 3)
}

func TestEWMA(t *testing.T) {
	c := adaptive(2, 8)
	c.Observe(good(2, 1000))
	c.Observe(good(2, 2000))
	if got, want := c.EWMA(), 1000+alpha*1000; got < want-1e-9 || got > want+1e-9 {
		t.Fatalf("EWMA = %v, want %v", got, want)
	}
}

func TestStartConservativeRecovers(t *testing.T) {
	c := NewController(ControllerConfig{Initial: 1, Max: 8, Adaptive: true, StartConservative: true})
	for i := range stableWindowsToRecover {
		c.Observe(good(c.Current(), 1e6*float64(i+1)))
		if c.Current() > 2 {
			t.Fatalf("window %d: Current %d > 2 before recovery", i, c.Current())
		}
	}
	rate := 1e7
	for range 20 {
		rate *= 1.5
		c.Observe(good(c.Current(), rate))
	}
	if c.Current() <= 2 {
		t.Fatalf("never recovered above 2: Current %d", c.Current())
	}
}

func TestNegativeRetryAfterClamped(t *testing.T) {
	c := adaptive(4, 8)
	s := good(4, 1e6)
	s.Throttled, s.RetryAfter = 1, -time.Second
	if a := c.Observe(s); a.Delay != 0 {
		t.Fatalf("Delay = %s, want 0", a.Delay)
	}
}
