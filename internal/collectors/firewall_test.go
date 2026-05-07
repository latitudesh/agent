package collectors

import (
	"testing"

	"github.com/sirupsen/logrus"
)

func TestFirewallRuleString(t *testing.T) {
	cases := []struct {
		name string
		rule FirewallRule
		want string
	}{
		{
			name: "strips /32 from IPv4 host",
			rule: FirewallRule{From: "8.8.8.8/32", Protocol: "tcp", Port: "8081"},
			want: "From: 8.8.8.8, Protocol: tcp, Port: 8081",
		},
		{
			name: "strips /128 from IPv6 host",
			rule: FirewallRule{From: "2001:db8::1/128", Protocol: "tcp", Port: "443"},
			want: "From: 2001:db8::1, Protocol: tcp, Port: 443",
		},
		{
			name: "preserves real IPv4 CIDR",
			rule: FirewallRule{From: "10.20.0.0/24", Protocol: "tcp", Port: "8080"},
			want: "From: 10.20.0.0/24, Protocol: tcp, Port: 8080",
		},
		{
			name: "preserves bare IPv4 host",
			rule: FirewallRule{From: "1.1.1.1", Protocol: "tcp", Port: "8080"},
			want: "From: 1.1.1.1, Protocol: tcp, Port: 8080",
		},
		{
			name: "lowercases uppercase protocol",
			rule: FirewallRule{From: "1.1.1.1", Protocol: "TCP", Port: "8080"},
			want: "From: 1.1.1.1, Protocol: tcp, Port: 8080",
		},
		{
			name: "empty fields default to any",
			rule: FirewallRule{},
			want: "From: any, Protocol: any, Port: any",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.rule.String()
			if got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSyncDiff_StableForCIDRHostRules(t *testing.T) {
	fc := NewFirewallCollector("ufw", false, logrus.New())

	apiRules := []FirewallRule{
		{From: "8.8.8.8/32", Protocol: "TCP", Port: "8081"},
		{From: "1.1.1.1", Protocol: "TCP", Port: "8080"},
		{From: "10.20.0.0/24", Protocol: "TCP", Port: "8080"},
	}

	ufwOutput := `Status: active

To                         Action      From
--                         ------      ----
8081/tcp                   ALLOW       8.8.8.8
8080/tcp                   ALLOW       1.1.1.1
8080/tcp                   ALLOW       10.20.0.0/24
`

	currentRules, err := fc.parseUFWRules(ufwOutput)
	if err != nil {
		t.Fatalf("parseUFWRules: %v", err)
	}

	apiSet := fc.rulesToStringSet(apiRules)
	currentSet := fc.rulesToStringSet(currentRules)
	toAdd := fc.findRulesToAdd(currentSet, apiSet, apiRules)
	toRemove := fc.findRulesToRemove(currentSet, apiSet, currentRules)

	if len(toAdd) != 0 {
		t.Errorf("expected no rules to add, got %d: %+v", len(toAdd), toAdd)
	}
	if len(toRemove) != 0 {
		t.Errorf("expected no rules to remove, got %d: %+v", len(toRemove), toRemove)
	}
}

func TestSyncDiff_StableWithCaseSensitiveProtocol(t *testing.T) {
	fc := NewFirewallCollector("ufw", true, logrus.New())

	apiRules := []FirewallRule{
		{From: "1.1.1.1", Protocol: "TCP", Port: "8080"},
	}
	ufwOutput := "Status: active\n\nTo                         Action      From\n--                         ------      ----\n8080/tcp                   ALLOW       1.1.1.1\n"

	currentRules, err := fc.parseUFWRules(ufwOutput)
	if err != nil {
		t.Fatalf("parseUFWRules: %v", err)
	}

	apiSet := fc.rulesToStringSet(apiRules)
	currentSet := fc.rulesToStringSet(currentRules)
	toAdd := fc.findRulesToAdd(currentSet, apiSet, apiRules)
	toRemove := fc.findRulesToRemove(currentSet, apiSet, currentRules)

	if len(toAdd) != 0 || len(toRemove) != 0 {
		t.Errorf("expected stable diff with case-sensitive mode, got toAdd=%v toRemove=%v", toAdd, toRemove)
	}
}
