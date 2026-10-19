package screens

import (
	"errors"
	"image"
	"image/color"
	"math"
	"strconv"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/raptordnj/goti/internal/app"
	"github.com/raptordnj/goti/internal/brand"
	"github.com/raptordnj/goti/internal/config"
	"github.com/raptordnj/goti/internal/domain"
	"github.com/raptordnj/goti/internal/settings"
	"github.com/raptordnj/goti/internal/ui/components"
	"github.com/raptordnj/goti/internal/ui/icons"
	"github.com/raptordnj/goti/internal/ui/state"
	"github.com/raptordnj/goti/internal/ui/theme"
)

// EngineNote is shown under the engine fields (spec §4).
const EngineNote = "Applies to downloads started after saving."

// LimitHelp is the helper text under the speed-limit rows.
const LimitHelp = "0 = unlimited"

// settingsWidth is the max width in dp of the centred settings column.
const settingsWidth = 640

var (
	policyOpts = [][2]string{{string(domain.PolicyRename), "Rename"}, {string(domain.PolicyOverwrite), "Overwrite"}, {string(domain.PolicyFail), "Fail"}}
	themeOpts  = [][2]string{{string(settings.ThemeSystem), "System"}, {string(settings.ThemeLight), "Light"}, {string(settings.ThemeDark), "Dark"}}
)

// Settings is the settings screen. Open loads values; Save validates with
// settings.Validate and shows per-field errors, or emits SaveSettings and
// IntentCloseSettings.
type Settings struct {
	// Version and DataDir are shown in the About group.
	Version, DataDir string
	// Mobile hides the desktop-only rows: download folder (the app's
	// private folder is fixed) and close to tray.
	Mobile bool

	compact bool // last layout was compact: stacked rows and footer

	base                                     settings.Settings
	dir, globalLimit, perLimit               widget.Editor
	maxSim, maxConn                          components.StepperState
	resumeOnStart, closeToTray, reduceMotion widget.Bool
	policy, theme                            widget.Enum
	shownTheme                               string
	save, cancel, change                     widget.Clickable
	list                                     widget.List
	errs                                     map[string]string
}

// Open loads s into the form and clears errors.
func (st *Settings) Open(s settings.Settings) {
	st.base = s
	for _, e := range []*widget.Editor{&st.dir, &st.globalLimit, &st.perLimit} {
		e.SingleLine, e.Submit = true, true
	}
	st.maxSim.Value, st.maxConn.Value = s.MaxSimultaneous, s.MaxConnections
	st.dir.SetText(s.DownloadDir)
	st.globalLimit.SetText(strconv.FormatInt(s.GlobalRateLimit/1024, 10))
	st.perLimit.SetText(strconv.FormatInt(s.PerDownloadLimit/1024, 10))
	st.resumeOnStart.Value, st.closeToTray.Value, st.reduceMotion.Value = s.ResumeOnStartup, s.CloseToTray, s.ReduceMotion
	st.policy.Value, st.theme.Value = string(s.ExistingPolicy), string(s.Theme)
	st.shownTheme = st.theme.Value
	st.errs = nil
}

// collect builds Settings from the form; parse failures go into errs.
func (st *Settings) collect() (settings.Settings, map[string]string) {
	s := st.base
	errs := map[string]string{}
	kb := func(field string, e *widget.Editor, old int64) int64 {
		txt := strings.TrimSpace(e.Text())
		if txt == strconv.FormatInt(old/1024, 10) {
			return old // unchanged: keep sub-KB precision
		}
		n, err := strconv.ParseInt(txt, 10, 64)
		switch {
		case errors.Is(err, strconv.ErrRange) && !strings.HasPrefix(txt, "-"), err == nil && n > math.MaxInt64/1024:
			errs[field] = "Too large"
			return old
		case err != nil:
			errs[field] = "Enter a whole number of KB/s"
		case n < 0:
			errs[field] = "Must be 0 or more"
		}
		return n * 1024
	}
	s.MaxSimultaneous, s.MaxConnections = st.maxSim.Value, st.maxConn.Value
	s.GlobalRateLimit = kb("GlobalRateLimit", &st.globalLimit, st.base.GlobalRateLimit)
	s.PerDownloadLimit = kb("PerDownloadLimit", &st.perLimit, st.base.PerDownloadLimit)
	s.DownloadDir = strings.TrimSpace(st.dir.Text())
	s.ResumeOnStartup, s.CloseToTray, s.ReduceMotion = st.resumeOnStart.Value, st.closeToTray.Value, st.reduceMotion.Value
	s.ExistingPolicy, s.Theme = domain.ExistingFilePolicy(st.policy.Value), settings.Theme(st.theme.Value)
	if err := s.Validate(); err != nil {
		var list []error
		if j, ok := err.(interface{ Unwrap() []error }); ok {
			list = j.Unwrap()
		} else {
			list = []error{err}
		}
		for _, e := range list {
			var fe config.FieldError
			if errors.As(e, &fe) {
				if _, dup := errs[fe.Field]; !dup {
					errs[fe.Field] = fe.Msg
				}
			}
		}
	}
	return s, errs
}

