package settings_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/raptordnj/goti/internal/config"
	"github.com/raptordnj/goti/internal/domain"
	"github.com/raptordnj/goti/internal/persistence"
	"github.com/raptordnj/goti/internal/settings"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func repo(t *testing.T) (*settings.Repo, *persistence.DB) {
	t.Helper()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return settings.NewRepo(db, quiet()), db
}

func TestDefaultValid(t *testing.T) {
	d := settings.Default()
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if d.MaxSimultaneous != 3 || !d.ResumeOnStartup || d.MaxConnections != 8 ||
		d.ExistingPolicy != domain.PolicyRename || d.Theme != settings.ThemeSystem ||
		!d.CloseToTray || d.TrayNoticeShown || d.DownloadDir != config.DefaultDownloadDir() {
		t.Fatalf("defaults: %+v", d)
	}
}

func TestValidateRanges(t *testing.T) {
	cases := map[string]func(*settings.Settings){
		"MaxSimultaneous":  func(s *settings.Settings) { s.MaxSimultaneous = 0 },
		"MaxSimultaneous ": func(s *settings.Settings) { s.MaxSimultaneous = 11 },
		"MaxConnections":   func(s *settings.Settings) { s.MaxConnections = 17 },
		"GlobalRateLimit":  func(s *settings.Settings) { s.GlobalRateLimit = -1 },
		"PerDownloadLimit": func(s *settings.Settings) { s.PerDownloadLimit = -1 },
		"Theme":            func(s *settings.Settings) { s.Theme = "neon" },
		"ExistingPolicy":   func(s *settings.Settings) { s.ExistingPolicy = "x" },
		"DownloadDir":      func(s *settings.Settings) { s.DownloadDir = "" },
	}
	for want, mut := range cases {
		s := settings.Default()
		mut(&s)
		err := s.Validate()
		var fe config.FieldError
		if !errors.As(err, &fe) || fe.Field != trim(want) {
			t.Errorf("%q: err = %v", want, err)
		}
	}
}

func trim(s string) string {
	for s != "" && s[len(s)-1] == ' ' {
		s = s[:len(s)-1]
	}
	return s
}

func TestApply(t *testing.T) {
	s := settings.Default()
	s.DownloadDir = "/x"
	s.MaxConnections = 5
	s.GlobalRateLimit = 100
	s.PerDownloadLimit = 50
	s.ExistingPolicy = domain.PolicyFail
	c := s.Apply(config.Default())
	if c.DownloadDir != "/x" || c.MaxConnections != 5 || c.GlobalRateLimit != 100 ||
		c.PerDownloadRateLimit != 50 || c.ExistingFilePolicy != domain.PolicyFail {
		t.Fatalf("apply: %+v", c)
	}
}

func TestRepoRoundTrip(t *testing.T) {
	r, _ := repo(t)
	ctx := context.Background()
	s := settings.Default()
	s.Theme = settings.ThemeDark
	s.MaxSimultaneous = 7
	s.TrayNoticeShown = true
	if err := r.Save(ctx, s); err != nil {
		t.Fatal(err)
	}
	if got := r.Load(ctx); got != s {
		t.Fatalf("got %+v want %+v", got, s)
	}
	s.MaxSimultaneous = 0
	if err := r.Save(ctx, s); err == nil {
		t.Fatal("invalid save accepted")
	}
}

func TestSettingsLoadTolerant(t *testing.T) {
	r, db := repo(t)
	ctx := context.Background()
	if err := db.PutSetting(ctx, "app", `{"MaxSimultaneous":99,"Theme":"dark","Future":1}`); err != nil {
		t.Fatal(err)
	}
	got := r.Load(ctx)
	if got.MaxSimultaneous != 3 || got.Theme != settings.ThemeDark {
		t.Fatalf("got %+v", got)
	}
}

func TestLoadMissingDefaults(t *testing.T) {
	r, db := repo(t)
	if got := r.Load(context.Background()); got != settings.Default() {
		t.Fatalf("got %+v", got)
	}
	_ = db.PutSetting(context.Background(), "app", "not json")
	if got := r.Load(context.Background()); got != settings.Default() {
		t.Fatalf("corrupt: %+v", got)
	}
}

func TestReduceMotionDefaultAndJSON(t *testing.T) {
	if settings.Default().ReduceMotion {
		t.Fatal("default must be false")
	}
	s := settings.Default()
	s.ReduceMotion = true
	b, err := json.Marshal(s)
	if err != nil || !strings.Contains(string(b), `"ReduceMotion":true`) {
		t.Fatalf("%s %v", b, err)
	}
	var back settings.Settings
	if err := json.Unmarshal(b, &back); err != nil || !back.ReduceMotion {
		t.Fatalf("round trip %+v %v", back, err)
	}
}
