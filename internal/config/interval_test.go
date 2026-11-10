package config

import (
	"strings"
	"testing"
	"time"
)

func TestParseInterval(t *testing.T) {
	valid := map[string]time.Duration{
		"30s":    30 * time.Second,
		"1m30s":  90 * time.Second,
		"1s":     time.Second,
		" 5m ":   5 * time.Minute,
		"1h":     time.Hour,
		"1500ms": 1500 * time.Millisecond,
	}
	for in, want := range valid {
		got, err := parseInterval(in)
		if err != nil || got != want {
			t.Errorf("parseInterval(%q) = %v, %v; want %v", in, got, err, want)
		}
	}

	invalid := map[string]string{
		"30":    "use a duration with a unit",
		"":      "use a duration with a unit",
		"soon":  "use a duration with a unit",
		"0s":    "must be at least 1s",
		"0":     "must be at least 1s",
		"-30s":  "must be at least 1s",
		"30ms":  "must be at least 1s",
		"999ms": "must be at least 1s",
	}
	for in, want := range invalid {
		_, err := parseInterval(in)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("parseInterval(%q) error = %v, want it to contain %q", in, err, want)
		}
	}
}

func TestValidateConfig_Interval(t *testing.T) {
	cfg := func(interval string) *Config {
		c := &Config{}
		c.Agent.Interval = interval
		c.Latitude.ProjectID = "proj_test"
		c.Latitude.FirewallID = "fw_test"
		return c
	}

	ok := cfg("45s")
	if err := validateConfig(ok); err != nil {
		t.Fatalf("validateConfig: %v", err)
	}
	if ok.Agent.SyncInterval != 45*time.Second {
		t.Errorf("SyncInterval = %v, want 45s", ok.Agent.SyncInterval)
	}

	// A config error, not a panic in time.NewTicker once the agent is up.
	if err := validateConfig(cfg("0s")); err == nil {
		t.Error("validateConfig accepted a zero interval")
	}
}

func TestLoadConfig_IntervalFromEnv(t *testing.T) {
	t.Setenv("PROJECT_ID", "proj_test")
	t.Setenv("FIREWALL_ID", "fw_test")
	t.Setenv("FIREWALL_ENABLED", "false")

	t.Setenv("AGENT_INTERVAL", "2m")
	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Agent.SyncInterval != 2*time.Minute {
		t.Errorf("SyncInterval = %v, want 2m", cfg.Agent.SyncInterval)
	}

	t.Setenv("AGENT_INTERVAL", "30")
	if _, err := LoadConfig(""); err == nil || !strings.Contains(err.Error(), `invalid interval "30"`) {
		t.Errorf("LoadConfig error = %v, want an invalid interval error", err)
	}
}
