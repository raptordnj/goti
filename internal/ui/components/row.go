package components

import (
	"image"
	"image/color"
	"path/filepath"
	"strings"
	"time"

	"gioui.org/gesture"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/raptordnj/goti/internal/domain"
	"github.com/raptordnj/goti/internal/platform"
	"github.com/raptordnj/goti/internal/ui/anim"
	"github.com/raptordnj/goti/internal/ui/icons"
	"github.com/raptordnj/goti/internal/ui/state"
	"github.com/raptordnj/goti/internal/ui/theme"
)

// Row geometry.
const (
	rowHeight unit.Dp = 56
	tileSize  unit.Dp = 32
	actionBox unit.Dp = 28
)

// RowWidgets is the per-row widget state. All of a row's animation state
// lives here, so it is dropped with the row. Shown records the trailing
// buttons laid out in the most recent frame (pause, resume, retry, open,
// menu); ShownName is the (ellipsized) file name drawn and NameWidth the
// width of the text column in px. MoreAt is the bottom-right corner of the
// more button relative to the row origin (menu anchor).
type RowWidgets struct {
	Pause, Resume, Retry, Open, Cancel, More, Select widget.Clickable
	Hover                                            gesture.Hover
	Progress                                         anim.Value
	Menu                                             MenuState
	Shown                                            []string
	ShownName                                        string
	NameWidth                                        int
	MoreAt                                           image.Point
	// Optimistic, when set, is the primary action ("pause", "resume",
	// "retry") to show in place of the state's own while a command is in
	// flight; the owner clears it when the task changes or the command fails.
	Optimistic string

	hot, sel anim.Value
}

// RowMenuItems lists the overflow menu of v; CanX-gated entries appear only
// when allowed.
func RowMenuItems(v state.TaskView) []MenuItem {
	var out []MenuItem
	if v.CanOpen {
		out = append(out, MenuItem{Key: "open", Label: "Open file", Icon: icons.File})
	}
	out = append(out,
		MenuItem{Key: "folder", Label: "Show in folder", Icon: icons.Folder},
		MenuItem{Key: "copy-url", Label: "Copy link", Icon: icons.Copy},
		MenuItem{Key: "copy-path", Label: "Copy path", Icon: icons.Copy},
	)
	if v.CanRestart {
		out = append(out, MenuItem{Key: "restart", Label: "Restart", Icon: icons.Restart})
	}
	if v.CanCancel {
		out = append(out, MenuItem{Key: "cancel", Label: "Cancel", Icon: icons.Close})
	}
	return append(out, MenuItem{Key: "remove", Label: "Remove…", Icon: icons.Trash, Danger: true})
}

type rowButton struct {
	name, desc string
	icon       icons.Icon
	c          *widget.Clickable
}

// primaryAction is the one always-visible state action of v (nil if none).
func primaryAction(v state.TaskView, w *RowWidgets) *rowButton {
	switch w.Optimistic {
	case "pause":
		return &rowButton{"pause", "Pause download", icons.Pause, &w.Pause}
	case "resume":
		return &rowButton{"resume", "Resume download", icons.Play, &w.Resume}
	}
	switch {
	case v.CanPause:
		return &rowButton{"pause", "Pause download", icons.Pause, &w.Pause}
	case v.CanResume:
		return &rowButton{"resume", "Resume download", icons.Play, &w.Resume}
	case v.CanRetry:
		return &rowButton{"retry", "Retry download", icons.Retry, &w.Retry}
	case v.CanOpen:
		return &rowButton{"open", "Open file", icons.File, &w.Open}
	case v.CanCancel:
		return &rowButton{"cancel", "Cancel download", icons.Close, &w.Cancel}
	}
	return nil
}

// transferring reports whether v is actively moving bytes (or about to).
func transferring(v state.TaskView) bool {
	switch v.State {
	case domain.StateDownloading, domain.StateInspecting, domain.StateVerifying:
		return !v.Interrupted
	}
	return false
}

// rowFrac is the progress fraction to draw: indeterminate (-1) only for an
// actively transferring task of unknown size; idle tasks get a static bar.
func rowFrac(v state.TaskView) float32 {
	if v.Percent >= 0 {
		return float32(min(v.Percent, 100) / 100)
	}
	if transferring(v) {
		return -1
	}
	return 0
}