// Layout draws the settings page and returns intents.
func (st *Settings) Layout(gtx layout.Context, th *theme.Theme, _ *state.Snapshot, _ time.Time) []Intent {
	var out []Intent
	if st.cancel.Clicked(gtx) {
		out = append(out, IntentCloseSettings{})
	}
	if st.change.Clicked(gtx) {
		out = append(out, IntentPickFolder{})
	}
	if st.save.Clicked(gtx) {
		s, errs := st.collect()
		st.errs = errs
		if len(errs) == 0 {
			out = append(out, IntentCommand{Cmd: app.SaveSettings{S: s}}, IntentCloseSettings{})
		}
	}
	for _, e := range []*widget.Editor{&st.globalLimit, &st.perLimit, &st.dir} {
		for {
			ev, ok := e.Update(gtx)
			if !ok {
				break
			}
			if _, ok := ev.(widget.ChangeEvent); ok && st.errs != nil {
				delete(st.errs, map[*widget.Editor]string{&st.globalLimit: "GlobalRateLimit", &st.perLimit: "PerDownloadLimit", &st.dir: "DownloadDir"}[e])
			}
		}
	}

	gtx.Constraints.Min = gtx.Constraints.Max
	st.compact = Compact(gtx)
	paint := func(gtx layout.Context) layout.Dimensions { return fill(gtx, th.Bg) }
	layout.Background{}.Layout(gtx, paint, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				ws := st.groups(th)
				st.list.Axis = layout.Vertical
				return st.list.Layout(gtx, len(ws), func(gtx layout.Context, i int) layout.Dimensions {
					in := layout.Inset{Top: theme.S5}
					if i == 0 {
						in.Top = theme.S6
					}
					if i == len(ws)-1 {
						in.Bottom = theme.S6
					}
					return in.Layout(gtx, centred(ws[i]))
				})
			}),
			layout.Rigid(st.footer(th)),
		)
	})
	if st.theme.Value != st.shownTheme {
		st.shownTheme = st.theme.Value
		out = append(out, IntentPreviewTheme{Theme: settings.Theme(st.theme.Value)})
	}
	return out
}

// centred caps w at settingsWidth (minus 16-dp gutters) and centres it.
func centred(w layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		full := gtx.Constraints.Max.X
		cw := min(gtx.Dp(settingsWidth), full-2*gtx.Dp(theme.S4))
		gtx.Constraints.Min.X, gtx.Constraints.Max.X = cw, cw
		pad := (full - cw) / 2
		defer op.Offset(image.Pt(pad, 0)).Push(gtx.Ops).Pop()
		d := w(gtx)
		d.Size.X = full
		return d
	}
}

// footer is the sticky Save/Cancel bar.
func (st *Settings) footer(th *theme.Theme) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Background{}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			sz := gtx.Constraints.Min
			fill(gtx, th.Surface)
			paintRect(gtx, th.Hairline, image.Rect(0, 0, sz.X, gtx.Dp(1)))
			return layout.Dimensions{Size: sz}
		}, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: theme.S3, Bottom: theme.S3}.Layout(gtx, centred(func(gtx layout.Context) layout.Dimensions {
				if st.compact {
					return st.compactFooter(gtx, th)
				}
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						if len(st.errs) == 0 {
							return Label(th, theme.Caption, th.TextMuted, EngineNote)(gtx)
						}
						return Label(th, theme.Caption, th.Error, "Fix the highlighted fields to save.")(gtx)
					}),
					layout.Rigid(components.Button(th, &st.cancel, "Cancel", components.Secondary)),
					layout.Rigid(layout.Spacer{Width: theme.S2}.Layout),
					layout.Rigid(components.Button(th, &st.save, "Save", components.Primary)),
				)
			}))
		})
	}
}

