package config

import (
	"strings"
	"testing"
	"time"
)

func TestSyncInterval(t *testing.T) {
	for _, tc := range []struct {
		interval string
		want     time.Duration
		wantErr  string
	}{
		{interval: "30s", want: 30 * time.Second},
		{interval: "1m30s", want: 90 * time.Second},
		{interval: "0s", wantErr: "must be greater than zero"},
		{interval: "0", wantErr: "must be greater than zero"},
		{interval: "-30s", wantErr: "must be greater than zero"},
		{interval: "30", wantErr: "missing unit"},
		{interval: "", wantErr: "invalid duration"},
	} {
		t.Run(tc.interval, func(t *testing.T) {
			cfg := &Config{Agent: AgentConfig{Interval: tc.interval}}
			got, err := cfg.SyncInterval()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("SyncInterval() error = %v, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("SyncInterval() error = %v", err)
			}
			if got != tc.want {
				t.Errorf("SyncInterval() = %s, want %s", got, tc.want)
			}
		})
	}
}
