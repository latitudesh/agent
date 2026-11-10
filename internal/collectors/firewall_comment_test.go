package collectors

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// commentedStatus is `ufw status` from ufw 0.36.2 after
//
//	ufw allow ssh comment 'ssh'
//	ufw allow from 10.0.0.0/8 to any port 8080 proto tcp comment 'office # vpn'
//	ufw allow from any to 203.0.113.5 port 9000 proto tcp comment 'api (v6 soon)'
//
// ufw pads the From column and appends "# <comment>".
const commentedStatus = `Status: active

To                         Action      From
--                         ------      ----
22/tcp                     ALLOW       Anywhere                   # ssh
8080/tcp                   ALLOW       10.0.0.0/8                 # office # vpn
203.0.113.5 9000/tcp       ALLOW       Anywhere                   # api (v6 soon)
22/tcp (v6)                ALLOW       Anywhere (v6)              # ssh
`

func TestParseUFWRules_StripsComments(t *testing.T) {
	fc := newTestCollector(false, true)
	rules, err := fc.parseUFWRules(commentedStatus)
	if err != nil {
		t.Fatalf("parseUFWRules: %v", err)
	}
	want := []string{
		"v4 From: any, To: any, Protocol: tcp, Port: 22",
		"v4 From: 10.0.0.0/8, To: any, Protocol: tcp, Port: 8080",
		"v4 From: any, To: 203.0.113.5, Protocol: tcp, Port: 9000",
		"v6 From: any, To: any, Protocol: tcp, Port: 22",
	}
	var got []string
	for _, r := range rules {
		got = append(got, string(r.family)+" "+r.rule.String())
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rules:\n got %q\nwant %q", got, want)
	}
}

// TestSyncFirewallRules_RemovesCommentedRule checks that a rule with a ufw
// comment is matched like any other: kept while the dashboard has it, and
// deleted once the dashboard drops it.
func TestSyncFirewallRules_RemovesCommentedRule(t *testing.T) {
	fc := newTestCollector(false, true)
	var commands []string
	fc.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if len(args) == 1 && args[0] == "status" {
			return []byte(commentedStatus), nil
		}
		commands = append(commands, name+" "+strings.Join(args, " "))
		return nil, nil
	}

	api := `{"firewall":{"rules":[
		{"from":"any","to":"any","protocol":"tcp","port":"22"},
		{"from":"any","to":"203.0.113.5","protocol":"tcp","port":"9000"}
	]}}`
	if err := fc.SyncFirewallRules(context.Background(), api); err != nil {
		t.Fatalf("SyncFirewallRules: %v", err)
	}

	want := []string{"ufw delete allow from 10.0.0.0/8 to any port 8080 proto tcp"}
	if !reflect.DeepEqual(commands, want) {
		t.Errorf("commands:\n got %q\nwant %q", commands, want)
	}
}
