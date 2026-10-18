// Package downloader orchestrates downloads; this file defines persistence.
package downloader

import (
	"context"

	"github.com/raptordnj/goti/internal/domain"
)

// Store persists tasks, segment checkpoints and retry attempts.
type Store interface {
	CreateTask(ctx context.Context, t *domain.Task) error
	GetTask(ctx context.Context, id string) (*domain.Task, error) // domain.ErrNotFound
	UpdateTask(ctx context.Context, t *domain.Task) error
	ListTasks(ctx context.Context) ([]*domain.Task, error) // newest first
	DeleteTask(ctx context.Context, id string) error       // cascades segments, attempts
	// SaveCheckpoint replaces the task's segments and sets downloaded in one transaction.
	SaveCheckpoint(ctx context.Context, taskID string, cp domain.Checkpoint) error
	LoadSegments(ctx context.Context, taskID string) ([]domain.SegmentRecord, error) // ordered by start
	ClearSegments(ctx context.Context, taskID string) error
	AddAttempt(ctx context.Context, taskID string, a domain.Attempt) error // prunes to 200 newest
	ListAttempts(ctx context.Context, taskID string) ([]domain.Attempt, error)
}
