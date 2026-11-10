package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeEnv(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "env")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadFromLegacyEnvFile(t *testing.T) {
	var cfg Config
	path := writeEnv(t, "FIREWALL_ID=fw_abc\nPROJECT_ID=proj_123\nPUBLIC_IP=203.0.113.5\n")
	if err := loadFromLegacyEnvFile(&cfg, path); err != nil {
		t.Fatalf("loadFromLegacyEnvFile: %v", err)
	}
	if cfg.Latitude.FirewallID != "fw_abc" || cfg.Latitude.ProjectID != "proj_123" || cfg.Latitude.PublicIP != "203.0.113.5" {
		t.Errorf("unexpected config: %+v", cfg.Latitude)
	}
}

// A newline smuggled into a value used to add a second FIREWALL_ID line, and
// the last line won.
func TestLoadFromLegacyEnvFile_RejectsRepeatedKey(t *testing.T) {
	var cfg Config
	path := writeEnv(t, "FIREWALL_ID=fw_abc\nPROJECT_ID=proj_123\nFIREWALL_ID=fw_other\n")
	err := loadFromLegacyEnvFile(&cfg, path)
	if err == nil || !strings.Contains(err.Error(), "FIREWALL_ID is set more than once") {
		t.Fatalf("expected a repeated-key error, got %v", err)
	}
}

func TestLoadFromLegacyEnvFile_MissingFile(t *testing.T) {
	var cfg Config
	if err := loadFromLegacyEnvFile(&cfg, filepath.Join(t.TempDir(), "absent")); err != nil {
		t.Fatalf("a missing file must be skipped, got %v", err)
	}
}

func TestValidateConfig_IDs(t *testing.T) {
	cases := []struct {
		project, firewall string
		ok                bool
	}{
		{"proj_123", "fw_abc", true},
		{"proj-1.a_B", "fw.9-x", true},
		{"p1;touch /tmp/x", "fw_abc", false},
		{"proj_123", "fw$(id)", false},
		{"proj_123", "fw`id`", false},
		{"proj_123\nFIREWALL_ID=x", "fw_abc", false},
		{"proj 123", "fw_abc", false},
		{strings.Repeat("a", 129), "fw_abc", false},
	}
	for _, tc := range cases {
		cfg := &Config{}
		cfg.Agent.Interval = "30s"
		cfg.Latitude.ProjectID = tc.project
		cfg.Latitude.FirewallID = tc.firewall
		err := validateConfig(cfg)
		if (err == nil) != tc.ok {
			t.Errorf("validateConfig(project=%q, firewall=%q) = %v, want ok=%v", tc.project, tc.firewall, err, tc.ok)
		}
	}
}