// compactFooter stacks the note over right-aligned buttons.
func (st *Settings) compactFooter(gtx layout.Context, th *theme.Theme) layout.Dimensions {
	note := Text(th, theme.Caption, th.TextMuted, EngineNote, 2)
	if len(st.errs) > 0 {
		note = Text(th, theme.Caption, th.Error, "Fix the highlighted fields to save.", 2)
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(note),
		layout.Rigid(layout.Spacer{Height: theme.S2}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.E.Layout(gtx, Row(
				components.Button(th, &st.cancel, "Cancel", components.Secondary),
				components.Button(th, &st.save, "Save", components.Primary)))
		}),
	)
}

func paintRect(gtx layout.Context, c color.NRGBA, r image.Rectangle) {
	paint.FillShape(gtx.Ops, c, clip.Rect(r).Op())
}

// setting is one row: label (+ muted helper or inline error) on the left,
// control right-aligned.
type setting struct {
	label, help, err string
	control          layout.Widget
	// stack puts the control under the text in compact layouts (wide
	// controls such as segmented pickers).
	stack bool
}

// settingsGroup draws an uppercase caption title over a card of rows.
func settingsGroup(th *theme.Theme, compact bool, title string, rows ...setting) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: theme.S1, Bottom: theme.S2}.Layout(gtx, Label(th, theme.Caption, th.TextMuted, strings.ToUpper(title)))
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return panel(gtx, th, func(gtx layout.Context) layout.Dimensions {
					var ch []layout.FlexChild
					for i, r := range rows {
						if i > 0 {
							ch = append(ch, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								w := gtx.Constraints.Max.X
								paintRect(gtx, th.Hairline, image.Rect(gtx.Dp(theme.S4), 0, w, gtx.Dp(1)))
								return layout.Dimensions{Size: image.Pt(w, gtx.Dp(1))}
							}))
						}
						ch = append(ch, layout.Rigid(settingRow(th, r, compact && r.stack)))
					}
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
				})
			}),
		)
	}
}

// panel is a Surface card with a hairline border.
func panel(gtx layout.Context, th *theme.Theme, w layout.Widget) layout.Dimensions {
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	return layout.Background{}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		sz := gtx.Constraints.Min
		components.RoundedFill(gtx, th.Surface, sz, theme.RadiusPanel)
		widget.Border{Color: th.Hairline, CornerRadius: theme.RadiusPanel, Width: 1}.Layout(gtx, func(layout.Context) layout.Dimensions {
			return layout.Dimensions{Size: sz}
		})
		return layout.Dimensions{Size: sz}
	}, w)
}

func settingRow(th *theme.Theme, r setting, stacked bool) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Inset{Left: theme.S4, Right: theme.S4, Top: theme.S3, Bottom: theme.S3}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			left := []layout.Widget{Label(th, theme.Body, th.Text, r.label)}
			if r.help != "" {
				left = append(left, Text(th, theme.Caption, th.TextMuted, r.help, 2))
			}
			if r.err != "" {
				left = append(left, errLine(th, r.err))
			}
			if stacked {
				return Column(theme.S2, Column(theme.S0, left...), r.control)(gtx)
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, Column(theme.S0, left...)),
				layout.Rigid(layout.Spacer{Width: theme.S4}.Layout),
				layout.Rigid(r.control),
			)
		})
	}
}

func errLine(th *theme.Theme, msg string) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return icons.Alert(gtx, 13, th.Error) }),
			layout.Rigid(layout.Spacer{Width: theme.S1}.Layout),
			layout.Flexed(1, Text(th, theme.Caption, th.Error, msg, 2)),
		)
	}
}

// sized fixes a control's width to w dp.
func sized(w unit.Dp, c layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		px := min(gtx.Dp(w), gtx.Constraints.Max.X)
		gtx.Constraints.Min.X, gtx.Constraints.Max.X = px, px
		return c(gtx)
	}
}

// switchCtl is a bare Switch (the row supplies the label).
func switchCtl(th *theme.Theme, b *widget.Bool) layout.Widget {
	return sized(32, components.Switch(th, b, ""))
}

