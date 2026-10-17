package strategy

import "time"

// Adaptive concurrency tuning (spec §8).
const (
	alpha                        = 0.3
	stableWindowsBeforeProbe     = 2
	probeWindows                 = 2
	minGain                      = 0.10
	cooldownAfterRevert          = 6
	cooldownAfterThrottle        = 12
	timeoutWindowsToConservative = 3
	stableWindowsToRecover       = 10
	diskBlockedLimit             = 0.5
	// maxRetryAfter (10 min) is shared with the inspector (inspector.go).

	// timeoutsPerWindowLimit is the per-window timeout/reset count that
	// triggers a decrease.
	timeoutsPerWindowLimit = 2
)

// Sample aggregates one measurement window. Window sizing is the caller's
// job; the controller trusts each Sample.
type Sample struct {
	Window           time.Duration
	UsefulBytes      int64
	Conns            int
	Errors           int
	Throttled        int // 429/503 responses
	RetryAfter       time.Duration
	TimeoutsResets   int
	WriteBlockedFrac float64
}

// ActionKind is what the engine should do after a window.
type ActionKind int

// Action kinds.
const (
	Hold ActionKind = iota
	Increase
	Decrease
	SwitchConservative
	Backoff
)

// Action is the controller's instruction. Target is the connection count to
// run with; Delay is the task-level backoff for Backoff.
type Action struct {
	Kind   ActionKind
	Target int
	Delay  time.Duration
}

// ControllerConfig configures a Controller. Adaptive=false pins concurrency
// at Initial except that throttling still halves it.
type ControllerConfig struct {
	Initial, Max int
	Adaptive     bool
}

// Controller is the pure adaptive-concurrency state machine of spec §8. It is
// not safe for concurrent use.
type Controller struct {
	cfg     ControllerConfig
	n       int
	ceiling int // current max: cfg.Max, or 2 while conservative

	ewma     float64
	haveEWMA bool

	stable   int // consecutive stable windows outside cooldown
	cooldown int

	probing     bool
	probeLeft   int
	baseN       int
	baseEWMA    float64
	timeoutRun  int
	conserv     bool
	recoverRun  int
	manualDrops bool // manual mode reduced below Initial by throttling
}

// NewController returns a Controller starting at c.Initial, clamped to
// [1, Max].
func NewController(c ControllerConfig) *Controller {
	c.Max = max(c.Max, 1)
	c.Initial = min(max(c.Initial, 1), c.Max)
	return &Controller{cfg: c, n: c.Initial, ceiling: c.Max}
}

// Current returns the connection count the controller wants.
func (c *Controller) Current() int { return c.n }

// EWMA returns the smoothed useful throughput (bytes/s) at Current.
func (c *Controller) EWMA() float64 { return c.ewma }

// Observe feeds one window and returns the action to take.
func (c *Controller) Observe(s Sample) Action {
	c.updateEWMA(s)

	if s.Throttled > 0 {
		return c.throttle(s)
	}
	if !c.cfg.Adaptive {
		return c.observeManual()
	}

	if s.TimeoutsResets >= timeoutsPerWindowLimit {
		return c.timeouts()
	}
	c.timeoutRun = 0

	if c.probing {
		c.probeLeft--
		if c.probeLeft > 0 {
			return c.hold()
		}
		c.probing = false
		if c.ewma >= c.baseEWMA*(1+minGain) {
			c.stable = 0
			return c.hold()
		}
		c.n, c.ewma, c.haveEWMA = c.baseN, c.baseEWMA, true
		c.cooldown, c.stable = cooldownAfterRevert, 0
		return Action{Kind: Decrease, Target: c.n}
	}

	if c.cooldown > 0 {
		c.cooldown--
		return c.hold()
	}

	if s.Errors > 0 {
		c.stable, c.recoverRun = 0, 0
		return c.hold()
	}
	c.stable++
	if c.conserv {
		c.recoverRun++
		if c.recoverRun >= stableWindowsToRecover {
			c.conserv, c.ceiling, c.stable = false, c.cfg.Max, 0
		}
	}

	if c.stable >= stableWindowsBeforeProbe && c.n < c.ceiling && s.WriteBlockedFrac <= diskBlockedLimit {
		c.probing, c.probeLeft = true, probeWindows
		c.baseN, c.baseEWMA = c.n, c.ewma
		c.n++
		c.haveEWMA, c.stable = false, 0
		return Action{Kind: Increase, Target: c.n}
	}
	return c.hold()
}

func (c *Controller) updateEWMA(s Sample) {
	if s.Window <= 0 {
		return
	}
	rate := float64(s.UsefulBytes) / s.Window.Seconds()
	if !c.haveEWMA {
		c.ewma, c.haveEWMA = rate, true
		return
	}
	c.ewma = alpha*rate + (1-alpha)*c.ewma
}

func (c *Controller) hold() Action { return Action{Kind: Hold, Target: c.n} }

// throttle halves concurrency and backs off; it applies in manual mode too.
func (c *Controller) throttle(s Sample) Action {
	c.n = max(1, c.n/2)
	c.probing, c.stable, c.timeoutRun, c.recoverRun = false, 0, 0, 0
	c.haveEWMA = false
	c.cooldown = cooldownAfterThrottle
	c.manualDrops = !c.cfg.Adaptive && c.n < c.cfg.Initial
	return Action{Kind: Backoff, Target: c.n, Delay: min(s.RetryAfter, maxRetryAfter)}
}

// observeManual holds at Initial, restoring it once a throttle cooldown ends.
func (c *Controller) observeManual() Action {
	if c.cooldown > 0 {
		c.cooldown--
		return c.hold()
	}
	if c.manualDrops {
		c.manualDrops, c.n, c.haveEWMA = false, c.cfg.Initial, false
		return Action{Kind: Increase, Target: c.n}
	}
	return c.hold()
}

func (c *Controller) timeouts() Action {
	c.probing, c.stable, c.recoverRun = false, 0, 0
	c.haveEWMA = false
	c.timeoutRun++
	if c.timeoutRun >= timeoutWindowsToConservative && !c.conserv {
		c.conserv, c.ceiling = true, min(conservativeMaxConns, c.cfg.Max)
		c.n = max(1, min(c.n-1, c.ceiling))
		return Action{Kind: SwitchConservative, Target: c.n}
	}
	c.n = max(1, c.n-1)
	return Action{Kind: Decrease, Target: c.n}
}
