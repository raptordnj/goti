// Package domain holds the core types of the download engine.
package domain

import (
	"errors"
	"fmt"
)

// State is the lifecycle state of a download.
type State string

// Download lifecycle states.
const (
	StateQueued      State = "queued"
	StateInspecting  State = "inspecting"
	StateDownloading State = "downloading"
	StatePaused      State = "paused"
	StateRetrying    State = "retrying"
	StateVerifying   State = "verifying"
	StateCompleted   State = "completed"
	StateFailed      State = "failed"
	StateCancelled   State = "cancelled"
)

// AllStates lists every valid state.
var AllStates = []State{
	StateQueued, StateInspecting, StateDownloading, StatePaused, StateRetrying,
	StateVerifying, StateCompleted, StateFailed, StateCancelled,
}

// ErrInvalidTransition is returned when a state change is not allowed.
var ErrInvalidTransition = errors.New("invalid state transition")

var transitions = map[State]map[State]bool{
	StateQueued:      {StateInspecting: true, StatePaused: true, StateCancelled: true},
	StateInspecting:  {StateDownloading: true, StateRetrying: true, StateFailed: true, StatePaused: true, StateCancelled: true},
	StateDownloading: {StateVerifying: true, StateRetrying: true, StatePaused: true, StateFailed: true, StateCancelled: true},
	StateRetrying:    {StateInspecting: true, StatePaused: true, StateFailed: true, StateCancelled: true},
	StatePaused:      {StateQueued: true, StateCancelled: true},
	StateVerifying:   {StateCompleted: true, StateDownloading: true, StateFailed: true, StateCancelled: true},
	StateFailed:      {StateQueued: true, StateCancelled: true},
}

// Transition reports whether moving from one state to another is allowed.
func Transition(from, to State) error {
	if transitions[from][to] {
		return nil
	}
	return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, from, to)
}

// Terminal reports whether the state is final.
func (s State) Terminal() bool {
	return s == StateCompleted || s == StateCancelled
}

// ParseState converts a string to a State, rejecting unknown values.
func ParseState(v string) (State, error) {
	for _, s := range AllStates {
		if string(s) == v {
			return s, nil
		}
	}
	return "", fmt.Errorf("unknown state %q", v)
}
