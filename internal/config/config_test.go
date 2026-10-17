package config

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/raptordnj/swiftfetch/internal/domain"
)

func TestDefaultsMatchSpec(t *testing.T) {
	c := Default()
	checks := []struct {
		name      string
		got, want any
	}{
		{"MaxConnections", c.MaxConnections, 8},
		{"GlobalMaxConnections", c.GlobalMaxConnections, 16},
		{"InitialConnections", c.InitialConnections, 0},
		{"AdaptiveConcurrency", c.AdaptiveConcurrency, true},
		{"MinMultipartSize", c.MinMultipartSize, int64(8 << 20)},
		{"ChunkSizeMin", c.ChunkSizeMin, int64(1 << 20)},
		{"ChunkSizeMax", c.ChunkSizeMax, int64(64 << 20)},
		{"GlobalRateLimit", c.GlobalRateLimit, int64(0)},
		{"PerDownloadRateLimit", c.PerDownloadRateLimit, int64(0)},
		{"RetryBaseDelay", c.RetryBaseDelay, 500 * time.Millisecond},
		{"RetryMaxDelay", c.RetryMaxDelay, 60 * time.Second},
		{"RetryFactor", c.RetryFactor, 2.0},
		{"MaxSegmentAttempts", c.MaxSegmentAttempts, 8},
		{"TaskRetryBudget", c.TaskRetryBudget, 50},
		{"StallTimeout", c.StallTimeout, 30 * time.Second},
		{"CheckpointInterval", c.CheckpointInterval, 2 * time.Second},
		{"CheckpointBytes", c.CheckpointBytes, int64(16 << 20)},
		{"ProbeTimeout", c.ProbeTimeout, 15 * time.Second},
		{"OfflineWaitLimit", c.OfflineWaitLimit, 24 * time.Hour},
		{"ExistingFilePolicy", c.ExistingFilePolicy, domain.PolicyRename},
		{"RestartOnRemoteChange", c.RestartOnRemoteChange, false},
		{"ConservativeMode", c.ConservativeMode, false},
		{"NetworkPolicy", c.NetworkPolicy, domain.NetAllowDirectPrivate},
		{"AllowHTTPSDowngrade", c.AllowHTTPSDowngrade, false},
		{"ProgressHz", c.ProgressHz, 4},
	}
	for _, ch := range checks {
		if ch.got != ch.want {
			t.Errorf("%s = %v, want %v", ch.name, ch.got, ch.want)
		}
	}
	if c.DownloadDir == "" || c.DataDir == "" {
		t.Errorf("dirs must be populated: %q %q", c.DownloadDir, c.DataDir)
	}
}

func TestDefaultValidates(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("Default().Validate() = %v", err)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := []struct {
		name  string
		field string
		mut   func(*Config)
	}{
		{"conns zero", "MaxConnections", func(c *Config) { c.MaxConnections = 0 }},
		{"conns 17", "MaxConnections", func(c *Config) { c.MaxConnections = 17 }},
		{"global 65", "GlobalMaxConnections", func(c *Config) { c.GlobalMaxConnections = 65 }},
		{"global 0", "GlobalMaxConnections", func(c *Config) { c.GlobalMaxConnections = 0 }},
		{"minmultipart", "MinMultipartSize", func(c *Config) { c.MinMultipartSize = 1 << 19 }},
		{"chunk order", "ChunkSizeMin", func(c *Config) { c.ChunkSizeMin = 100 << 20 }},
		{"global rate", "GlobalRateLimit", func(c *Config) { c.GlobalRateLimit = -1 }},
		{"per rate", "PerDownloadRateLimit", func(c *Config) { c.PerDownloadRateLimit = -1 }},
		{"attempts", "MaxSegmentAttempts", func(c *Config) { c.MaxSegmentAttempts = 0 }},
		{"budget", "TaskRetryBudget", func(c *Config) { c.TaskRetryBudget = 0 }},
		{"stall low", "StallTimeout", func(c *Config) { c.StallTimeout = 4 * time.Second }},
		{"stall high", "StallTimeout", func(c *Config) { c.StallTimeout = 601 * time.Second }},
		{"ckpt low", "CheckpointInterval", func(c *Config) { c.CheckpointInterval = 400 * time.Millisecond }},
		{"ckpt high", "CheckpointInterval", func(c *Config) { c.CheckpointInterval = 31 * time.Second }},
		{"ckpt bytes", "CheckpointBytes", func(c *Config) { c.CheckpointBytes = 1 << 19 }},
		{"probe zero", "ProbeTimeout", func(c *Config) { c.ProbeTimeout = 0 }},
		{"probe high", "ProbeTimeout", func(c *Config) { c.ProbeTimeout = 121 * time.Second }},
		{"offline", "OfflineWaitLimit", func(c *Config) { c.OfflineWaitLimit = 30 * time.Second }},
		{"hz zero", "ProgressHz", func(c *Config) { c.ProgressHz = 0 }},
		{"hz 31", "ProgressHz", func(c *Config) { c.ProgressHz = 31 }},
		{"initial>max", "InitialConnections", func(c *Config) { c.InitialConnections = 9 }},
		{"initial negative", "InitialConnections", func(c *Config) { c.InitialConnections = -1 }},
		{"existing policy", "ExistingFilePolicy", func(c *Config) { c.ExistingFilePolicy = "bogus" }},
		{"network policy", "NetworkPolicy", func(c *Config) { c.NetworkPolicy = "bogus" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			tc.mut(&c)
			err := c.Validate()
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Errorf("error %q does not mention %s", err, tc.field)
			}
		})
	}
}

func TestValidateAcceptsBoundaries(t *testing.T) {
	c := Default()
	c.OfflineWaitLimit = 0
	c.StallTimeout = 5 * time.Second
	c.CheckpointInterval = 500 * time.Millisecond
	c.ProbeTimeout = time.Second
	c.ProgressHz = 30
	c.InitialConnections = c.MaxConnections
	if err := c.Validate(); err != nil {
		t.Fatalf("boundary values rejected: %v", err)
	}
}

func TestDefaultDataDir(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-specific")
	}
	t.Setenv("XDG_DATA_HOME", "/xdg/data")
	if got, want := DefaultDataDir(), filepath.Join("/xdg/data", "swiftfetch"); got != want {
		t.Errorf("DefaultDataDir = %q, want %q", got, want)
	}
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", "/home/u")
	if got, want := DefaultDataDir(), filepath.Join("/home/u", ".local", "share", "swiftfetch"); got != want {
		t.Errorf("fallback DefaultDataDir = %q, want %q", got, want)
	}
}

func TestDefaultDownloadDir(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-specific")
	}
	t.Setenv("XDG_DOWNLOAD_DIR", "/dl")
	if got := DefaultDownloadDir(); got != "/dl" {
		t.Errorf("got %q", got)
	}
	t.Setenv("XDG_DOWNLOAD_DIR", "")
	t.Setenv("HOME", "/home/u")
	if got, want := DefaultDownloadDir(), filepath.Join("/home/u", "Downloads"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
