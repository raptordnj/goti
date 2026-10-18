package settings

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/raptordnj/goti/internal/domain"
)

const key = "app"

// KV is the key/value store backing the repo.
type KV interface {
	GetSetting(ctx context.Context, key string) (string, error)
	PutSetting(ctx context.Context, key, value string) error
}

// Repo loads and saves Settings.
type Repo struct {
	kv  KV
	log *slog.Logger
}

// NewRepo returns a Repo over kv.
func NewRepo(kv KV, log *slog.Logger) *Repo { return &Repo{kv: kv, log: log} }

// Load returns the stored settings. Missing or unreadable data yields defaults;
// invalid fields are reset to their defaults with a warning. It never fails.
func (r *Repo) Load(ctx context.Context) Settings {
	s := Default()
	raw, err := r.kv.GetSetting(ctx, key)
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			r.log.Warn("settings: read failed, using defaults", "err", err)
		}
		return s
	}
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		r.log.Warn("settings: corrupt, using defaults", "err", err)
		return Default()
	}
	def := Default()
	for _, fe := range s.fieldErrors() {
		r.log.Warn("settings: invalid stored value, using default", "field", fe.Field, "err", fe.Msg)
		switch fe.Field {
		case "MaxSimultaneous":
			s.MaxSimultaneous = def.MaxSimultaneous
		case "DownloadDir":
			s.DownloadDir = def.DownloadDir
		case "MaxConnections":
			s.MaxConnections = def.MaxConnections
		case "GlobalRateLimit":
			s.GlobalRateLimit = def.GlobalRateLimit
		case "PerDownloadLimit":
			s.PerDownloadLimit = def.PerDownloadLimit
		case "ExistingPolicy":
			s.ExistingPolicy = def.ExistingPolicy
		case "Theme":
			s.Theme = def.Theme
		}
	}
	return s
}

// Save validates and stores s.
func (r *Repo) Save(ctx context.Context, s Settings) error {
	if err := s.Validate(); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return r.kv.PutSetting(ctx, key, string(b))
}
