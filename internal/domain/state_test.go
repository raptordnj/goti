package domain

import (
	"errors"
	"slices"
	"testing"
)

func TestTransitionTableExhaustive(t *testing.T) {
	allowed := map[State][]State{
		StateQueued:      {StateInspecting, StatePaused, StateCancelled},
		StateInspecting:  {StateDownloading, StateRetrying, StateFailed, StatePaused, StateCancelled},
		StateDownloading: {StateVerifying, StateRetrying, StatePaused, StateFailed, StateCancelled},
		StateRetrying:    {StateInspecting, StatePaused, StateFailed, StateCancelled},
		StatePaused:      {StateQueued, StateCancelled},
		StateVerifying:   {StateCompleted, StateDownloading, StateFailed, StateCancelled},
		StateFailed:      {StateQueued, StateCancelled},
	}
	if len(AllStates) != 9 {
		t.Fatalf("AllStates has %d entries, want 9", len(AllStates))
	}
	for _, from := range AllStates {
		for _, to := range AllStates {
			err := Transition(from, to)
			want := slices.Contains(allowed[from], to)
			if want && err != nil {
				t.Errorf("%s->%s: unexpected %v", from, to, err)
			}
			if !want && !errors.Is(err, ErrInvalidTransition) {
				t.Errorf("%s->%s: want ErrInvalidTransition, got %v", from, to, err)
			}
		}
	}
}

func TestTerminal(t *testing.T) {
	for _, s := range AllStates {
		want := s == StateCompleted || s == StateCancelled
		if got := s.Terminal(); got != want {
			t.Errorf("%s.Terminal() = %v, want %v", s, got, want)
		}
	}
}

func TestParseState(t *testing.T) {
	for _, s := range AllStates {
		got, err := ParseState(string(s))
		if err != nil || got != s {
			t.Errorf("ParseState(%q) = %q, %v", s, got, err)
		}
	}
	if _, err := ParseState("bogus"); err == nil {
		t.Error("ParseState(\"bogus\") succeeded, want error")
	}
}
