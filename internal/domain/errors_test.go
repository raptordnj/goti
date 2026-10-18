package domain

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestKindOf(t *testing.T) {
	wrapped := fmt.Errorf("x: %w", NewError(KindAuth, "denied", "log in", nil))
	cases := []struct {
		name string
		err  error
		want ErrorKind
	}{
		{"wrapped domain error", wrapped, KindAuth},
		{"context canceled", context.Canceled, KindCancelled},
		{"unknown", errors.New("x"), KindTransient},
	}
	for _, c := range cases {
		if got := KindOf(c.err); got != c.want {
			t.Errorf("%s: KindOf = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestErrorUnwrap(t *testing.T) {
	cause := errors.New("boom")
	e := NewError(KindLocal, "disk", "", cause)
	if !errors.Is(e, cause) {
		t.Fatal("errors.Is should reach cause")
	}
	if e.Error() == "" {
		t.Fatal("empty message")
	}
}

func TestValidatorsMatches(t *testing.T) {
	cases := []struct {
		name string
		a, b Validators
		want bool
	}{
		{"strong same", Validators{ETag: `"a"`, ETagStrong: true}, Validators{ETag: `"a"`, ETagStrong: true}, true},
		{"strong different", Validators{ETag: `"a"`, ETagStrong: true}, Validators{ETag: `"b"`, ETagStrong: true}, false},
		{"weak with equal LM", Validators{ETag: `W/"a"`, LastModified: "lm"}, Validators{ETag: `W/"b"`, LastModified: "lm"}, true},
		{"both empty", Validators{}, Validators{}, false},
	}
	for _, c := range cases {
		if got := c.a.Matches(c.b); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}
