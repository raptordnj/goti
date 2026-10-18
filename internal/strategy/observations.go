// Package strategy inspects remote resources and selects a download strategy.
package strategy

import (
	"time"

	"github.com/raptordnj/goti/internal/domain"
)

// ProbeResult records one range probe (spec §4.2).
type ProbeResult struct {
	Requested    domain.ByteRange
	Status       int
	ContentRange string
	BodyLen      int64
	Validators   domain.Validators
	RTT          time.Duration
	Err          string // redacted
}

// Observations is everything learned about a resource by Inspect (spec §4.2).
// All URLs are redacted.
type Observations struct {
	OriginalURL, FinalURL string
	Redirects             []string
	Proto                 string
	Status                int
	Size                  int64
	SizeKnown             bool
	ContentType           string
	Validators            domain.Validators
	Probes                []ProbeResult
	RangesProven          bool
	Anomalies             []string
	SignedURL             bool
	Dynamic               bool
	ProbeRetries          int
	ProbeTimeouts         int
	FileName              string
	FileNameSource        string
	Err                   *domain.Error
	// RangeProbeInterrupted: Err is a transport failure on a range probe
	// (after probe 1's headers), not evidence against range support.
	RangeProbeInterrupted bool
}
