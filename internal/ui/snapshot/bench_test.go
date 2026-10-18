package snapshot

import (
	"image"
	"testing"

	"gioui.org/io/input"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"github.com/raptordnj/goti/internal/ui/screens"
	"github.com/raptordnj/goti/internal/ui/theme"
)

// BenchmarkMainBusyLayout measures one main-screen frame (layout and op
// recording, no GPU) of the Busy fixture at 1100x720; target < 2 ms.
func BenchmarkMainBusyLayout(b *testing.B) {
	th := theme.New(theme.Light())
	snap := Busy()
	var m screens.Main
	var r input.Router
	ops := new(op.Ops)
	gtx := layout.Context{Ops: ops, Constraints: layout.Exact(image.Pt(1100, 720)),
		Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Now: Now, Source: r.Source()}
	b.ReportAllocs()
	for b.Loop() {
		ops.Reset()
		m.Layout(gtx, th, snap, Now)
		r.Frame(ops)
	}
}
