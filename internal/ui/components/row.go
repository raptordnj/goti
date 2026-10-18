package components

import (
	"fmt"
	"image/color"
	"path/filepath"
	"strings"

	"time"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/widget"

	"github.com/raptordnj/swiftfetch/internal/platform"
	"github.com/raptordnj/swiftfetch/internal/ui/state"
	"github.com/raptordnj/swiftfetch/internal/ui/theme"
)

// RowWidgets is the per-row widget state. Shown records the action buttons
// laid out in the most recent frame (names: pause, resume, cancel, retry,
// restart, open, menu).
type RowWidgets struct {
	Pause, Resume, Cancel, Retry, Restart, Open, Menu, Select widget.Clickable
	Shown                                                     []string
}

// fileGlyph returns a short type badge for name.
func fileGlyph(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".zip", ".tar", ".gz", ".xz", ".7z", ".rar", ".zst":
		return "ZIP"
	case ".iso", ".img":
		return "ISO"
	case ".mp4", ".mkv", ".webm", ".avi", ".mov":
		return "VID"
	case ".mp3", ".flac", ".ogg", ".wav":
		return "AUD"
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg":
		return "IMG"
	case ".pdf", ".txt", ".doc", ".docx", ".md":
		return "DOC"
	}
	return "FILE"
}

// rowMeta builds "12 MB / 40 MB · 30% · 1.2 MB/s · 10s".
func rowMeta(v state.TaskView) string {
	parts := []string{platform.FormatBytes(v.Downloaded)}
	if v.TotalKnown {
		parts[0] += " / " + platform.FormatBytes(v.Total)
	}
	if v.Percent >= 0 {
		parts = append(parts, fmt.Sprintf("%.0f%%", v.Percent))
	}
	if v.SpeedBps > 0 {
		parts = append(parts, platform.FormatSpeed(v.SpeedBps))
	}
	if v.ETA != nil {
		parts = append(parts, platform.FormatETA(*v.ETA))
	}
	return strings.Join(parts, " · ")
}

func statusColor(th *theme.Theme, v state.TaskView) color.NRGBA {
	switch {
	case v.LastError != "" && !v.CanPause && v.CanRetry:
		return th.Error
	case v.CanOpen:
		return th.Success
	}
	return th.TextMuted
}

// TaskRow renders one task: glyph, name, status, progress, meta and the
// action buttons allowed by the CanX flags.
func TaskRow(th *theme.Theme, v state.TaskView, w *RowWidgets, now time.Time) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		w.Shown = w.Shown[:0]
		return FocusRing(gtx, th, &w.Select, theme.RadiusMd, func(gtx layout.Context) layout.Dimensions {
			return w.Select.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				gtx.Constraints.Min.Y = 0
				bg := th.Surface
				if w.Select.Hovered() {
					bg = theme.Mix(th.Surface, th.Text, 0.04)
				}
				return background(bg, theme.RadiusMd, func(gtx layout.Context) layout.Dimensions {
					return layout.UniformInset(theme.S3).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return rowBody(gtx, th, v, w, now)
					})
				})(gtx)
			})
		})
	}
}

func rowBody(gtx layout.Context, th *theme.Theme, v state.TaskView, w *RowWidgets, now time.Time) layout.Dimensions {
	frac := float32(-1)
	if v.Percent >= 0 {
		frac = float32(v.Percent / 100)
	}
	gap := layout.Rigid(layout.Spacer{Height: theme.S1}.Layout)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(background(th.SurfaceAlt, theme.RadiusSm, func(gtx layout.Context) layout.Dimensions {
					return layout.UniformInset(theme.S1).Layout(gtx, label(th, theme.TextSm, th.TextMuted, fileGlyph(v.FileName)).Layout)
				})),
				layout.Rigid(layout.Spacer{Width: theme.S2}.Layout),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					l := label(th, theme.TextMd, th.Text, platform.MiddleEllipsis(v.FileName, 60))
					l.Font.Weight = font.Medium
					return l.Layout(gtx)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return rowButtons(gtx, th, v, w) }),
			)
		}),
		gap,
		layout.Rigid(label(th, theme.TextSm, statusColor(th, v), state.StatusText(v, now)).Layout),
		gap,
		layout.Rigid(ProgressBar(th, frac)),
		gap,
		layout.Rigid(label(th, theme.TextSm, th.TextMuted, rowMeta(v)).Layout),
	)
}

func rowButtons(gtx layout.Context, th *theme.Theme, v state.TaskView, w *RowWidgets) layout.Dimensions {
	type btn struct {
		name, glyph, desc string
		ok                bool
		c                 *widget.Clickable
	}
	btns := []btn{
		{"pause", "Pause", "Pause download", v.CanPause, &w.Pause},
		{"resume", "Resume", "Resume download", v.CanResume, &w.Resume},
		{"retry", "Retry", "Retry download", v.CanRetry, &w.Retry},
		{"restart", "Restart", "Restart download", v.CanRestart, &w.Restart},
		{"open", "Open", "Open file", v.CanOpen, &w.Open},
		{"cancel", "Cancel", "Cancel download", v.CanCancel, &w.Cancel},
		{"menu", "⋯", "More actions", true, &w.Menu},
	}
	var children []layout.FlexChild
	for _, b := range btns {
		if !b.ok {
			continue
		}
		w.Shown = append(w.Shown, b.name)
		children = append(children, layout.Rigid(IconButton(th, b.c, b.glyph, b.desc)))
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
}
