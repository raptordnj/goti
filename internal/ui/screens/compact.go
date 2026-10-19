package screens

import (
	"gioui.org/layout"
	"gioui.org/unit"
)

// CompactWidth is the width below which the phone (compact) layout is used.
const CompactWidth unit.Dp = 600

// Compact reports whether gtx is narrower than CompactWidth: header without
// Add, a floating Add button, touch-height rows, full-window details and
// bottom-sheet dialogs.
func Compact(gtx layout.Context) bool { return gtx.Constraints.Max.X < gtx.Dp(CompactWidth) }

// Compact-layout geometry.
const (
	fabSize      unit.Dp = 56
	longPressFor         = 500 // ms
	touchButton  unit.Dp = 40
)
