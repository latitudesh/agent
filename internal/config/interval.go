package config

import (
	"fmt"
	"time"
)

// SyncInterval returns the agent sync interval. It must be a positive Go
// duration: time.NewTicker panics on zero or a negative one.
func (c *Config) SyncInterval() (time.Duration, error) {
	interval, err := time.ParseDuration(c.Agent.Interval)
	if err != nil {
		return 0, fmt.Errorf("invalid interval %q: %w", c.Agent.Interval, err)
	}
	if interval <= 0 {
		return 0, fmt.Errorf("invalid interval %q: must be greater than zero", c.Agent.Interval)
	}
	return interval, nil
}
