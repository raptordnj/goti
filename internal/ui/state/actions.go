package state

import (
	"github.com/raptordnj/swiftfetch/internal/domain"
)

type actions struct{ pause, resume, cancel, retry, restart, open bool }

// actionTable lists the commands valid in each state, so buttons only show
// when their command would be accepted (spec §5, §7).
var actionTable = map[domain.State]actions{
	domain.StateQueued:      {pause: true, cancel: true},
	domain.StateInspecting:  {pause: true, cancel: true},
	domain.StateDownloading: {pause: true, cancel: true},
	domain.StateRetrying:    {pause: true, cancel: true},
	domain.StateVerifying:   {cancel: true},
	domain.StatePaused:      {resume: true, cancel: true},
	domain.StateCompleted:   {open: true},
	domain.StateFailed:      {retry: true, cancel: true},
	domain.StateCancelled:   {},
}

// interruptedActions apply to a task left mid-run with no live run here.
var interruptedActions = actions{resume: true, cancel: true}

func actionsFor(st domain.State, interrupted bool, kind domain.ErrorKind) actions {
	if interrupted {
		return interruptedActions
	}
	a := actionTable[st]
	if st == domain.StateFailed && kind == domain.KindRemoteChanged {
		a.restart = true
	}
	return a
}

// errorAction suggests what the user can do about a failure kind.
func errorAction(k domain.ErrorKind) string {
	switch k {
	case domain.KindOffline:
		return "Check your network connection, then retry."
	case domain.KindAuth:
		return "The server requires authorization; check the link."
	case domain.KindRemoteChanged:
		return "The file changed on the server; restart from zero."
	case domain.KindIntegrity:
		return "Integrity check failed; restart the download."
	case domain.KindLocal:
		return "Check disk space and folder permissions."
	case domain.KindPolicy:
		return "The address is blocked by network policy."
	case domain.KindPermanent:
		return "The server refused the request; check the link."
	default:
		return "Retry the download."
	}
}
