package strategy

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/raptordnj/swiftfetch/internal/config"
	"github.com/raptordnj/swiftfetch/internal/domain"
)

const mib = int64(1 << 20)

var (
	strongV = domain.Validators{ETag: `"abc"`, ETagStrong: true, LastModified: "Mon, 01 Jan 2024 00:00:00 GMT"}
	lmV     = domain.Validators{LastModified: "Mon, 01 Jan 2024 00:00:00 GMT"}
)

func provenObs(size int64, v domain.Validators) Observations {
	return Observations{
		Status: 206, Size: size, SizeKnown: true, Validators: v, RangesProven: true,
		Probes: []ProbeResult{
			{Status: 206, BodyLen: 1, Validators: v, RTT: 20 * time.Millisecond},
			{Status: 206, BodyLen: 2, Validators: v, RTT: 25 * time.Millisecond},
		},
	}
}

func TestSelect(t *testing.T) {
	def := config.Default()
	withCfg := func(f func(*config.Config)) config.Config { c := def; f(&c); return c }
	override6 := withCfg(func(c *config.Config) { c.InitialConnections, c.MaxConnections, c.AdaptiveConcurrency = 6, 6, false })

	slowRTT := provenObs(100*mib, strongV)
	slowRTT.Probes[1].RTT = 500 * time.Millisecond
	retried := provenObs(100*mib, strongV)
	retried.ProbeRetries = 1
	unknown := Observations{Status: 200}
	notProven := provenObs(100*mib, strongV)
	notProven.RangesProven = false
	dynamic := provenObs(100*mib, strongV)
	dynamic.Dynamic = true

	tests := []struct {
		name       string
		obs        Observations
		cfg        config.Config
		resume     *ResumeState
		strat      domain.Strategy
		init, max  int
		conf       Confidence
		resumeOK   bool
		changed    bool
		errKind    domain.ErrorKind
		hasErr     bool
		minChunk   int64
		reasonPart string
	}{
		{name: "auth error", obs: Observations{Err: domain.NewError(domain.KindAuth, "forbidden", "check credentials", nil)},
			cfg: def, strat: domain.StrategyUnsupported, conf: ConfidenceLow, hasErr: true, errKind: domain.KindAuth},
		{name: "unknown size", obs: unknown, cfg: def, strat: domain.StrategySingle, init: 1, max: 1, conf: ConfidenceLow, reasonPart: "size unknown"},
		{name: "ranges not proven", obs: notProven, cfg: def, strat: domain.StrategySingle, init: 1, max: 1, conf: ConfidenceLow, reasonPart: "not proven"},
		{name: "below threshold", obs: provenObs(7*mib, strongV), cfg: def, strat: domain.StrategySingle, init: 1, max: 1, conf: ConfidenceHigh, reasonPart: "below multipart threshold"},
		{name: "no validator", obs: provenObs(100*mib, domain.Validators{}), cfg: def, strat: domain.StrategySingle, init: 1, max: 1, conf: ConfidenceLow, reasonPart: "no usable validator"},
		{name: "dynamic", obs: dynamic, cfg: def, strat: domain.StrategySingle, init: 1, max: 1, conf: ConfidenceHigh, reasonPart: "dynamic"},
		{name: "strong etag multipart", obs: provenObs(100*mib, strongV), cfg: def, strat: domain.StrategyMultipart, init: 2, max: 8, conf: ConfidenceHigh},
		{name: "last-modified multipart", obs: provenObs(100*mib, lmV), cfg: def, strat: domain.StrategyMultipart, init: 2, max: 8, conf: ConfidenceMedium},
		{name: "slow rtt", obs: slowRTT, cfg: def, strat: domain.StrategyConservative, init: 1, max: 2, conf: ConfidenceHigh, minChunk: 4 * mib, reasonPart: "RTT"},
		{name: "probe retried", obs: retried, cfg: def, strat: domain.StrategyConservative, init: 1, max: 2, conf: ConfidenceHigh, minChunk: 4 * mib},
		{name: "conservative mode", obs: provenObs(100*mib, strongV), cfg: withCfg(func(c *config.Config) { c.ConservativeMode = true }),
			strat: domain.StrategyConservative, init: 1, max: 2, conf: ConfidenceHigh, minChunk: 4 * mib},
		{name: "max conns 1", obs: provenObs(100*mib, strongV), cfg: withCfg(func(c *config.Config) { c.MaxConnections = 1 }),
			strat: domain.StrategySingle, init: 1, max: 1, conf: ConfidenceHigh},
		{name: "override proven", obs: provenObs(100*mib, strongV), cfg: override6, strat: domain.StrategyMultipart, init: 6, max: 6, conf: ConfidenceHigh},
		{name: "override not proven", obs: notProven, cfg: override6, strat: domain.StrategySingle, init: 1, max: 1, conf: ConfidenceLow},
		{name: "resume match", obs: provenObs(100*mib, strongV), cfg: def,
			resume: &ResumeState{Validators: strongV, Size: 100 * mib, SizeKnown: true, Previous: domain.StrategyMultipart},
			strat:  domain.StrategyMultipart, init: 2, max: 8, conf: ConfidenceHigh, resumeOK: true},
		{name: "resume single from offset", obs: provenObs(100*mib, strongV), cfg: def,
			resume: &ResumeState{Validators: strongV, Size: 100 * mib, SizeKnown: true, Previous: domain.StrategySingle},
			strat:  domain.StrategySingle, init: 1, max: 1, conf: ConfidenceHigh, resumeOK: true},
		{name: "resume etag mismatch", obs: provenObs(100*mib, strongV), cfg: def,
			resume: &ResumeState{Validators: domain.Validators{ETag: `"old"`, ETagStrong: true}, Size: 100 * mib, SizeKnown: true, Previous: domain.StrategyMultipart},
			strat:  domain.StrategyMultipart, init: 2, max: 8, conf: ConfidenceHigh, changed: true, hasErr: true, errKind: domain.KindRemoteChanged},
		{name: "resume lm size mismatch", obs: provenObs(100*mib, lmV), cfg: def,
			resume: &ResumeState{Validators: lmV, Size: 99 * mib, SizeKnown: true, Previous: domain.StrategyMultipart},
			strat:  domain.StrategyMultipart, init: 2, max: 8, conf: ConfidenceMedium, changed: true, hasErr: true, errKind: domain.KindRemoteChanged},
		{name: "resume no stored validator", obs: provenObs(100*mib, strongV), cfg: def,
			resume: &ResumeState{Size: 100 * mib, SizeKnown: true, Previous: domain.StrategyMultipart},
			strat:  domain.StrategyMultipart, init: 2, max: 8, conf: ConfidenceHigh, changed: true, hasErr: true, errKind: domain.KindRemoteChanged, reasonPart: "cannot verify"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := Select(tt.obs, tt.cfg, tt.resume)
			if d.Strategy != tt.strat {
				t.Fatalf("Strategy = %q, want %q (reasons %v)", d.Strategy, tt.strat, d.Reasons)
			}
			if d.InitialConns != tt.init || d.MaxConns != tt.max {
				t.Errorf("conns = %d/%d, want %d/%d", d.InitialConns, d.MaxConns, tt.init, tt.max)
			}
			if d.Confidence != tt.conf {
				t.Errorf("Confidence = %q, want %q", d.Confidence, tt.conf)
			}
			if len(d.Reasons) == 0 {
				t.Error("Reasons empty")
			}
			if tt.reasonPart != "" && !strings.Contains(strings.Join(d.Reasons, "; "), tt.reasonPart) {
				t.Errorf("Reasons %v missing %q", d.Reasons, tt.reasonPart)
			}
			if d.Resume != tt.resumeOK || d.RemoteChanged != tt.changed {
				t.Errorf("Resume/RemoteChanged = %v/%v, want %v/%v", d.Resume, d.RemoteChanged, tt.resumeOK, tt.changed)
			}
			if tt.hasErr != (d.Err != nil) || (d.Err != nil && d.Err.Kind != tt.errKind) {
				t.Errorf("Err = %v, want kind %v (present %v)", d.Err, tt.errKind, tt.hasErr)
			}
			if tt.name == "auth error" && d.Err != tt.obs.Err {
				t.Error("Err not preserved")
			}
			if tt.minChunk > 0 && d.ChunkSize < tt.minChunk {
				t.Errorf("ChunkSize = %d, want >= %d", d.ChunkSize, tt.minChunk)
			}
			if d.Summary() == "" {
				t.Error("empty Summary")
			}
		})
	}
}

