// Package faultinjection tests the download engine's recovery from store
// failures and from the process being killed mid-download.
package faultinjection

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/raptordnj/swiftfetch/internal/domain"
	"github.com/raptordnj/swiftfetch/internal/downloader"
)

// ErrInjected is the error a FaultyStore returns for an injected fault.
var ErrInjected = errors.New("faultinjection: injected store failure")

// FaultyStore wraps a downloader.Store and fails SaveCheckpoint and
// UpdateTask from their Nth call on (a persistent fault, like a full disk):
// with FailSaveCheckpointAfter = N the first N-1 calls reach the wrapped
// store and call N and every later call returns ErrInjected without
// touching it. Zero disables a fault. Counters are safe for concurrent use.
type FaultyStore struct {
	downloader.Store
	FailSaveCheckpointAfter int
	FailUpdateTaskAfter     int

	saveCalls   atomic.Int64
	updateCalls atomic.Int64
}

// SaveCheckpoint fails from call FailSaveCheckpointAfter on.
func (s *FaultyStore) SaveCheckpoint(ctx context.Context, taskID string, cp domain.Checkpoint) error {
	if n := s.saveCalls.Add(1); s.FailSaveCheckpointAfter > 0 && n >= int64(s.FailSaveCheckpointAfter) {
		return ErrInjected
	}
	return s.Store.SaveCheckpoint(ctx, taskID, cp)
}

// UpdateTask fails from call FailUpdateTaskAfter on.
func (s *FaultyStore) UpdateTask(ctx context.Context, t *domain.Task) error {
	if n := s.updateCalls.Add(1); s.FailUpdateTaskAfter > 0 && n >= int64(s.FailUpdateTaskAfter) {
		return ErrInjected
	}
	return s.Store.UpdateTask(ctx, t)
}
