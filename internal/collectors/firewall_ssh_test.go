package collectors

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// TestSyncFirewallRules_KeepsSSHAccess checks that the SSH rule install.sh
// creates (`ufw allow ssh`, shown as 22/tcp in both tables) is only removed
// when the dashboard grants SSH itself, and only after that grant is in place.
func TestSyncFirewallRules_KeepsSSHAccess(t *testing.T) {
	const ssh = "any|any|tcp|22"
	cases := []struct {
		name         string
		v4, v6       []string // tables before the sync
		api          string   // firewall object
		failAdd      string   // an add command containing this fails
		wantV4       []string
		wantV6       []string
		wantCommands []string
	}{
		{
			name:   "disabled firewall keeps SSH",
			v4:     []string{ssh},
			v6:     []string{ssh},
			api:    `{}`,
			wantV4: []string{ssh},
			wantV6: []string{ssh},
		},
		{
			name:   "IPv6-only SSH rule is kept too",
			v6:     []string{ssh},
			api:    `{}`,
			wantV6: []string{ssh},
		},
		{
			name:   "rule set without SSH keeps SSH and syncs the rest",
			v4:     []string{ssh, "10.0.0.0/8|any|tcp|8080"},
			v6:     []string{ssh},
			api:    `{"rules":[{"from":"any","to":"any","protocol":"tcp","port":"443"}]}`,
			wantV4: []string{ssh, "any|any|tcp|443"},
			wantV6: []string{ssh, "any|any|tcp|443"},
			wantCommands: []string{
				"ufw allow proto tcp from any to any port 443",
				"ufw delete allow from 10.0.0.0/8 to any port 8080 proto tcp",
			},
		},
		{
			name:   "a broad range that includes 22 is still removed",
			v4:     []string{ssh, "10.0.0.0/8|any|tcp|1:65535"},
			v6:     []string{ssh},
			api:    `{}`,
			wantV4: []string{ssh},
			wantV6: []string{ssh},
			wantCommands: []string{
				"ufw delete allow from 10.0.0.0/8 to any port 1:65535 proto tcp",
			},
		},
		{
			name:   "dashboard SSH rule replaces the open one after it is added",
			v4:     []string{ssh},
			v6:     []string{ssh},
			api:    `{"rules":[{"from":"203.0.113.0/24","to":"any","protocol":"tcp","port":"22"}]}`,
			wantV4: []string{"203.0.113.0/24|any|tcp|22"},
			wantCommands: []string{
				"ufw allow proto tcp from 203.0.113.0/24 to any port 22",
				"ufw delete allow from any to any port 22 proto tcp",
			},
		},
		{
			name:    "failed SSH add keeps the open rule",
			v4:      []string{ssh, "10.0.0.0/8|any|tcp|8080"},
			v6:      []string{ssh},
			api:     `{"rules":[{"from":"203.0.113.0/24","to":"any","protocol":"tcp","port":"22"}]}`,
			failAdd: "from 203.0.113.0/24",
			wantV4:  []string{ssh},
			wantV6:  []string{ssh},
			wantCommands: []string{
				"ufw allow proto tcp from 203.0.113.0/24 to any port 22",
				"ufw delete allow from 10.0.0.0/8 to any port 8080 proto tcp",
			},
		},
		{
			name:   "dashboard grants SSH through a range",
			v4:     []string{ssh},
			v6:     []string{ssh},
			api:    `{"rules":[{"from":"10.0.0.0/8","to":"any","protocol":"tcp","port":"1:65535"}]}`,
			wantV4: []string{"10.0.0.0/8|any|tcp|1:65535"},
			wantCommands: []string{
				"ufw allow proto tcp from 10.0.0.0/8 to any port 1:65535",
				"ufw delete allow from any to any port 22 proto tcp",
			},
		},
		{
			name:   "UDP 22 is not SSH",
			v4:     []string{ssh, "any|any|udp|22"},
			v6:     []string{ssh, "any|any|udp|22"},
			api:    `{}`,
			wantV4: []string{ssh},
			wantV6: []string{ssh},
			wantCommands: []string{
				"ufw delete allow from any to any port 22 proto udp",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ufw := &fakeUFW{ipv6On: true, v4: tc.v4, v6: tc.v6}
			fc := newTestCollector(false, true)
			fc.run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
				cmd := strings.Join(args, " ")
				if tc.failAdd != "" && args[0] == "allow" && strings.Contains(cmd, tc.failAdd) {
					ufw.commands = append(ufw.commands, "ufw "+cmd)
					return []byte("ERROR: simulated"), errors.New("exit status 1")
				}
				return ufw.run(ctx, name, args...)
			}

			api := `{"firewall":` + tc.api + `}`
			if err := fc.SyncFirewallRules(context.Background(), api); err != nil {
				t.Fatalf("SyncFirewallRules: %v", err)
			}
			if !reflect.DeepEqual(ufw.commands, tc.wantCommands) {
				t.Errorf("commands:\n got %q\nwant %q", ufw.commands, tc.wantCommands)
			}
			if !reflect.DeepEqual(sorted(ufw.v4), sorted(tc.wantV4)) || !reflect.DeepEqual(sorted(ufw.v6), sorted(tc.wantV6)) {
				t.Errorf("tables: v4=%q v6=%q, want v4=%q v6=%q", ufw.v4, ufw.v6, tc.wantV4, tc.wantV6)
			}
		})
	}
}

func TestAllowsSSH(t *testing.T) {
	cases := map[string]bool{
		"tcp/22":         true,
		"tcp/1:65535":    true,
		"tcp/80,22":      true,
		"tcp/20:21,23":   false,
		"tcp/2222":       false,
		"udp/22":         false,
		"tcp/":           true,
		"any/22":         true,
		"tcp/8000:8010":  false,
		"tcp/443, 22":    true,
		"tcp/notaport22": false,
	}
	for in, want := range cases {
		proto, port, _ := strings.Cut(in, "/")
		if got := allowsSSH(FirewallRule{Protocol: proto, Port: port}); got != want {
			t.Errorf("allowsSSH(%s) = %v, want %v", in, got, want)
		}
	}
}
