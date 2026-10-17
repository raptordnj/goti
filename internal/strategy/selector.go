package strategy

import (
	"fmt"
	"time"

	"github.com/raptordnj/swiftfetch/internal/config"
	"github.com/raptordnj/swiftfetch/internal/domain"
)

// Confidence expresses how trustworthy the evidence behind a Decision is
// (spec §5.2).
type Confidence string

// Confidence levels.
const (
	ConfidenceHigh   Confidence = "high"   // strong ETag + two consistent probes
	ConfidenceMedium Confidence = "medium" // Last-Modified only
	ConfidenceLow    Confidence = "low"    // otherwise
)

const (
	// conservativeRTT is the probe RTT above which strategy C is chosen.
	conservativeRTT = 400 * time.Millisecond
	// hardMaxConns caps per-download connections regardless of config.
	hardMaxConns = 16
	// defaultInitialConns is strategy B's starting concurrency.
	defaultInitialConns = 2
	// conservativeMaxConns and conservativeChunkMin define strategy C.
	conservativeMaxConns = 2
	conservativeChunkMin = 4 << 20
	// maxSegments bounds the number of chunks (spec §6.1).
	maxSegments = 4096
	mibBytes    = 1 << 20
)

// ResumeState is what was persisted about a previous attempt of a task.
type ResumeState struct {
	Validators domain.Validators
	Size       int64
	SizeKnown  bool
	Previous   domain.Strategy
}

// Decision is the outcome of Select (spec §5.2). When Resume is true,
// Strategy is the underlying single/multipart/conservative strategy used to
// continue. When RemoteChanged is true, the strategy describes a restart from
// zero and Err carries KindRemoteChanged.
type Decision struct {
	Strategy       domain.Strategy      `json:"strategy"`
	Resume         bool                 `json:"resume"`
	RemoteChanged  bool                 `json:"remote_changed"`
	Confidence     Confidence           `json:"confidence"`
	Size           int64                `json:"size"`
	SizeKnown      bool                 `json:"size_known"`
	RangesProven   bool                 `json:"ranges_proven"`
	Validator      domain.ValidatorKind `json:"validator"`
	InitialConns   int                  `json:"initial_conns"`
	MaxConns       int                  `json:"max_conns"`
	ChunkSize      int64                `json:"chunk_size"`
	Probes         []ProbeResult        `json:"probes"`
	Reasons        []string             `json:"reasons"`
	ChangeTriggers []string             `json:"change_triggers"`
	Err            *domain.Error        `json:"err,omitempty"`
}

// Select applies the spec §5.1 rules, in order, to obs. It is pure and
// deterministic.
func Select(obs Observations, cfg config.Config, resume *ResumeState) Decision {
	d := Decision{
		Size:         obs.Size,
		SizeKnown:    obs.SizeKnown,
		RangesProven: obs.RangesProven,
		Validator:    obs.Validators.Kind(),
		Probes:       obs.Probes,
		Confidence:   confidence(obs),
	}

	// E — unsupported/restricted.
	if obs.Err != nil {
		d.Strategy = domain.StrategyUnsupported
		d.Err = obs.Err
		d.Reasons = append(d.Reasons, fmt.Sprintf("inspection failed (%s): %s", obs.Err.Kind, obs.Err.Msg))
		if obs.Err.Action != "" {
			d.Reasons = append(d.Reasons, "recommended action: "+obs.Err.Action)
		}
		return d
	}

	// D — resume: decide whether stored progress may be reused.
	if resume != nil {
		switch {
		case resume.Validators.Kind() == domain.ValidatorNone:
			d.RemoteChanged = true
			d.Reasons = append(d.Reasons, "no validator stored: cannot verify the remote file is unchanged")
		case !resume.Validators.Matches(obs.Validators) || resume.SizeKnown != obs.SizeKnown || resume.Size != obs.Size:
			d.RemoteChanged = true
			d.Reasons = append(d.Reasons, "remote file changed: validators or size differ from the stored download")
		}
		if d.RemoteChanged {
			d.Err = domain.NewError(domain.KindRemoteChanged, "remote file changed",
				"restart the download from zero", nil)
		}
	}

	selectFresh(&d, obs, cfg)

	if resume != nil && !d.RemoteChanged {
		switch {
		case !obs.RangesProven:
			d.Reasons = append(d.Reasons, "ranges no longer proven: stored progress cannot be resumed, restarting")
		case resume.Previous == domain.StrategySingle:
			d.Resume = true
			d.Strategy = domain.StrategySingle
			d.InitialConns, d.MaxConns, d.ChunkSize = 1, 1, 0
			d.ChangeTriggers = nil
			d.Reasons = append(d.Reasons, "validators match: resuming single stream from offset with If-Range")
		default:
			d.Resume = true
			d.Reasons = append(d.Reasons, "validators match: resuming verified segments")
		}
	}
	return d
}

