package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config represents the agent configuration
type Config struct {
	Agent    AgentConfig    `yaml:"agent"`
	Latitude LatitudeConfig `yaml:"latitude"`
	Firewall FirewallConfig `yaml:"firewall"`
	Logging  LoggingConfig  `yaml:"logging"`
}

// AgentConfig contains general agent settings
type AgentConfig struct {
	Interval string `yaml:"interval" default:"30s"`
	LogLevel string `yaml:"log_level" default:"info"`
	// SyncInterval is Interval parsed, set by LoadConfig once it is valid.
	SyncInterval time.Duration `yaml:"-"`
}

// LatitudeConfig contains Latitude.sh API configuration
type LatitudeConfig struct {
	APIEndpoint string `yaml:"api_endpoint" default:"https://api.latitude.sh/agent/ping"`
	BearerToken string `yaml:"bearer_token"`
	ProjectID   string `yaml:"project_id"`
	FirewallID  string `yaml:"firewall_id"`
	PublicIP    string `yaml:"public_ip"`
}

// FirewallConfig contains firewall-specific settings
type FirewallConfig struct {
	Enabled       bool   `yaml:"enabled" default:"true"`
	UFWBinary     string `yaml:"ufw_binary" default:"/usr/sbin/ufw"`
	CaseSensitive bool   `yaml:"case_sensitive" default:"false"`
}

// LoggingConfig contains logging configuration
type LoggingConfig struct {
	Level  string `yaml:"level" default:"info"`
	Format string `yaml:"format" default:"text"`
}

// LoadConfig loads configuration from file and environment variables
func LoadConfig(configPath string) (*Config, error) {
	config := &Config{}

	// Set defaults
	config.Agent.Interval = "30s"
	config.Agent.LogLevel = "info"
	config.Latitude.APIEndpoint = "https://api.latitude.sh/agent/ping"
	config.Firewall.Enabled = true
	config.Firewall.UFWBinary = "/usr/sbin/ufw"
	config.Firewall.CaseSensitive = false
	config.Logging.Level = "info"
	config.Logging.Format = "text"

	// Load from YAML file if it exists
	if configPath != "" {
		if err := loadFromYAML(config, configPath); err != nil {
			return nil, fmt.Errorf("failed to load YAML config: %w", err)
		}
	}

	// Override with legacy environment file if it exists
	if err := loadFromLegacyEnv(config); err != nil {
		return nil, fmt.Errorf("failed to load legacy env config: %w", err)
	}

	// Override with environment variables
	loadFromEnv(config)

	// Validate required fields
	if err := validateConfig(config); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return config, nil
}

// loadFromYAML loads configuration from YAML file
func loadFromYAML(config *Config, configPath string) error {
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil // File doesn't exist, skip
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}

	return yaml.Unmarshal(data, config)
}

// legacyEnvFile is the KEY=value file install.sh writes.
const legacyEnvFile = "/etc/lsh-agent/env"

// loadFromLegacyEnv loads configuration from legacy /etc/lsh-agent/env file
func loadFromLegacyEnv(config *Config) error {
	return loadFromLegacyEnvFile(config, legacyEnvFile)
}

// loadFromLegacyEnvFile reads a KEY=value env file. A key the agent uses may
// appear only once: install.sh writes each one once, so a repeated key means
// the file was edited or built from unvalidated input, and silently letting
// the last line win could switch the firewall the agent applies.
func loadFromLegacyEnvFile(config *Config, envFile string) error {
	if _, err := os.Stat(envFile); os.IsNotExist(err) {
		return nil // File doesn't exist, skip
	}

	data, err := os.ReadFile(envFile)
	if err != nil {
		return err
	}

	seen := make(map[string]bool)
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.Trim(strings.TrimSpace(parts[1]), `"'`)

		switch key {
		case "PROJECT_ID", "FIREWALL_ID", "PUBLIC_IP":
			if seen[key] {
				return fmt.Errorf("%s: %s is set more than once", envFile, key)
			}
			seen[key] = true
		}

		switch key {
		case "PROJECT_ID":
			config.Latitude.ProjectID = value
		case "FIREWALL_ID":
			config.Latitude.FirewallID = value
		case "PUBLIC_IP":
			config.Latitude.PublicIP = value
		}
	}

	return nil
}