// metaSlots builds the numeric parts of the meta line (after the status).
func metaSlots(v state.TaskView) []string {
	var out []string
	done := max(v.Downloaded, 0)
	switch {
	case v.State == domain.StateCompleted:
		out = append(out, platform.FormatBytes(max(done, v.Total)))
	case v.State == domain.StateFailed || v.State == domain.StateCancelled:
	case v.TotalKnown && v.Total > 0:
		out = append(out, platform.FormatBytes(done)+" of "+platform.FormatBytes(v.Total))
	case done > 0:
		out = append(out, platform.FormatBytes(done))
	}
	if transferring(v) || v.State == domain.StateRetrying {
		if v.SpeedBps > 0 && v.State != domain.StateRetrying {
			out = append(out, platform.FormatSpeed(v.SpeedBps))
		}
		if v.ETA != nil && *v.ETA >= 0 && v.State == domain.StateDownloading {
			out = append(out, platform.FormatETA(*v.ETA)+" left")
		}
	}
	return out
}

// TaskRow renders one 56-dp task row: file tile, name, meta line with state
// dot, eased progress and trailing actions (primary always; more on hover,
// selection or focus).
func TaskRow(th *theme.Theme, v state.TaskView, w *RowWidgets, now time.Time, selected bool) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		w.Shown = w.Shown[:0]
		h := gtx.Dp(rowHeight)
		gtx.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X, h))
		return FocusRing(gtx, th, &w.Select, theme.RadiusRow, func(gtx layout.Context) layout.Dimensions {
			return w.Select.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				pointer.CursorPointer.Add(gtx.Ops)
				hovered := w.Hover.Update(gtx.Source) || w.Select.Hovered()
				w.Hover.Add(gtx.Ops)
				focused := gtx.Focused(&w.Select) || gtx.Focused(&w.More) || w.Menu.Open
				for _, c := range []*widget.Clickable{&w.Pause, &w.Resume, &w.Retry, &w.Open, &w.Cancel} {
					focused = focused || gtx.Focused(c)
				}
				w.hot.Set(b2f(hovered))
				w.sel.Set(b2f(selected))
				hot, sel := w.hot.Get(gtx), w.sel.Get(gtx)
				sz := gtx.Constraints.Min
				rowBg(gtx, th, sz, hot, sel)
				showMore := hovered || selected || focused
				return layout.Inset{Left: theme.S3, Right: theme.S3}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return rowBody(gtx, th, v, w, now, showMore)
				})
			})
		})
	}
}

// rowBg paints the hover (SurfaceSunken @ 60 %) and selection (AccentSoft
// plus a 2-dp accent bar) backgrounds, eased.
func rowBg(gtx layout.Context, th *theme.Theme, sz image.Point, hot, sel float32) {
	if hot > 0 {
		c := th.SurfaceSunken
		fillRRect(gtx, theme.WithAlpha(c, uint8(float32(c.A)*0.6*hot)), sz, theme.RadiusRow)
	}
	if sel > 0 {
		c := th.AccentSoft
		fillRRect(gtx, theme.WithAlpha(c, uint8(float32(c.A)*sel)), sz, theme.RadiusRow)
		bar := image.Rect(0, gtx.Dp(theme.S3), gtx.Dp(2), sz.Y-gtx.Dp(theme.S3))
		a := th.Accent
		paint.FillShape(gtx.Ops, theme.WithAlpha(a, uint8(float32(a.A)*sel)), clip.UniformRRect(bar, gtx.Dp(1)).Op(gtx.Ops))
	}
}

func rowBody(gtx layout.Context, th *theme.Theme, v state.TaskView, w *RowWidgets, now time.Time, showMore bool) layout.Dimensions {
	rowW := gtx.Constraints.Max.X + gtx.Dp(theme.S3)*2
	box := gtx.Dp(actionBox)
	// The more button's slot is the right-most one; its bottom-right corner
	// anchors the overflow menu.
	w.MoreAt = image.Pt(rowW-gtx.Dp(theme.S3), gtx.Dp(rowHeight)/2+box/2)
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return fileTile(gtx, th, v.FileName) }),
		layout.Rigid(layout.Spacer{Width: theme.S3}.Layout),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			w.NameWidth = gtx.Constraints.Max.X
			return textColumn(gtx, th, v, w, now)
		}),
		layout.Rigid(layout.Spacer{Width: theme.S3}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return trailing(gtx, th, v, w, showMore)
		}),
	)
}

func fileTile(gtx layout.Context, th *theme.Theme, name string) layout.Dimensions {
	gtx = fixed(gtx, tileSize, tileSize)
	fillRRect(gtx, th.SurfaceSunken, gtx.Constraints.Min, theme.RadiusRow)
	layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return icons.ForFile(name)(gtx, 16, th.TextMuted)
	})
	return layout.Dimensions{Size: gtx.Constraints.Min}
}

