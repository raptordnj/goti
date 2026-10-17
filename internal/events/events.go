// Package events defines engine events and a bounded, coalescing event bus.
package events

import (
	"time"

	"github.com/raptordnj/swiftfetch/internal/domain"
)

// Event is anything published on the Bus.
type Event interface{ TaskID() string }

// StateChanged reports a task state transition.
type StateChanged struct {
	ID       string
	From, To domain.State
	Reason   string
}

// Progress reports transfer progress; it is coalesced latest-wins per task.
type Progress struct {
	ID          string
	Downloaded  int64
	Total       int64
	TotalKnown  bool
	SpeedBps    float64
	ActiveConns int
	ETA         *time.Duration
}

// DecisionMade carries the strategy decision for a task.
type DecisionMade struct {
	ID           string
	Summary      string
	DecisionJSON []byte
}

// RetryScheduled reports an upcoming retry.
type RetryScheduled struct {
	ID      string
	Attempt int
	Kind    domain.ErrorKind
	Delay   time.Duration
	Reason  string
}

// StrategyChanged reports a mid-download strategy switch.
type StrategyChanged struct {
	ID       string
	From, To domain.Strategy
	Reason   string
}

// Warning is a non-fatal notice.
type Warning struct{ ID, Msg string }

// Resync tells a consumer it missed events and must re-read a snapshot.
type Resync struct{}

// TaskID implements Event.
func (e StateChanged) TaskID() string { return e.ID }

// TaskID implements Event.
func (e Progress) TaskID() string { return e.ID }

// TaskID implements Event.
func (e DecisionMade) TaskID() string { return e.ID }

// TaskID implements Event.
func (e RetryScheduled) TaskID() string { return e.ID }

// TaskID implements Event.
func (e StrategyChanged) TaskID() string { return e.ID }

// TaskID implements Event.
func (e Warning) TaskID() string { return e.ID }

// TaskID implements Event; Resync is not tied to a task.
func (Resync) TaskID() string { return "" }
