package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config represents the agent configuration
type Config struct {
	Agent    AgentConfig    `yaml:"agent"`
	Latitude LatitudeConfig `yaml:"latitude"`
	Firewall FirewallConfig `yaml:"firewall"`
	Health   HealthConfig   `yaml:"health"`
	Logging  LoggingConfig  `yaml:"logging"`
}

// AgentConfig contains general agent settings
type AgentConfig struct {
	Interval string `yaml:"interval" default:"30s"`
	LogLevel string `yaml:"log_level" default:"info"`
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
	TempFile      string `yaml:"temp_file" default:"/tmp/lsh_firewall_temp.json"`
	OutputFile    string `yaml:"output_file" default:"/tmp/lsh_firewall.json"`
}

// LoggingConfig contains logging configuration
type LoggingConfig struct {
	Level  string `yaml:"level" default:"info"`
	Format string `yaml:"format" default:"text"`
}

// HealthConfig contains health monitoring settings
type HealthConfig struct {
	Enabled           bool   `yaml:"enabled" default:"true"`
	Interval          string `yaml:"interval" default:"60s"`
	Detailed          bool   `yaml:"detailed" default:"true"`
	SmartMonitoring   bool   `yaml:"smart_monitoring" default:"true"`
	IPMIMonitoring    bool   `yaml:"ipmi_monitoring" default:"true"`
	ConnectivityTests bool   `yaml:"connectivity_tests" default:"true"`
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
	config.Firewall.TempFile = "/tmp/lsh_firewall_temp.json"
	config.Firewall.OutputFile = "/tmp/lsh_firewall.json"
	config.Health.Enabled = true
	config.Health.Interval = "60s"
	config.Health.Detailed = true
	config.Health.SmartMonitoring = true
	config.Health.IPMIMonitoring = true
	config.Health.ConnectivityTests = true
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

// loadFromLegacyEnv loads configuration from legacy /etc/lsh-agent/env file
func loadFromLegacyEnv(config *Config) error {
	envFile := "/etc/lsh-agent/env"
	if _, err := os.Stat(envFile); os.IsNotExist(err) {
		return nil // File doesn't exist, skip
	}

	data, err := os.ReadFile(envFile)
	if err != nil {
		return err
	}

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
	if config.Latitude.ProjectID == "" {
		return fmt.Errorf("PROJECT_ID is required")
	}
	if config.Latitude.FirewallID == "" {
		return fmt.Errorf("FIREWALL_ID is required")
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

// DefaultConfigPath returns the default configuration file path
func DefaultConfigPath() string {
	return filepath.Join("/etc", "lsh-agent", "config.yaml")
}
