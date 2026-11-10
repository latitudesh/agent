package collectors

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// statusRunner answers `ufw status` with status and records every other ufw
// command a sync issues.
func statusRunner(status string, commands *[]string) commandRunner {
	return func(_ context.Context, name string, args ...string) ([]byte, error) {
		cmd := strings.Join(append([]string{name}, args...), " ")
		if cmd == "ufw status" {
			return []byte(status), nil
		}
		*commands = append(*commands, cmd)
		return nil, nil
	}
}

// commentedStatus is `ufw status` from ufw 0.36.2 for rules added with
// `ufw allow ... comment '...'`. Comments may themselves hold "#", "(v6)" or
// "ALLOW", and a DENY row must stay ignored whatever its comment says.
const commentedStatus = `Status: active

To                         Action      From
--                         ------      ----
22/tcp                     ALLOW       Anywhere                   # ssh
8080/tcp                   ALLOW       10.0.0.0/8                 # vendor access # ticket 42
203.0.113.5 9000/tcp       ALLOW       Anywhere                   # api (v6 soon)
3306/tcp                   DENY        Anywhere                   # was ALLOW before the audit
22/tcp (v6)                ALLOW       Anywhere (v6)              # ssh
`

func TestStripUFWComment(t *testing.T) {
	cases := map[string]string{
		"22/tcp  ALLOW  Anywhere  # ssh":        "22/tcp  ALLOW  Anywhere  ",
		"22/tcp  ALLOW  Anywhere  # a # b":      "22/tcp  ALLOW  Anywhere  ",
		"22/tcp  ALLOW  Anywhere":               "22/tcp  ALLOW  Anywhere",
		"22/tcp (v6)  ALLOW  Anywhere (v6)  #x": "22/tcp (v6)  ALLOW  Anywhere (v6)  ",
		"":                                      "",
	}
	for in, want := range cases {
		if got := stripUFWComment(in); got != want {
			t.Errorf("stripUFWComment(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseUFWRules_IgnoresComments(t *testing.T) {
	fc := newTestCollector(false, true)
	rules, err := fc.parseUFWRules(commentedStatus)
	if err != nil {
		t.Fatalf("parseUFWRules: %v", err)
	}

	var got []string
	for _, r := range rules {
		got = append(got, string(r.family)+" "+r.rule.String())
	}
	want := []string{
		"v4 From: any, To: any, Protocol: tcp, Port: 22",
		"v4 From: 10.0.0.0/8, To: any, Protocol: tcp, Port: 8080",
		"v4 From: any, To: 203.0.113.5, Protocol: tcp, Port: 9000",
		"v6 From: any, To: any, Protocol: tcp, Port: 22",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("parsed rules:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestSyncFirewallRules_CommentedRules checks that a commented rule still in
// the dashboard is left alone and one the dashboard dropped is deleted.
func TestSyncFirewallRules_CommentedRules(t *testing.T) {
	var commands []string
	fc := newTestCollector(false, true)
	fc.run = statusRunner(commentedStatus, &commands)

	api := `{"firewall":{"rules":[
		{"from":"any","to":"any","protocol":"tcp","port":"22"},
		{"from":"any","to":"203.0.113.5","protocol":"tcp","port":"9000"}
	]}}`
	if err := fc.SyncFirewallRules(context.Background(), api); err != nil {
		t.Fatalf("SyncFirewallRules: %v", err)
	}

	want := "ufw delete allow from 10.0.0.0/8 to any port 8080 proto tcp"
	if len(commands) != 1 || commands[0] != want {
		t.Errorf("commands = %q, want only %q", commands, want)
	}
}

func TestSyncFirewallRules_RefusesUntrustedStatus(t *testing.T) {
	api := `{"firewall":{"rules":[{"from":"any","to":"any","protocol":"tcp","port":"22"}]}}`
	cases := []struct {
		name    string
		status  string
		wantErr string
	}{
		{name: "inactive", status: "Status: inactive\n", wantErr: "ufw is inactive"},
		{name: "no status line", status: "ERROR: problem running iptables\n", wantErr: `unexpected ` + "`ufw status`" + ` output (state "")`},
		{name: "unknown state", status: "Status: inativo\n", wantErr: `state "inativo"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var commands []string
			fc := newTestCollector(false, true)
			fc.run = statusRunner(tc.status, &commands)

			err := fc.SyncFirewallRules(context.Background(), api)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("SyncFirewallRules error = %v, want it to contain %q", err, tc.wantErr)
			}
			if tc.name == "inactive" && !errors.Is(err, ErrUFWInactive) {
				t.Errorf("error %v does not wrap ErrUFWInactive", err)
			}
			if len(commands) != 0 {
				t.Errorf("expected no ufw command besides status, got %q", commands)
			}
		})
	}
}

func TestExecRunner_UsesCLocale(t *testing.T) {
	t.Setenv("LANG", "pt_BR.UTF-8")
	t.Setenv("LC_ALL", "pt_BR.UTF-8")
	t.Setenv("LANGUAGE", "pt_BR")

	out, err := execRunner(context.Background(), "sh", "-c", `printf '%s %s' "$LC_ALL" "$LANGUAGE"`)
	if err != nil {
		t.Fatalf("execRunner: %v", err)
	}
	if want := "C.UTF-8 C"; string(out) != want {
		t.Errorf("LC_ALL LANGUAGE = %q, want %q", out, want)
	}
}
