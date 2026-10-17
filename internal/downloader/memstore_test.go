package downloader

import (
	"context"
	"sort"
	"sync"

	"github.com/raptordnj/swiftfetch/internal/domain"
)

var _ Store = (*memStore)(nil)

// memStore is an in-memory Store for package tests. onSave, when set, is
// called at the start of every SaveCheckpoint.
type memStore struct {
	mu          sync.Mutex
	tasks       map[string]*domain.Task
	segments    map[string][]domain.SegmentRecord
	attempts    map[string][]domain.Attempt
	checkpoints []domain.Checkpoint // every SaveCheckpoint, in order
	onSave      func()
}

func newMemStore() *memStore {
	return &memStore{
		tasks:    map[string]*domain.Task{},
		segments: map[string][]domain.SegmentRecord{},
		attempts: map[string][]domain.Attempt{},
	}
}

func cloneRecords(recs []domain.SegmentRecord) []domain.SegmentRecord {
	out := make([]domain.SegmentRecord, len(recs))
	for i, r := range recs {
		r.Hash = append([]byte(nil), r.Hash...)
		r.HasherState = append([]byte(nil), r.HasherState...)
		out[i] = r
	}
	return out
}

func (m *memStore) CreateTask(_ context.Context, t *domain.Task) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := *t
	m.tasks[t.ID] = &c
	return nil
}

func (m *memStore) GetTask(_ context.Context, id string) (*domain.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	c := *t
	return &c, nil
}

func (m *memStore) UpdateTask(_ context.Context, t *domain.Task) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.tasks[t.ID]; !ok {
		return domain.ErrNotFound
	}
	c := *t
	m.tasks[t.ID] = &c
	return nil
}

func (m *memStore) ListTasks(_ context.Context) ([]*domain.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*domain.Task, 0, len(m.tasks))
	for _, t := range m.tasks {
		c := *t
		out = append(out, &c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (m *memStore) DeleteTask(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.tasks[id]; !ok {
		return domain.ErrNotFound
	}
	delete(m.tasks, id)
	delete(m.segments, id)
	delete(m.attempts, id)
	return nil
}

func (m *memStore) SaveCheckpoint(_ context.Context, taskID string, cp domain.Checkpoint) error {
	if m.onSave != nil {
		m.onSave()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cp.Segments = cloneRecords(cp.Segments)
	m.segments[taskID] = cp.Segments
	m.checkpoints = append(m.checkpoints, cp)
	if t, ok := m.tasks[taskID]; ok {
		t.Downloaded = cp.Downloaded
	}
	return nil
}

func (m *memStore) LoadSegments(_ context.Context, taskID string) ([]domain.SegmentRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneRecords(m.segments[taskID]), nil
}

func (m *memStore) ClearSegments(_ context.Context, taskID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.segments, taskID)
	return nil
}

func (m *memStore) AddAttempt(_ context.Context, taskID string, a domain.Attempt) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := append(m.attempts[taskID], a)
	if len(l) > 200 {
		l = l[len(l)-200:]
	}
	m.attempts[taskID] = l
	return nil
}

func (m *memStore) ListAttempts(_ context.Context, taskID string) ([]domain.Attempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]domain.Attempt(nil), m.attempts[taskID]...), nil
}

// lastCheckpoint returns the most recent checkpoint and whether any exists.
func (m *memStore) lastCheckpoint() (domain.Checkpoint, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.checkpoints) == 0 {
		return domain.Checkpoint{}, false
	}
	return m.checkpoints[len(m.checkpoints)-1], true
}

func (m *memStore) saves() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.checkpoints)
}
