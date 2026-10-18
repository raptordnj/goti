package screens

import (
	"image"
	"slices"
	"testing"
	"time"

	"gioui.org/io/event"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"github.com/raptordnj/swiftfetch/internal/domain"
	"github.com/raptordnj/swiftfetch/internal/ui/state"
	"github.com/raptordnj/swiftfetch/internal/ui/theme"
)

var now = time.Unix(1000, 0)

type harness struct {
	r   input.Router
	gtx layout.Context
}

func newHarness(w, h int) *harness {
	hs := &harness{}
	hs.gtx = layout.Context{
		Ops:         new(op.Ops),
		Constraints: layout.Exact(image.Pt(w, h)),
		Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Now:         now,
		Source:      hs.r.Source(),
	}
	return hs
}

// frame resets ops, runs f, and submits the frame to the router.
func (hs *harness) frame(f func(gtx layout.Context)) {
	hs.gtx.Ops.Reset()
	f(hs.gtx)
	hs.r.Frame(hs.gtx.Ops)
}

// click focuses tag and queues Enter press+release (keyboard activation).
func (hs *harness) click(tag event.Tag, f func(gtx layout.Context)) {
	hs.gtx.Execute(key.FocusCmd{Tag: tag})
	hs.frame(f)
	hs.r.Queue(key.Event{Name: key.NameReturn, State: key.Press}, key.Event{Name: key.NameReturn, State: key.Release})
}

func recordTexts(t *testing.T) {
	t.Helper()
	RecordTexts(true)
	t.Cleanup(func() { RecordTexts(false) })
}

func hasText(t *testing.T, want string) bool {
	t.Helper()
	return slices.Contains(RecordedTexts(), want)
}

var th = theme.New(theme.Light())

func task(id string, st domain.State, sec state.Section) state.TaskView {
	return state.TaskView{ID: id, FileName: id + ".iso", State: st, Section: sec, Percent: -1, URL: "https://ex.com/" + id, DestPath: "/tmp/" + id + ".iso"}
}

func snapOf(tasks ...state.TaskView) *state.Snapshot {
	s := &state.Snapshot{Tasks: tasks}
	for _, v := range tasks {
		s.Counts[v.Section]++
	}
	return s
}
