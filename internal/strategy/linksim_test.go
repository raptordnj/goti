package strategy

import (
	"math/rand/v2"
	"testing"
	"time"
)

// link models a network path: each connection can carry at most perConn
// bytes/s and the path carries at most total bytes/s. throttleAbove > 0 makes
// the server answer 429 when more connections are used.
type link struct {
	perConn, total float64
	throttleAbove  int
	noise          float64 // ±fraction, applied with rng
	rng            *rand.Rand
}

func (l link) sample(n int) Sample {
	rate := min(float64(n)*l.perConn, l.total)
	if l.noise > 0 {
		rate *= 1 + l.noise*(2*l.rng.Float64()-1)
	}
	s := Sample{Window: 2 * time.Second, UsefulBytes: int64(rate * 2), Conns: n}
	if l.throttleAbove > 0 && n > l.throttleAbove {
		s.Throttled, s.RetryAfter = 1, 5*time.Second
	}
	return s
}

// run feeds windows samples and returns Current() after each one.
func run(l link, windows int) []int {
	c := NewController(ControllerConfig{Initial: 2, Max: 8, Adaptive: true})
	out := make([]int, windows)
	for i := range out {
		c.Observe(l.sample(c.Current()))
		out[i] = c.Current()
	}
	return out
}

func TestSimPerConnectionCappedClimbs(t *testing.T) {
	trace := run(link{perConn: 1e6, total: 20e6}, 200)
	if final := trace[len(trace)-1]; final < 6 {
		t.Fatalf("final Current = %d, want >= 6 (trace %v)", final, trace)
	}
}

func TestSimBandwidthCappedHoldsLow(t *testing.T) {
	trace := run(link{perConn: 10e6, total: 5e6}, 200)
	low, excursion := 0, 0
	for i, n := range trace {
		if n <= 2 {
			low++
			excursion = 0
			continue
		}
		excursion++
		if n > 3 || excursion > probeWindows {
			t.Fatalf("window %d: Current %d, excursion %d windows (want <= 3 conns, <= %d windows) trace %v",
				i, n, excursion, probeWindows, trace)
		}
	}
	// With the spec §8 constants the steady state is exactly 80%: each
	// 10-window cycle is 2 probe windows at 3 conns, 6 cooldown windows and 2
	// stable windows at 2. The structural check above is what keeps it robust.
	if low*100 < 80*len(trace) {
		t.Fatalf("Current <= 2 in %d/%d windows, want >= 80%% (trace %v)", low, len(trace), trace)
	}
}

func TestSimThrottlingHalves(t *testing.T) {
	trace := run(link{perConn: 1e6, total: 100e6, throttleAbove: 3}, 200)
	run := 0
	for i, n := range trace {
		if n > 3 {
			run++
		} else {
			run = 0
		}
		if run > 2 {
			t.Fatalf("above 3 for %d consecutive windows ending at %d (trace %v)", run, i, trace)
		}
	}
}

func TestSimNoiseNoOscillation(t *testing.T) {
	for seed := uint64(1); seed <= 20; seed++ {
		rng := rand.New(rand.NewPCG(seed, seed*7919)) //nolint:gosec // deterministic simulation noise
		for _, l := range []link{
			{perConn: 10e6, total: 5e6, noise: 0.1, rng: rng},
			{perConn: 1e6, total: 4e6, noise: 0.1, rng: rng},
			{perConn: 1e6, total: 20e6, noise: 0.1, rng: rng},
		} {
			trace := run(l, 200)
			changes, prev := 0, 2
			for _, n := range trace {
				if n != prev {
					changes++
				}
				prev = n
			}
			if changes*4 > len(trace) {
				t.Fatalf("seed %d link %+v: %d changes in %d windows", seed, l, changes, len(trace))
			}
		}
	}
}