// loadFromEnv loads configuration from environment variables
func loadFromEnv(config *Config) {
	if val := os.Getenv("LATITUDESH_AUTH_TOKEN"); val != "" {
		config.Latitude.BearerToken = val
	}
	if val := os.Getenv("PROJECT_ID"); val != "" {
		config.Latitude.ProjectID = val
	}
	if val := os.Getenv("FIREWALL_ID"); val != "" {
		config.Latitude.FirewallID = val
	}
	if val := os.Getenv("PUBLIC_IP"); val != "" {
		config.Latitude.PublicIP = val
	}
	if val := os.Getenv("AGENT_INTERVAL"); val != "" {
		config.Agent.Interval = val
	}
	if val := os.Getenv("LOG_LEVEL"); val != "" {
		config.Agent.LogLevel = val
		config.Logging.Level = val
	}
	if val := os.Getenv("UFW_BINARY"); val != "" {
		config.Firewall.UFWBinary = val
	}
	if val := os.Getenv("FIREWALL_ENABLED"); val != "" {
		if enabled, err := strconv.ParseBool(val); err == nil {
			config.Firewall.Enabled = enabled
		}
	}
}

// validateConfig validates the loaded configuration
func validateConfig(config *Config) error {
	interval, err := parseInterval(config.Agent.Interval)
	if err != nil {
		return err
	}
	config.Agent.SyncInterval = interval

	if config.Latitude.ProjectID == "" {
		return fmt.Errorf("PROJECT_ID is required")
	}
	if config.Latitude.FirewallID == "" {
		return fmt.Errorf("FIREWALL_ID is required")
	}
	if !validID(config.Latitude.ProjectID) {
		return fmt.Errorf("PROJECT_ID %q may only contain letters, digits, '.', '_' and '-'", config.Latitude.ProjectID)
	}
	if !validID(config.Latitude.FirewallID) {
		return fmt.Errorf("FIREWALL_ID %q may only contain letters, digits, '.', '_' and '-'", config.Latitude.FirewallID)
	}
	// Bearer token is optional since /ping API is unauthenticated

	// Validate UFW binary exists
	if config.Firewall.Enabled {
		if _, err := os.Stat(config.Firewall.UFWBinary); os.IsNotExist(err) {
			return fmt.Errorf("UFW binary not found at %s", config.Firewall.UFWBinary)
		}
	}

	return nil
}

// validID reports whether a firewall or project ID has the shape the dashboard
// issues (e.g. fw_..., proj_...): letters, digits, '.', '_' and '-', 1-128
// characters. install.sh enforces the same rule.
func validID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}

// minInterval is the shortest sync interval accepted. Each cycle calls the
// API, so a missing or wrong unit ("30ms" for "30s") must not turn the agent
// into a tight loop against it; zero or a negative value would also make
// time.NewTicker panic.
const minInterval = time.Second

// parseInterval parses agent.interval / AGENT_INTERVAL, a Go duration such as
// "30s" or "1m30s" of at least minInterval.
func parseInterval(value string) (time.Duration, error) {
	interval, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("invalid interval %q: use a duration with a unit, e.g. \"30s\" or \"5m\"", value)
	}
	if interval < minInterval {
		return 0, fmt.Errorf("invalid interval %q: must be at least %s", value, minInterval)
	}
	return interval, nil
}

// DefaultConfigPath returns the default configuration file path
func DefaultConfigPath() string {
	return filepath.Join("/etc", "lsh-agent", "config.yaml")
}