func TestSelectDeterministic(t *testing.T) {
	obs := provenObs(100*mib, strongV)
	rs := &ResumeState{Validators: strongV, Size: 100 * mib, SizeKnown: true, Previous: domain.StrategyMultipart}
	a := Select(obs, config.Default(), rs)
	b := Select(obs, config.Default(), rs)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("non-deterministic:\n%+v\n%+v", a, b)
	}
}

func TestDecisionSummary(t *testing.T) {
	d := Select(provenObs(128*mib, strongV), config.Default(), nil)
	if got, want := d.Summary(), "multipart (high confidence): 2→8 connections, 4 MiB chunks"; got != want {
		t.Fatalf("Summary = %q, want %q", got, want)
	}
}

func TestChunkSize(t *testing.T) {
	cfg := config.Default()
	if got := ChunkSize(100*mib, 8, false, cfg); got != 3*mib {
		t.Errorf("100 MiB/8 = %d, want 3 MiB", got)
	}
	if got := ChunkSize(10<<30, 8, false, cfg); got != 64*mib {
		t.Errorf("10 GiB/8 = %d, want 64 MiB", got)
	}
	tib := int64(1) << 40
	got := ChunkSize(tib, 8, false, cfg)
	if got < 256*mib || (tib+got-1)/got > 4096 || got%mib != 0 {
		t.Errorf("1 TiB chunk = %d: want >= 256 MiB, <= 4096 chunks, MiB multiple", got)
	}
	if got := ChunkSize(20*mib, 2, true, cfg); got != 4*mib {
		t.Errorf("conservative 20 MiB = %d, want 4 MiB", got)
	}
}
