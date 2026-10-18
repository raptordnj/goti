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

	"github.com/raptordnj/swiftfetch/internal/domain"
	"github.com/raptordnj/swiftfetch/internal/platform"
	"github.com/raptordnj/swiftfetch/internal/ui/anim"
	"github.com/raptordnj/swiftfetch/internal/ui/icons"
	"github.com/raptordnj/swiftfetch/internal/ui/state"
	"github.com/raptordnj/swiftfetch/internal/ui/theme"
)

// RowWidgets is the per-row widget state. Shown records the action buttons
// laid out in the most recent frame (names: pause, resume, cancel, retry,
// restart, open, menu); ShownName is the (ellipsized) file name drawn.
type RowWidgets struct {
	Pause, Resume, Cancel, Retry, Restart, Open, Menu, Select widget.Clickable
	Progress                                                  anim.Value
	Shown                                                     []string
	ShownName                                                 string
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
	return strings.Join(parts, metaSep)
}

func statusColor(th *theme.Theme, v state.TaskView) color.NRGBA {
	switch {
	case v.State == domain.StateFailed:
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
		return FocusRing(gtx, th, &w.Select, theme.RadiusRow, func(gtx layout.Context) layout.Dimensions {
			return w.Select.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				gtx.Constraints.Min.Y = 0
				bg := th.Surface
				if w.Select.Hovered() {
					bg = theme.Mix(th.Surface, th.Text, 0.04)
				}
				return background(bg, theme.RadiusRow, func(gtx layout.Context) layout.Dimensions {
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
				layout.Rigid(background(th.SurfaceSunken, theme.RadiusControl, func(gtx layout.Context) layout.Dimensions {
					return layout.UniformInset(theme.S1).Layout(gtx, label(th, theme.Caption, th.TextMuted, fileGlyph(v.FileName)).Layout)
				})),
				layout.Rigid(layout.Spacer{Width: theme.S2}.Layout),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					w.ShownName = fitName(v.FileName, runeBudget(gtx, gtx.Constraints.Max.X))
					l := label(th, theme.Body, th.Text, w.ShownName)
					l.Font.Weight = font.Medium
					return l.Layout(gtx)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return rowButtons(gtx, th, v, w) }),
			)
		}),
		gap,
		layout.Rigid(label(th, theme.Caption, statusColor(th, v), state.StatusText(v, now)).Layout),
		gap,
		layout.Rigid(ProgressBar(th, &w.Progress, frac, theme.StateColor(th.Palette, v.State, v.Interrupted))),
		gap,
		layout.Rigid(label(th, theme.Caption, th.TextMuted, rowMeta(v)).Layout),
	)
}

func rowButtons(gtx layout.Context, th *theme.Theme, v state.TaskView, w *RowWidgets) layout.Dimensions {
	ok := map[string]bool{"pause": v.CanPause, "resume": v.CanResume, "retry": v.CanRetry,
		"restart": v.CanRestart, "open": v.CanOpen, "cancel": v.CanCancel, "menu": true}
	var children []layout.FlexChild
	for _, b := range rowButtonSpecs(w) {
		if !ok[b.name] {
			continue
		}
		w.Shown = append(w.Shown, b.name)
		children = append(children, layout.Rigid(IconButton(th, b.c, b.icon, b.desc)))
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
}

type rowButton struct {
	name, desc string
	icon       icons.Icon
	c          *widget.Clickable
}

func rowButtonSpecs(w *RowWidgets) []rowButton {
	return []rowButton{
		{"pause", "Pause download", icons.Pause, &w.Pause},
		{"resume", "Resume download", icons.Play, &w.Resume},
		{"retry", "Retry download", icons.Retry, &w.Retry},
		{"restart", "Restart download", icons.Restart, &w.Restart},
		{"open", "Open file", icons.File, &w.Open},
		{"cancel", "Cancel download", icons.Close, &w.Cancel},
		{"menu", "More actions", icons.More, &w.Menu},
	}
}

// runeBudget conservatively estimates how many Body runes fit in px
// (average Go font advance is about 0.55em; 0.6em leaves headroom).
func runeBudget(gtx layout.Context, px int) int {
	per := float32(gtx.Sp(theme.Body)) * 0.6
	if per <= 0 {
		return 8
	}
	return max(8, int(float32(px)/per))
}

// fitName middle-ellipsizes name to n runes, always keeping its extension
// (including compound ones such as .tar.gz) visible.
func fitName(name string, n int) string {
	r := []rune(name)
	if len(r) <= n {
		return name
	}
	ext := filepath.Ext(name)
	if stem := strings.TrimSuffix(name, ext); strings.EqualFold(filepath.Ext(stem), ".tar") {
		ext = filepath.Ext(stem) + ext
	}
	er := len([]rune(ext))
	if ext == "" || er > 12 || n-er < 4 {
		return platform.MiddleEllipsis(name, n)
	}
	return platform.MiddleEllipsis(strings.TrimSuffix(name, ext), n-er) + ext
}
