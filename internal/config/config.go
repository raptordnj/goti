// Package config holds engine configuration, its defaults (spec §13) and
// validation of user-supplied values.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/raptordnj/goti/internal/domain"
)

// Config is the engine configuration. Zero connection counts mean "auto"
// where noted. Validate guards user-supplied values only; it is never called
// implicitly by Default.
type Config struct {
	MaxConnections       int // per download, 1-16
	GlobalMaxConnections int // 1-64
	InitialConnections   int // 0 = auto, else 1-MaxConnections
	AdaptiveConcurrency  bool

	MinMultipartSize int64
	ChunkSizeMin     int64
	ChunkSizeMax     int64

	GlobalRateLimit      int64 // bytes/s, 0 = unlimited
	PerDownloadRateLimit int64 // bytes/s, 0 = unlimited

	RetryBaseDelay     time.Duration
	RetryMaxDelay      time.Duration
	RetryFactor        float64
	MaxSegmentAttempts int
	TaskRetryBudget    int

	StallTimeout       time.Duration
	CheckpointInterval time.Duration
	CheckpointBytes    int64
	ProbeTimeout       time.Duration
	OfflineWaitLimit   time.Duration // 0 = wait forever

	ExistingFilePolicy    domain.ExistingFilePolicy
	RestartOnRemoteChange bool
	ConservativeMode      bool
	NetworkPolicy         domain.NetworkPolicy
	AllowHTTPSDowngrade   bool

	ProgressHz  int
	DownloadDir string
	DataDir     string
}

// FieldError reports an invalid configuration field.
type FieldError struct {
	Field string
	Msg   string
}

func (e FieldError) Error() string { return "config: " + e.Field + ": " + e.Msg }

// Default returns the spec §13 defaults.
func Default() Config {
	return Config{
		MaxConnections:       8,
		GlobalMaxConnections: 16,
		AdaptiveConcurrency:  true,
		MinMultipartSize:     8 << 20,
		ChunkSizeMin:         1 << 20,
		ChunkSizeMax:         64 << 20,
		RetryBaseDelay:       500 * time.Millisecond,
		RetryMaxDelay:        60 * time.Second,
		RetryFactor:          2,
		MaxSegmentAttempts:   8,
		TaskRetryBudget:      50,
		StallTimeout:         30 * time.Second,
		CheckpointInterval:   2 * time.Second,
		CheckpointBytes:      16 << 20,
		ProbeTimeout:         15 * time.Second,
		OfflineWaitLimit:     24 * time.Hour,
		ExistingFilePolicy:   domain.PolicyRename,
		NetworkPolicy:        domain.NetAllowDirectPrivate,
		ProgressHz:           4,
		DownloadDir:          DefaultDownloadDir(),
		DataDir:              DefaultDataDir(),
	}
}

