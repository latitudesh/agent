package collectors

import (
	"context"
	"strings"
	"testing"
)

// TestSyncFirewallRules_RefusesWhenUFWInactive checks that a sync against an
// inactive ufw fails instead of adding every dashboard rule again and
// reporting them as applied. `ufw status` prints only this line while ufw is
// disabled, whatever rules its files hold.
func TestSyncFirewallRules_RefusesWhenUFWInactive(t *testing.T) {
	fc := newTestCollector(false, true)
	var commands [][]string
	fc.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if len(args) == 1 && args[0] == "status" {
			return []byte("Status: inactive\n"), nil
		}
		commands = append(commands, append([]string{name}, args...))
		return nil, nil
	}

	api := `{"firewall":{"rules":[
		{"from":"any","to":"any","protocol":"tcp","port":"22"},
		{"from":"10.0.0.0/8","to":"any","protocol":"tcp","port":"8080"}
	]}}`
	err := fc.SyncFirewallRules(context.Background(), api)
	if err == nil || !strings.Contains(err.Error(), "ufw is inactive") {
		t.Errorf("SyncFirewallRules error = %v, want one saying ufw is inactive", err)
	}
	if len(commands) != 0 {
		t.Errorf("expected only `ufw status`, got %q", commands)
	}
}
