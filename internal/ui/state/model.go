// Package state folds engine events into an immutable view model (Snapshot)
// that the GUI reads every frame. It must not import gioui.org.
package state

import (
	"sync/atomic"
	"time"

	"github.com/raptordnj/goti/internal/domain"
	"github.com/raptordnj/goti/internal/settings"
)

// Section groups tasks in the list.
type Section int

// Sections in display order.
const (
	SectionActive    Section = iota
	SectionQueued            // queued
	SectionPaused            // paused, incl. interrupted
	SectionCompleted         // completed
	SectionFailed            // failed, incl. cancelled
	numSections
)

// NumSections is the number of sections.
const NumSections = int(numSections)

// AttemptView is one retry history entry.
type AttemptView struct {
	At      time.Time
	Attempt int
	Kind    string
	Message string
	Delay   time.Duration
}

// TaskView is the display model of one task.
type TaskView struct {
	ID, FileName, DestPath, URL                                   string // URL is redacted
	State                                                         domain.State
	Interrupted                                                   bool
	Section                                                       Section
	Downloaded, Total                                             int64
	TotalKnown                                                    bool
	Percent                                                       float64 // -1 when unknown
	SpeedBps                                                      float64
	ETA                                                           *time.Duration
	Conns                                                         int
	Strategy, DecisionSummary                                     string
	LastError, LastErrorAction                                    string
	VerifyResult                                                  string
	Retries                                                       []AttemptView
	RangesSupported                                               bool // decision "ranges_proven"
	Resumable                                                     bool // ranges + a usable validator
	ChunksDone, ChunksTotal                                       int  // persisted segments
	CanPause, CanResume, CanCancel, CanRetry, CanRestart, CanOpen bool
	CreatedAt                                                     time.Time

	waitingNet bool      // retrying because offline
	retryAt    time.Time // when the scheduled retry fires (zero = unknown)
}

// ToastKind is the visual variant of a toast.
type ToastKind int

// Toast kinds.
const (
	ToastInfo ToastKind = iota
	ToastSuccess
	ToastError
)

// Toast is a transient notification.
type Toast struct {
	Kind ToastKind
	Text string
	At   time.Time
}

// Snapshot is an immutable view of the whole UI state.
type Snapshot struct {
	Tasks         []TaskView // ordered: Section, then CreatedAt (Completed: newest first)
	Counts        [numSections]int
	TotalSpeedBps float64
	ActiveCount   int
	Settings      settings.Settings
	QueuePaused   bool
	Toasts        []Toast // newest last, max 3, each expires after 5 s
	Offline       bool    // any active task waiting for network
	Err           string  // fatal load error
	// AddResultSeq counts AddDownload results; AddErr is the latest one's
	// redacted error ("" on success). The window uses them for the Add dialog.
	AddResultSeq uint64
	AddErr       string
}

// Store holds the latest Snapshot; safe for concurrent use.
type Store struct{ cur atomic.Pointer[Snapshot] }

var empty = &Snapshot{}

// Load returns the current snapshot, never nil.
func (s *Store) Load() *Snapshot {
	if p := s.cur.Load(); p != nil {
		return p
	}
	return empty
}

func (s *Store) publish(p *Snapshot) { s.cur.Store(p) }