func textColumn(gtx layout.Context, th *theme.Theme, v state.TaskView, w *RowWidgets, now time.Time) layout.Dimensions {
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	gtx.Constraints.Min.Y = 0
	name := styled(th, theme.StyleBodyStrong, th.Text, "")
	w.ShownName = fitName(v.FileName, func(s string) bool {
		name.Text = s
		return measure(gtx, name) <= gtx.Constraints.Max.X
	})
	name.Text = w.ShownName
	col := theme.StateColor(th.Palette, v.State, v.Interrupted)
	barCol := theme.BarColor(th.Palette, v.State, v.Interrupted)
	frac := rowFrac(v)
	bar := v.State != domain.StateCompleted && !(frac == 0 && max(v.Downloaded, 0) == 0)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(name.Layout),
		layout.Rigid(layout.Spacer{Height: theme.S0}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return metaLine(gtx, th, v, now, col) }),
		layout.Rigid(layout.Spacer{Height: theme.S1}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			// The bar's space is always reserved so rows align.
			if !bar {
				return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, gtx.Dp(3))}
			}
			return ProgressBar(th, &w.Progress, frac, barCol)(gtx)
		}),
	)
}

// metaLine draws "● Status · 412 MB of 1.2 GB · 8.4 MB/s · 1 min left".
// Each numeric slot is as wide as its text with every digit replaced by
// "0", so changing values do not shift the slots that follow.
func metaLine(gtx layout.Context, th *theme.Theme, v state.TaskView, now time.Time, dot color.NRGBA) layout.Dimensions {
	stCol := th.TextMuted
	if v.State == domain.StateFailed {
		stCol = th.Error
	}
	children := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			d := gtx.Dp(6)
			sz := image.Pt(d+gtx.Dp(6), d)
			paint.FillShape(gtx.Ops, dot, clip.Ellipse{Max: image.Pt(d, d)}.Op(gtx.Ops))
			return layout.Dimensions{Size: sz}
		}),
		layout.Rigid(styled(th, theme.StyleCaption, stCol, state.StatusText(v, now)).Layout),
	}
	for _, s := range metaSlots(v) {
		children = append(children,
			layout.Rigid(styled(th, theme.StyleCaption, th.TextMuted, metaSep).Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return numSlot(gtx, styled(th, theme.StyleCaption, th.TextMuted, s), zeroDigits(s))
			}))
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, children...)
}

// numSlot lays out l right-aligned in a slot as wide as l would be with the
// text ref, so the separators around it keep their natural spacing.
func numSlot(gtx layout.Context, l material.LabelStyle, ref string) layout.Dimensions {
	r := l
	r.Text = ref
	full, got := measure(gtx, r), measure(gtx, l)
	pad := max(0, min(full, gtx.Constraints.Max.X)-got)
	gtx.Constraints.Min.X = 0
	defer op.Offset(image.Pt(pad, 0)).Push(gtx.Ops).Pop()
	d := l.Layout(gtx)
	d.Size.X += pad
	return d
}

func trailing(gtx layout.Context, th *theme.Theme, v state.TaskView, w *RowWidgets, showMore bool) layout.Dimensions {
	box := gtx.Dp(actionBox)
	slot := func(b *rowButton) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if b == nil {
				return layout.Dimensions{Size: image.Pt(box, box)}
			}
			w.Shown = append(w.Shown, b.name)
			return IconButton(th, b.c, b.icon, b.desc)(gtx)
		})
	}
	var more *rowButton
	if showMore {
		more = &rowButton{"menu", "More actions", icons.More, &w.More}
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		slot(primaryAction(v, w)),
		layout.Rigid(layout.Spacer{Width: theme.S1}.Layout),
		slot(more),
	)
}

// measure returns the laid-out width of l without drawing it.
func measure(gtx layout.Context, l interface {
	Layout(layout.Context) layout.Dimensions
}) int {
	m := op.Record(gtx.Ops)
	gtx.Constraints = layout.Constraints{Max: image.Pt(1<<20, 1<<20)}
	d := l.Layout(gtx)
	m.Stop()
	return d.Size.X
}

// zeroDigits replaces every digit of s with '0'.
func zeroDigits(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return '0'
		}
		return r
	}, s)
}

// fitName middle-ellipsizes name to the longest form fits accepts, always
// keeping its extension (including compound ones such as .tar.gz) visible.
// fits measures with the real shaper, so no per-font advance guess is needed.
func fitName(name string, fits func(string) bool) string {
	if fits(name) {
		return name
	}
	lo, hi := 4, len([]rune(name))-1
	best := shorten(name, lo)
	for lo <= hi {
		mid := (lo + hi) / 2
		if s := shorten(name, mid); fits(s) {
			best, lo = s, mid+1
		} else {
			hi = mid - 1
		}
	}
	return best
}

// shorten middle-ellipsizes name to n runes, keeping the extension.
func shorten(name string, n int) string {
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
