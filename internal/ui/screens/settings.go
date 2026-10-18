package screens

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/widget"

	"github.com/raptordnj/swiftfetch/internal/app"
	"github.com/raptordnj/swiftfetch/internal/config"
	"github.com/raptordnj/swiftfetch/internal/domain"
	"github.com/raptordnj/swiftfetch/internal/settings"
	"github.com/raptordnj/swiftfetch/internal/ui/components"
	"github.com/raptordnj/swiftfetch/internal/ui/state"
	"github.com/raptordnj/swiftfetch/internal/ui/theme"
)

// EngineNote is shown under the engine fields (spec §4).
const EngineNote = "Applies to downloads started after saving."

var (
	policies = []domain.ExistingFilePolicy{domain.PolicyRename, domain.PolicyOverwrite, domain.PolicyFail}
	themes   = []settings.Theme{settings.ThemeSystem, settings.ThemeLight, settings.ThemeDark}
)

// Settings is the settings screen. Open loads values; Save validates with
// settings.Validate and shows per-field errors, or emits SaveSettings and
// IntentCloseSettings.
type Settings struct {
	base                                        settings.Settings
	maxSim, dir, maxConn, globalLimit, perLimit widget.Editor
	resumeOnStart, closeToTray                  bool
	policy                                      domain.ExistingFilePolicy
	theme                                       settings.Theme
	resumeBtn, trayBtn, policyBtn, themeBtn     widget.Clickable
	save, cancel                                widget.Clickable
	list                                        widget.List
	errs                                        map[string]string
}

// Open loads s into the form and clears errors.
func (st *Settings) Open(s settings.Settings) {
	st.base = s
	for _, e := range []*widget.Editor{&st.maxSim, &st.dir, &st.maxConn, &st.globalLimit, &st.perLimit} {
		e.SingleLine, e.Submit = true, true
	}
	st.maxSim.SetText(strconv.Itoa(s.MaxSimultaneous))
	st.dir.SetText(s.DownloadDir)
	st.maxConn.SetText(strconv.Itoa(s.MaxConnections))
	st.globalLimit.SetText(strconv.FormatInt(s.GlobalRateLimit, 10))
	st.perLimit.SetText(strconv.FormatInt(s.PerDownloadLimit, 10))
	st.resumeOnStart, st.closeToTray = s.ResumeOnStartup, s.CloseToTray
	st.policy, st.theme = s.ExistingPolicy, s.Theme
	st.errs = nil
}

// collect builds Settings from the form; parse failures go into errs.
func (st *Settings) collect() (settings.Settings, map[string]string) {
	s := st.base
	errs := map[string]string{}
	atoi := func(field string, e *widget.Editor) int64 {
		n, err := strconv.ParseInt(strings.TrimSpace(e.Text()), 10, 64)
		if err != nil {
			errs[field] = "must be a whole number"
		}
		return n
	}
	s.MaxSimultaneous = int(atoi("MaxSimultaneous", &st.maxSim))
	s.MaxConnections = int(atoi("MaxConnections", &st.maxConn))
	s.GlobalRateLimit = atoi("GlobalRateLimit", &st.globalLimit)
	s.PerDownloadLimit = atoi("PerDownloadLimit", &st.perLimit)
	s.DownloadDir = strings.TrimSpace(st.dir.Text())
	s.ResumeOnStartup, s.CloseToTray = st.resumeOnStart, st.closeToTray
	s.ExistingPolicy, s.Theme = st.policy, st.theme
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

func next[T comparable](all []T, cur T) T {
	for i, v := range all {
		if v == cur {
			return all[(i+1)%len(all)]
		}
	}
	return all[0]
}

// Layout draws the settings form and returns intents.
func (st *Settings) Layout(gtx layout.Context, th *theme.Theme, _ *state.Snapshot, _ time.Time) []Intent {
	var out []Intent
	if st.resumeBtn.Clicked(gtx) {
		st.resumeOnStart = !st.resumeOnStart
	}
	if st.trayBtn.Clicked(gtx) {
		st.closeToTray = !st.closeToTray
	}
	if st.policyBtn.Clicked(gtx) {
		st.policy = next(policies, st.policy)
	}
	if st.themeBtn.Clicked(gtx) {
		st.theme = next(themes, st.theme)
		out = append(out, IntentPreviewTheme{Theme: st.theme})
	}
	if st.cancel.Clicked(gtx) {
		out = append(out, IntentCloseSettings{})
	}
	if st.save.Clicked(gtx) {
		s, errs := st.collect()
		st.errs = errs
		if len(errs) == 0 {
			out = append(out, IntentCommand{Cmd: app.SaveSettings{S: s}}, IntentCloseSettings{})
		}
	}

	paint := func(gtx layout.Context) layout.Dimensions { return fill(gtx, th.Bg) }
	gtx.Constraints.Min = gtx.Constraints.Max
	layout.Background{}.Layout(gtx, paint, func(gtx layout.Context) layout.Dimensions {
		ws := st.fields(th)
		st.list.Axis = layout.Vertical
		return layout.UniformInset(theme.S5).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return st.list.Layout(gtx, len(ws), func(gtx layout.Context, i int) layout.Dimensions {
				return layout.Inset{Bottom: theme.S4}.Layout(gtx, ws[i])
			})
		})
	})
	return out
}

func onOff(b bool) string {
	if b {
		return "On"
	}
	return "Off"
}

func (st *Settings) choice(th *theme.Theme, name string, c *widget.Clickable, value string) layout.Widget {
	return Column(theme.S1, Label(th, theme.TextSm, th.TextMuted, name),
		components.Button(th, c, value, components.Secondary))
}

func (st *Settings) fields(th *theme.Theme) []layout.Widget {
	tf := func(e *widget.Editor, field, name, hint string) layout.Widget {
		return components.TextField(th, e, name, hint, st.errs[field])
	}
	errText := func(field string) layout.Widget {
		// Record field errors for tests; TextField draws them itself.
		return func(gtx layout.Context) layout.Dimensions {
			if m := st.errs[field]; m != "" {
				record(m)
			}
			return layout.Dimensions{}
		}
	}
	return []layout.Widget{
		bold(th, theme.TextXl, th.Text, "Settings"),
		Column(0, tf(&st.maxSim, "MaxSimultaneous", "Simultaneous downloads", "1-10"), errText("MaxSimultaneous")),
		st.choice(th, "Resume downloads on startup", &st.resumeBtn, onOff(st.resumeOnStart)),
		Column(0, tf(&st.dir, "DownloadDir", "Download folder", "Folder path"), errText("DownloadDir")),
		Column(0, tf(&st.maxConn, "MaxConnections", "Connections per download", "1-16"), errText("MaxConnections")),
		Column(0, tf(&st.globalLimit, "GlobalRateLimit", "Global speed limit (bytes/s, 0 = unlimited)", "0"), errText("GlobalRateLimit")),
		Column(0, tf(&st.perLimit, "PerDownloadLimit", "Per-download limit (bytes/s, 0 = unlimited)", "0"), errText("PerDownloadLimit")),
		st.choice(th, "When the file exists", &st.policyBtn, string(st.policy)),
		Label(th, theme.TextSm, th.TextMuted, EngineNote),
		st.choice(th, "Theme", &st.themeBtn, string(st.theme)),
		st.choice(th, "Close to tray", &st.trayBtn, onOff(st.closeToTray)),
		Row(components.Button(th, &st.save, "Save", components.Primary),
			components.Button(th, &st.cancel, "Cancel", components.Ghost)),
	}
}
