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
			want: "From: 8.8.8.8, To: any, Protocol: tcp, Port: 8081",
		},
		{
			name: "strips /128 from IPv6 host",
			rule: FirewallRule{From: "2001:db8::1/128", Protocol: "tcp", Port: "443"},
			want: "From: 2001:db8::1, To: any, Protocol: tcp, Port: 443",
		},
		{
			name: "preserves real IPv4 CIDR",
			rule: FirewallRule{From: "10.20.0.0/24", Protocol: "tcp", Port: "8080"},
			want: "From: 10.20.0.0/24, To: any, Protocol: tcp, Port: 8080",
		},
		{
			name: "preserves bare IPv4 host",
			rule: FirewallRule{From: "1.1.1.1", Protocol: "tcp", Port: "8080"},
			want: "From: 1.1.1.1, To: any, Protocol: tcp, Port: 8080",
		},
		{
			name: "lowercases uppercase protocol",
			rule: FirewallRule{From: "1.1.1.1", Protocol: "TCP", Port: "8080"},
			want: "From: 1.1.1.1, To: any, Protocol: tcp, Port: 8080",
		},
		{
			name: "includes specific destination",
			rule: FirewallRule{From: "any", To: "203.51.16.0/24", Protocol: "tcp", Port: "8080"},
			want: "From: any, To: 203.51.16.0/24, Protocol: tcp, Port: 8080",
		},
		{
			name: "strips /32 from destination host",
			rule: FirewallRule{From: "any", To: "203.51.16.5/32", Protocol: "tcp", Port: "8080"},
			want: "From: any, To: 203.51.16.5, Protocol: tcp, Port: 8080",
		},
		{
			name: "empty fields default to any",
			rule: FirewallRule{},
			want: "From: any, To: any, Protocol: any, Port: any",
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

func TestParseUFWRules_CapturesDestination(t *testing.T) {
	fc := NewFirewallCollector("ufw", false, logrus.New())

	ufwOutput := `Status: active

To                         Action      From
--                         ------      ----
203.51.16.0/24 8081/tcp    ALLOW       8.8.8.8
8080/tcp                   ALLOW       1.1.1.1
`

	rules, err := fc.parseUFWRules(ufwOutput)
	if err != nil {
		t.Fatalf("parseUFWRules: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d: %+v", len(rules), rules)
	}

	if rules[0].To != "203.51.16.0/24" || rules[0].From != "8.8.8.8" || rules[0].Port != "8081" {
		t.Errorf("unexpected rule with destination: %+v", rules[0])
	}
	if rules[1].To != "any" || rules[1].From != "1.1.1.1" || rules[1].Port != "8080" {
		t.Errorf("unexpected rule without destination: %+v", rules[1])
	}
}

func TestSyncDiff_StableForDestinationRules(t *testing.T) {
	fc := NewFirewallCollector("ufw", false, logrus.New())

	apiRules := []FirewallRule{
		{From: "8.8.8.8", To: "203.51.16.0/24", Protocol: "TCP", Port: "8081"},
		{From: "1.1.1.1", To: "", Protocol: "TCP", Port: "8080"},
	}

	ufwOutput := `Status: active

To                         Action      From
--                         ------      ----
203.51.16.0/24 8081/tcp    ALLOW       8.8.8.8
8080/tcp                   ALLOW       1.1.1.1
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

func TestParseUFWRules_CapturesPortRange(t *testing.T) {
	fc := NewFirewallCollector("ufw", false, logrus.New())

	ufwOutput := `Status: active

To                         Action      From
--                         ------      ----
10.4.0.0/24 1:65535/tcp    ALLOW       10.4.0.0/24
1:65535/udp                ALLOW       172.16.0.0/13
`

	rules, err := fc.parseUFWRules(ufwOutput)
	if err != nil {
		t.Fatalf("parseUFWRules: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d: %+v", len(rules), rules)
	}
	if rules[0].To != "10.4.0.0/24" || rules[0].From != "10.4.0.0/24" || rules[0].Port != "1:65535" || rules[0].Protocol != "tcp" {
		t.Errorf("unexpected range rule with destination: %+v", rules[0])
	}
	if rules[1].To != "any" || rules[1].From != "172.16.0.0/13" || rules[1].Port != "1:65535" || rules[1].Protocol != "udp" {
		t.Errorf("unexpected range rule without destination: %+v", rules[1])
	}
}

// TestSyncDiff_StableForPortRangeRules reproduces the every-30s `ufw reload`
// loop: the API port-range rules were never recognized in `ufw status`, so the
// sync re-added them and reloaded on every tick, dropping in-flight packets.
func TestSyncDiff_StableForPortRangeRules(t *testing.T) {
	fc := NewFirewallCollector("ufw", false, logrus.New())

	apiRules := []FirewallRule{
		{From: "10.4.0.0/24", To: "10.4.0.0/24", Protocol: "tcp", Port: "1:65535"},
		{From: "10.4.0.0/24", To: "10.4.0.0/24", Protocol: "udp", Port: "1:65535"},
		{From: "172.16.0.0/13", To: "ANY", Protocol: "tcp", Port: "1:65535"},
		{From: "172.16.0.0/13", To: "ANY", Protocol: "udp", Port: "1:65535"},
	}

	ufwOutput := `Status: active

To                         Action      From
--                         ------      ----
10.4.0.0/24 1:65535/tcp    ALLOW       10.4.0.0/24
10.4.0.0/24 1:65535/udp    ALLOW       10.4.0.0/24
1:65535/tcp                ALLOW       172.16.0.0/13
1:65535/udp                ALLOW       172.16.0.0/13
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

func TestSyncDiff_NoCycleFromMismatchedProtocolCase(t *testing.T) {
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