// Validate returns an errors.Join of FieldError for every out-of-range value.
func (c Config) Validate() error {
	var errs []error
	bad := func(field, format string, a ...any) {
		errs = append(errs, FieldError{Field: field, Msg: fmt.Sprintf(format, a...)})
	}
	if c.MaxConnections < 1 || c.MaxConnections > 16 {
		bad("MaxConnections", "must be 1-16, got %d", c.MaxConnections)
	}
	if c.GlobalMaxConnections < 1 || c.GlobalMaxConnections > 64 {
		bad("GlobalMaxConnections", "must be 1-64, got %d", c.GlobalMaxConnections)
	}
	if c.InitialConnections < 0 || c.InitialConnections > c.MaxConnections {
		bad("InitialConnections", "must be 0 (auto) or 1-MaxConnections (%d), got %d", c.MaxConnections, c.InitialConnections)
	}
	if c.MinMultipartSize < 1<<20 {
		bad("MinMultipartSize", "must be >= 1 MiB, got %d", c.MinMultipartSize)
	}
	if c.ChunkSizeMin > c.ChunkSizeMax {
		bad("ChunkSizeMin", "must be <= ChunkSizeMax (%d), got %d", c.ChunkSizeMax, c.ChunkSizeMin)
	}
	if c.GlobalRateLimit < 0 {
		bad("GlobalRateLimit", "must be >= 0, got %d", c.GlobalRateLimit)
	}
	if c.PerDownloadRateLimit < 0 {
		bad("PerDownloadRateLimit", "must be >= 0, got %d", c.PerDownloadRateLimit)
	}
	if c.MaxSegmentAttempts < 1 {
		bad("MaxSegmentAttempts", "must be >= 1, got %d", c.MaxSegmentAttempts)
	}
	if c.TaskRetryBudget < 1 {
		bad("TaskRetryBudget", "must be >= 1, got %d", c.TaskRetryBudget)
	}
	if c.StallTimeout < 5*time.Second || c.StallTimeout > 600*time.Second {
		bad("StallTimeout", "must be 5s-600s, got %s", c.StallTimeout)
	}
	if c.CheckpointInterval < 500*time.Millisecond || c.CheckpointInterval > 30*time.Second {
		bad("CheckpointInterval", "must be 500ms-30s, got %s", c.CheckpointInterval)
	}
	if c.CheckpointBytes < 1<<20 {
		bad("CheckpointBytes", "must be >= 1 MiB, got %d", c.CheckpointBytes)
	}
	if c.ProbeTimeout < time.Second || c.ProbeTimeout > 120*time.Second {
		bad("ProbeTimeout", "must be 1s-120s, got %s", c.ProbeTimeout)
	}
	if c.OfflineWaitLimit != 0 && c.OfflineWaitLimit < time.Minute {
		bad("OfflineWaitLimit", "must be 0 (forever) or >= 1m, got %s", c.OfflineWaitLimit)
	}
	switch c.ExistingFilePolicy {
	case domain.PolicyRename, domain.PolicyOverwrite, domain.PolicyFail:
	default:
		bad("ExistingFilePolicy", "unknown policy %q", string(c.ExistingFilePolicy))
	}
	switch c.NetworkPolicy {
	case domain.NetAllowDirectPrivate, domain.NetAllowAll, domain.NetBlockPrivate:
	default:
		bad("NetworkPolicy", "unknown policy %q", string(c.NetworkPolicy))
	}
	if c.ProgressHz < 1 || c.ProgressHz > 30 {
		bad("ProgressHz", "must be 1-30, got %d", c.ProgressHz)
	}
	return errors.Join(errs...)
}

// DefaultDownloadDir returns $XDG_DOWNLOAD_DIR or ~/Downloads.
func DefaultDownloadDir() string {
	if d := os.Getenv("XDG_DOWNLOAD_DIR"); d != "" {
		return d
	}
	return filepath.Join(homeDir(), "Downloads")
}

// DefaultDataDir returns the per-OS data directory (spec §13).
func DefaultDataDir() string {
	switch runtime.GOOS {
	case "windows":
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			base = filepath.Join(homeDir(), "AppData", "Local")
		}
		return filepath.Join(base, "Goti")
	case "darwin":
		return filepath.Join(homeDir(), "Library", "Application Support", "Goti")
	default:
		if x := os.Getenv("XDG_DATA_HOME"); x != "" {
			return filepath.Join(x, "goti")
		}
		return filepath.Join(homeDir(), ".local", "share", "goti")
	}
}

// LegacyDataDir returns the pre-rebrand (SwiftFetch) per-OS data directory,
// used only as a migration source.
func LegacyDataDir() string {
	nw := DefaultDataDir()
	return filepath.Join(filepath.Dir(nw), legacyDirName())
}

func legacyDirName() string {
	if runtime.GOOS == "linux" || (runtime.GOOS != "windows" && runtime.GOOS != "darwin") {
		return "swiftfetch"
	}
	return "SwiftFetch"
}

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "."
}