// selectFresh applies rules A, C and B and the manual override.
func selectFresh(d *Decision, obs Observations, cfg config.Config) {
	var single []string
	if !obs.SizeKnown {
		single = append(single, "size unknown")
	}
	if !obs.RangesProven {
		single = append(single, "byte ranges not proven")
	}
	if obs.SizeKnown && obs.Size < cfg.MinMultipartSize {
		single = append(single, fmt.Sprintf("size %d below multipart threshold %d", obs.Size, cfg.MinMultipartSize))
	}
	if obs.Validators.Kind() == domain.ValidatorNone {
		single = append(single, "no usable validator (strong ETag or Last-Modified)")
	}
	if cfg.MaxConnections <= 1 {
		single = append(single, "max connections is 1")
	}
	if obs.Dynamic {
		single = append(single, "resource looks dynamic")
	}
	if len(single) > 0 {
		d.Strategy = domain.StrategySingle
		d.InitialConns, d.MaxConns = 1, 1
		for _, r := range single {
			d.Reasons = append(d.Reasons, "single stream: "+r)
		}
		if !obs.RangesProven || !obs.SizeKnown {
			d.Reasons = append(d.Reasons, "an interrupted download restarts from zero")
		}
		return
	}

	maxConns := min(cfg.MaxConnections, hardMaxConns)
	override := !cfg.AdaptiveConcurrency && cfg.InitialConnections > 0 && cfg.InitialConnections == cfg.MaxConnections

	var slow []string
	for _, p := range obs.Probes {
		if p.RTT > conservativeRTT {
			slow = append(slow, fmt.Sprintf("probe RTT %s above %s", p.RTT, conservativeRTT))
			break
		}
	}
	if obs.ProbeRetries > 0 || obs.ProbeTimeouts > 0 {
		slow = append(slow, fmt.Sprintf("probes retried %d / timed out %d", obs.ProbeRetries, obs.ProbeTimeouts))
	}
	if cfg.ConservativeMode {
		slow = append(slow, "conservative mode configured")
	}

	conservative := len(slow) > 0
	if conservative {
		d.Strategy = domain.StrategyConservative
		d.InitialConns, d.MaxConns = 1, min(conservativeMaxConns, maxConns)
		for _, r := range slow {
			d.Reasons = append(d.Reasons, "conservative: "+r)
		}
		d.ChangeTriggers = []string{
			"return to adaptive multipart after 10 stable windows",
			"halve connections and back off on 429/503",
		}
	} else {
		d.Strategy = domain.StrategyMultipart
		d.InitialConns, d.MaxConns = min(defaultInitialConns, maxConns), maxConns
		if cfg.InitialConnections > 0 {
			d.InitialConns = min(cfg.InitialConnections, maxConns)
		}
		d.Reasons = append(d.Reasons, fmt.Sprintf("multipart: ranges proven, size %d, validator %s", obs.Size, d.Validator))
		d.ChangeTriggers = []string{
			"increase connections while throughput improves >= 10%",
			"drop to conservative after 3 consecutive windows with repeated timeouts/resets",
			"halve connections and back off on 429/503",
		}
	}
	if override {
		n := min(cfg.MaxConnections, hardMaxConns)
		d.InitialConns, d.MaxConns = n, n
		d.Reasons = append(d.Reasons, fmt.Sprintf("manual override: %d connections", n))
		d.ChangeTriggers = []string{"halve connections and back off on 429/503"}
	}
	d.ChunkSize = ChunkSize(obs.Size, d.MaxConns, conservative, cfg)
}

func confidence(obs Observations) Confidence {
	switch obs.Validators.Kind() {
	case domain.ValidatorStrongETag:
		if obs.RangesProven && len(obs.Probes) >= 2 {
			return ConfidenceHigh
		}
		return ConfidenceLow
	case domain.ValidatorLastModified:
		return ConfidenceMedium
	default:
		return ConfidenceLow
	}
}

// ChunkSize returns the initial segment size (spec §6.1):
// clamp(size/(maxConns*4), ChunkSizeMin, ChunkSizeMax) rounded down to a MiB
// multiple, with a 4 MiB minimum when conservative, grown so the segment
// count never exceeds 4096.
func ChunkSize(size int64, maxConns int, conservative bool, cfg config.Config) int64 {
	if size <= 0 {
		return 0
	}
	maxConns = max(maxConns, 1)
	lo, hi := cfg.ChunkSizeMin, cfg.ChunkSizeMax
	if conservative {
		lo = max(lo, conservativeChunkMin)
	}
	hi = max(hi, lo)
	chunk := min(max(size/int64(maxConns*4), lo), hi)
	chunk = max(chunk/mibBytes*mibBytes, mibBytes)
	if (size+chunk-1)/chunk > maxSegments {
		need := (size + maxSegments - 1) / maxSegments
		chunk = (need + mibBytes - 1) / mibBytes * mibBytes
	}
	return chunk
}

// Summary returns a one-line human description of the decision.
func (d Decision) Summary() string {
	head := fmt.Sprintf("%s (%s confidence)", d.Strategy, d.Confidence)
	switch {
	case d.Strategy == domain.StrategyUnsupported:
		msg := "inspection failed"
		if d.Err != nil {
			msg = d.Err.Msg
		}
		return head + ": " + msg
	case d.RemoteChanged:
		head = "remote changed, restart as " + head
	case d.Resume:
		head = "resume " + head
	}
	if d.MaxConns <= 1 {
		return head + ": 1 connection"
	}
	return fmt.Sprintf("%s: %d→%d connections, %d MiB chunks", head, d.InitialConns, d.MaxConns, d.ChunkSize/mibBytes)
}
