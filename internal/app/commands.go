// Package app is the command loop between the GUI/tray and the queue, engine and settings.
package app

import "github.com/raptordnj/goti/internal/settings"

// Command is a typed request sent to the App.
type Command interface{ isCommand() }

type (
	// AddDownload adds and enqueues a download; empty DestDir uses the saved download directory.
	AddDownload struct{ URL, DestDir, FileName string }
	// PauseTask pauses one task.
	PauseTask struct{ ID string }
	// ResumeTask resumes one task.
	ResumeTask struct{ ID string }
	// CancelTask cancels one task.
	CancelTask struct{ ID string }
	// RetryTask retries a failed task.
	RetryTask struct{ ID string }
	// RestartTask restarts a task from scratch.
	RestartTask struct{ ID string }
	// RemoveTask removes a task, optionally deleting its partial file.
	RemoveTask struct {
		ID            string
		DeletePartial bool
	}
	// PauseAll pauses every task.
	PauseAll struct{}
	// ResumeAll resumes every task.
	ResumeAll struct{}
	// SaveSettings validates, persists and live-applies settings.
	SaveSettings struct{ S settings.Settings }
	// MarkTrayNoticeShown sets TrayNoticeShown on the settings current when
	// it runs and saves them, so it cannot revert an earlier queued save.
	MarkTrayNoticeShown struct{}
	// Quit shuts the queue down and ends Run.
	Quit struct{}
)

func (AddDownload) isCommand()  {}
func (PauseTask) isCommand()    {}
func (ResumeTask) isCommand()   {}
func (CancelTask) isCommand()   {}
func (RetryTask) isCommand()    {}
func (RestartTask) isCommand()  {}
func (RemoveTask) isCommand()   {}
func (PauseAll) isCommand()     {}
func (ResumeAll) isCommand()    {}
func (SaveSettings) isCommand() {}
func (Quit) isCommand()         {}

func (MarkTrayNoticeShown) isCommand() {}
