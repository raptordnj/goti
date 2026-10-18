package domain

import (
	"context"
	"errors"
	"time"
)

// ErrorKind classifies a failure for retry and reporting decisions.
type ErrorKind int

// Error kinds.
const (
	KindTransient ErrorKind = iota
	KindOffline
	KindPermanent
	KindAuth
	KindIntegrity
	KindCancelled
	KindRemoteChanged
	KindPolicy
	KindLocal
)

var kindNames = [...]string{
	KindTransient:     "transient",
	KindOffline:       "offline",
	KindPermanent:     "permanent",
	KindAuth:          "auth",
	KindIntegrity:     "integrity",
	KindCancelled:     "cancelled",
	KindRemoteChanged: "remote-changed",
	KindPolicy:        "policy",
	KindLocal:         "local",
}

// String returns the lowercase name of the kind.
func (k ErrorKind) String() string {
	if k < 0 || int(k) >= len(kindNames) {
		return "unknown"
	}
	return kindNames[k]
}

// Sentinel causes used to signal why work stopped.
var (
	ErrPaused    = errors.New("paused")
	ErrCancelled = errors.New("cancelled")
)

// Error is a classified download error. Msg must already be redacted.
type Error struct {
	Kind       ErrorKind
	Msg        string
	Action     string // recommended user action, may be empty
	RetryAfter time.Duration
	Err        error
}

// NewError builds an Error with the given kind, message, action and cause.
func NewError(kind ErrorKind, msg, action string, cause error) *Error {
	return &Error{Kind: kind, Msg: msg, Action: action, Err: cause}
}

// Error implements the error interface.
func (e *Error) Error() string {
	return e.Msg
}

// Unwrap returns the underlying cause.
func (e *Error) Unwrap() error { return e.Err }

// KindOf returns the kind of err, walking wrapped errors. Context
// cancellation maps to KindCancelled; unknown errors are KindTransient.
func KindOf(err error) ErrorKind {
	var de *Error
	if errors.As(err, &de) {
		return de.Kind
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, ErrCancelled) {
		return KindCancelled
	}
	return KindTransient
}

// ErrNotFound is returned by stores when a record does not exist.
var ErrNotFound = errors.New("not found")