func (st *Settings) groups(th *theme.Theme) []layout.Widget {
	unitField := func(e *widget.Editor, field string) layout.Widget {
		return sized(140, components.UnitField(th, e, "KB/s", "0", st.errs[field] != ""))
	}
	c := st.compact
	var downloads []setting
	if !st.Mobile {
		downloads = append(downloads, setting{"Download folder", "Where new downloads are saved", st.errs["DownloadDir"],
			func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(sized(220, components.TextField(th, &st.dir, "", "Folder path", ""))),
					layout.Rigid(layout.Spacer{Width: theme.S2}.Layout),
					layout.Rigid(components.Button(th, &st.change, "Change…", components.Secondary, components.Small())),
				)
			}, false})
	}
	downloads = append(downloads,
		setting{"Simultaneous downloads", "How many files download at once (1–10)", st.errs["MaxSimultaneous"],
			components.Stepper(th, &st.maxSim, 1, 10), false},
		setting{"When the file exists", "What to do if a file with the same name is already there", st.errs["ExistingPolicy"],
			components.Segmented(th, &st.policy, policyOpts), true},
	)
	behaviour := []setting{
		{"Resume downloads on startup", "Continue unfinished downloads when " + brand.Name + " opens", "", switchCtl(th, &st.resumeOnStart), false},
	}
	if !st.Mobile {
		behaviour = append(behaviour, setting{"Close to tray", "Keep running in the system tray when the window closes", "", switchCtl(th, &st.closeToTray), false})
	}
	return []layout.Widget{
		bold(th, theme.Display, th.Text, "Settings"),
		settingsGroup(th, c, "Downloads", downloads...),
		settingsGroup(th, c, "Network",
			setting{"Connections per download", "Parallel connections for one file (1–16)", st.errs["MaxConnections"],
				components.Stepper(th, &st.maxConn, 1, 16), false},
			setting{"Global speed limit", LimitHelp, st.errs["GlobalRateLimit"], unitField(&st.globalLimit, "GlobalRateLimit"), false},
			setting{"Per-download limit", LimitHelp, st.errs["PerDownloadLimit"], unitField(&st.perLimit, "PerDownloadLimit"), false},
		),
		settingsGroup(th, c, "Appearance",
			setting{"Theme", "Preview applies immediately", "", components.Segmented(th, &st.theme, themeOpts), true},
			setting{"Reduce motion", "Turn off animations and transitions", "", switchCtl(th, &st.reduceMotion), false},
		),
		settingsGroup(th, c, "Behaviour", behaviour...),
		st.about(th),
	}
}

// Dir returns the download folder field's text.
func (st *Settings) Dir() string { return st.dir.Text() }

// SetDir puts a picked folder into the download folder field.
func (st *Settings) SetDir(p string) {
	st.dir.SetText(p)
	delete(st.errs, "DownloadDir")
}

// ScrollToAbout scrolls the form so the About group is visible.
func (st *Settings) ScrollToAbout() {
	st.list.Position.First = 1 << 20 // clamped to the last item by List
	st.list.Position.Offset = 0
}

// about is the About group (spec B11): mark, name, wordmark, tagline,
// version and data directory.
func (st *Settings) about(th *theme.Theme) layout.Widget {
	version := st.Version
	if version == "" {
		version = "dev"
	}
	mark := func(gtx layout.Context) layout.Dimensions { return appMark(gtx, th) }
	meta := []layout.Widget{Label(th, theme.Caption, th.TextMuted, "Version "+version)}
	if st.DataDir != "" {
		meta = append(meta, Label(th, theme.Caption, th.TextMuted, "Data folder: "+st.DataDir))
	}
	card := func(gtx layout.Context) layout.Dimensions {
		return panel(gtx, th, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.UniformInset(theme.S4).Layout(gtx, Column(theme.S3,
				Row(mark, Column(0,
					bold(th, theme.Title, th.Text, brand.Name),
					Label(th, theme.Body, th.TextMuted, brand.Wordmark))),
				Text(th, theme.Body, th.Text, brand.Tagline, 2),
				Column(theme.S1, meta...),
			))
		})
	}
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: theme.S1, Bottom: theme.S2}.Layout(gtx, Label(th, theme.Caption, th.TextMuted, "ABOUT"))
			}),
			layout.Rigid(card),
		)
	}
}

// HasFocus reports whether one of the form's text fields has keyboard focus.
func (st *Settings) HasFocus(gtx layout.Context) bool {
	for _, e := range []*widget.Editor{&st.dir, &st.globalLimit, &st.perLimit} {
		if gtx.Focused(e) {
			return true
		}
	}
	return false
}
