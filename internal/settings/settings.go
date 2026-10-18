// Package settings holds the user-editable application settings.
package settings

import (
	"errors"
	"fmt"

	"github.com/raptordnj/swiftfetch/internal/config"
	"github.com/raptordnj/swiftfetch/internal/domain"
)

// Theme is the UI colour scheme.
type Theme string

// Supported themes.
const (
	ThemeSystem Theme = "system"
	ThemeLight  Theme = "light"
	ThemeDark   Theme = "dark"
)

// Settings are the persisted user preferences.
type Settings struct {
	MaxSimultaneous  int // 1-10
	ResumeOnStartup  bool
	DownloadDir      string
	MaxConnections   int   // 1-16
	GlobalRateLimit  int64 // bytes/s, 0 = unlimited
	PerDownloadLimit int64 // bytes/s, 0 = unlimited
	ExistingPolicy   domain.ExistingFilePolicy
	Theme            Theme
	CloseToTray      bool
	TrayNoticeShown  bool
	ReduceMotion     bool
}

// Default returns the default settings.
func Default() Settings {
	return Settings{
		MaxSimultaneous: 3,
		ResumeOnStartup: true,
		DownloadDir:     config.DefaultDownloadDir(),
		MaxConnections:  8,
		ExistingPolicy:  domain.PolicyRename,
		Theme:           ThemeSystem,
		CloseToTray:     true,
	}
}

// fieldErrors returns one FieldError per invalid field.
func (s Settings) fieldErrors() []config.FieldError {
	var errs []config.FieldError
	bad := func(field, format string, a ...any) {
		errs = append(errs, config.FieldError{Field: field, Msg: fmt.Sprintf(format, a...)})
	}
	if s.MaxSimultaneous < 1 || s.MaxSimultaneous > 10 {
		bad("MaxSimultaneous", "must be 1-10, got %d", s.MaxSimultaneous)
	}
	if s.DownloadDir == "" {
		bad("DownloadDir", "must not be empty")
	}
	if s.MaxConnections < 1 || s.MaxConnections > 16 {
		bad("MaxConnections", "must be 1-16, got %d", s.MaxConnections)
	}
	if s.GlobalRateLimit < 0 {
		bad("GlobalRateLimit", "must be >= 0, got %d", s.GlobalRateLimit)
	}
	if s.PerDownloadLimit < 0 {
		bad("PerDownloadLimit", "must be >= 0, got %d", s.PerDownloadLimit)
	}
	switch s.ExistingPolicy {
	case domain.PolicyRename, domain.PolicyOverwrite, domain.PolicyFail:
	default:
		bad("ExistingPolicy", "unknown policy %q", string(s.ExistingPolicy))
	}
	switch s.Theme {
	case ThemeSystem, ThemeLight, ThemeDark:
	default:
		bad("Theme", "unknown theme %q", string(s.Theme))
	}
	return errs
}

// Validate returns an errors.Join of FieldError for every invalid value.
func (s Settings) Validate() error {
	var errs []error
	for _, e := range s.fieldErrors() {
		errs = append(errs, e)
	}
	return errors.Join(errs...)
}

// Apply overlays the engine-relevant settings onto c.
func (s Settings) Apply(c config.Config) config.Config {
	c.DownloadDir = s.DownloadDir
	c.MaxConnections = s.MaxConnections
	c.GlobalRateLimit = s.GlobalRateLimit
	c.PerDownloadRateLimit = s.PerDownloadLimit
	c.ExistingFilePolicy = s.ExistingPolicy
	return c
}
