package collectors

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
)

// newTestCollector returns a collector whose ufw settings come from a fixture
// instead of the host. An empty ufwDefaults leaves /etc/default/ufw missing,
// so the IPv6 setting is unknown and catch-all addresses compare literally.
func newTestCollector(t *testing.T, caseSensitive bool, ufwDefaults string) *FirewallCollector {
	t.Helper()
	dir := t.TempDir()
	fc := NewFirewallCollector("ufw", caseSensitive, logrus.New())
	fc.ufwDefaultsPath = filepath.Join(dir, "default-ufw")
	fc.procIPv6Path = dir
	fc.useSudo = false // the agent runs as root under systemd
	if ufwDefaults != "" {
		if err := os.WriteFile(fc.ufwDefaultsPath, []byte(ufwDefaults), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return fc
}

// ufwDefaultsFixture is the stock /etc/default/ufw from Ubuntu 24.04 (ufw
// 0.36.2) with the IPV6 line set to the given value.
func ufwDefaultsFixture(ipv6 string) string {
	return `# /etc/default/ufw
#

# Set to yes to apply rules to support IPv6 (no means only IPv6 on loopback
# accepted). You will need to 'disable' and then 'enable' the firewall for
# the changes to take affect.
IPV6=` + ipv6 + `

# Set the default input policy to ACCEPT, DROP, or REJECT. Please note that if
# you change this you will most likely want to adjust your rules.
DEFAULT_INPUT_POLICY="DROP"

# Set the default output policy to ACCEPT, DROP, or REJECT. Please note that if
# you change this you will most likely want to adjust your rules.
DEFAULT_OUTPUT_POLICY="ACCEPT"

# Set the default forward policy to ACCEPT, DROP or REJECT.  Please note that
# if you change this you will most likely want to adjust your rules
DEFAULT_FORWARD_POLICY="DROP"

# Set the default application policy to ACCEPT, DROP, REJECT or SKIP. Please
# note that setting this to ACCEPT may be a security risk. See 'man ufw' for
# details
DEFAULT_APPLICATION_POLICY="SKIP"

# By default, ufw only touches its own chains. Set this to 'yes' to have ufw
# manage the built-in chains too. Warning: setting this to 'yes' will break
# non-ufw managed firewall rules
MANAGE_BUILTINS=no

#
# IPT backend
#
# only enable if using iptables backend
IPT_SYSCTL=/etc/ufw/sysctl.conf

# Extra connection tracking modules to load. IPT_MODULES should typically be
# empty for new installations and modules added only as needed. See
# 'CONNECTION HELPERS' from 'man ufw-framework' for details. Eg:
IPT_MODULES=""
`
}

func TestReadIPv6Mode(t *testing.T) {
	cases := []struct {
		name     string
		defaults string // "" = no file
		noProc   bool
		want     ipv6Mode
	}{
		{name: "stock file with IPV6=yes", defaults: ufwDefaultsFixture("yes"), want: ipv6On},
		{name: "stock file with IPV6=no", defaults: ufwDefaultsFixture("no"), want: ipv6Off},
		{name: "quoted and uppercase value", defaults: "IPV6=\"YES\"\n", want: ipv6On},
		{name: "last setting wins", defaults: "IPV6=yes\nIPV6=no\n", want: ipv6Off},
		{name: "commented out line is ignored", defaults: "#IPV6=yes\nIPV6=no\n", want: ipv6Off},
		{name: "missing key means no, as in ufw", defaults: "DEFAULT_INPUT_POLICY=\"DROP\"\n", want: ipv6Off},
		{name: "kernel without IPv6", defaults: ufwDefaultsFixture("yes"), noProc: true, want: ipv6Off},
		{name: "no defaults file", want: ipv6Unknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "ufw")
			if tc.defaults != "" {
				if err := os.WriteFile(path, []byte(tc.defaults), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			proc := dir
			if tc.noProc {
				proc = filepath.Join(dir, "missing")
			}
			if got := readIPv6Mode(path, proc); got != tc.want {
				t.Errorf("readIPv6Mode() = %s, want %s", got, tc.want)
			}
		})
	}
}

// fakeUFW models how ufw stores catch-all rules ("from any|0.0.0.0/0|::/0 to
// any") per address family and prints them in `ufw status`, following
// parser.py and frontend.py: "any" is stored once per family that ufw
// manages, 0.0.0.0/0 only for IPv4 and ::/0 only for IPv6, and the IPv6 copy
// is printed as a "(v6)" row. It lets a test run several sync cycles against
// the state the previous cycle left behind.
type fakeUFW struct {
	ipv6  bool
	rules map[string]bool // "v4 9001/tcp", "v6 9001/tcp"
	calls []string
}

func (u *fakeUFW) families(addr string) []string {
	switch addr {
	case "0.0.0.0/0":
		return []string{"v4"}
	case "::/0":
		if !u.ipv6 {
			return nil
		}
		return []string{"v6"}
	}
	if u.ipv6 {
		return []string{"v4", "v6"}
	}
	return []string{"v4"}
}

func (u *fakeUFW) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if len(args) > 0 && args[len(args)-1] == "status" {
		return []byte(u.status()), nil
	}
	argv := append([]string{name}, args...)
	u.calls = append(u.calls, strings.Join(argv, " "))
	if argv[0] == "sudo" {
		argv = argv[1:]
	}
	// argv is now ufw, the action, then key/value pairs.
	opts := map[string]string{}
	for i := 2; i+1 < len(argv); i++ {
		opts[argv[i]] = argv[i+1]
	}
	key := opts["port"] + "/" + opts["proto"]
	for _, fam := range u.families(opts["from"]) {
		switch argv[1] {
		case "allow":
			u.rules[fam+" "+key] = true
		case "delete":
			delete(u.rules, fam+" "+key)
		}
	}
	return nil, nil
}

func (u *fakeUFW) status() string {
	var v4, v6 []string
	for r := range u.rules {
		fam, key, _ := strings.Cut(r, " ")
		if fam == "v4" {
			v4 = append(v4, fmt.Sprintf("%-26s ALLOW       Anywhere", key))
		} else {
			v6 = append(v6, fmt.Sprintf("%-26s ALLOW       Anywhere (v6)", key+" (v6)"))
		}
	}
	sort.Strings(v4)
	sort.Strings(v6)
	return "Status: active\n\nTo                         Action      From\n--                         ------      ----\n" +
		strings.Join(append(v4, v6...), "\n") + "\n"
}

func (u *fakeUFW) state() string {
	var s []string
	for r := range u.rules {
		s = append(s, r)
	}
	sort.Strings(s)
	return strings.Join(s, ", ")
}

// TestSyncFirewallRules_CatchAllConvergesPerIPv6Setting runs four sync cycles
// per case against a fake ufw and checks that the host ends up with exactly the
// address families the API rule allows, and that the cycles after the first
// issue no ufw command. Before the agent read IPV6 from /etc/default/ufw, an
// IPv4-only "Anywhere" row was parsed as "any", so:
//   - a 0.0.0.0/0 rule from the API was added on one cycle and deleted (as
//     "from any") on the next, closing the port every other cycle;
//   - a dual-stack rule that had lost its IPv6 half was never repaired;
//   - on an IPV6=no host, 0.0.0.0/0 never matched the "Anywhere" row either.
func TestSyncFirewallRules_CatchAllConvergesPerIPv6Setting(t *testing.T) {
	cases := []struct {
		name    string
		ipv6    string
		from    string   // API rule source, to any port 9001/tcp; "" = no API rule
		initial []string // host state before the first cycle
		want    string
	}{
		{name: "IPV6=yes, 0.0.0.0/0 added", ipv6: "yes", from: "0.0.0.0/0", want: "v4 9001/tcp"},
		{name: "IPV6=yes, 0.0.0.0/0 already in place", ipv6: "yes", from: "0.0.0.0/0", initial: []string{"v4 9001/tcp"}, want: "v4 9001/tcp"},
		{name: "IPV6=yes, 0.0.0.0/0 removed from the API", ipv6: "yes", initial: []string{"v4 9001/tcp"}, want: ""},
		{name: "IPV6=yes, any narrowed to 0.0.0.0/0", ipv6: "yes", from: "0.0.0.0/0", initial: []string{"v4 9001/tcp", "v6 9001/tcp"}, want: "v4 9001/tcp"},
		{name: "IPV6=yes, any narrowed to ::/0", ipv6: "yes", from: "::/0", initial: []string{"v4 9001/tcp", "v6 9001/tcp"}, want: "v6 9001/tcp"},
		{name: "IPV6=yes, any missing its IPv6 half", ipv6: "yes", from: "any", initial: []string{"v4 9001/tcp"}, want: "v4 9001/tcp, v6 9001/tcp"},
		{name: "IPV6=yes, any missing its IPv4 half", ipv6: "yes", from: "any", initial: []string{"v6 9001/tcp"}, want: "v4 9001/tcp, v6 9001/tcp"},
		{name: "IPV6=yes, ::/0 widened to any", ipv6: "yes", from: "any", initial: []string{"v6 9001/tcp"}, want: "v4 9001/tcp, v6 9001/tcp"},
		{name: "IPV6=yes, 0.0.0.0/0 replaced by ::/0", ipv6: "yes", from: "::/0", initial: []string{"v4 9001/tcp"}, want: "v6 9001/tcp"},
		{name: "IPV6=no, 0.0.0.0/0 added", ipv6: "no", from: "0.0.0.0/0", want: "v4 9001/tcp"},
		{name: "IPV6=no, 0.0.0.0/0 in place as any", ipv6: "no", from: "0.0.0.0/0", initial: []string{"v4 9001/tcp"}, want: "v4 9001/tcp"},
		{name: "IPV6=no, any in place", ipv6: "no", from: "any", initial: []string{"v4 9001/tcp"}, want: "v4 9001/tcp"},
		{name: "IPV6=no, 0.0.0.0/0 removed from the API", ipv6: "no", initial: []string{"v4 9001/tcp"}, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fc := newTestCollector(t, false, ufwDefaultsFixture(tc.ipv6))
			u := &fakeUFW{ipv6: tc.ipv6 == "yes", rules: map[string]bool{}}
			for _, r := range tc.initial {
				u.rules[r] = true
			}
			fc.run = u.run

			apiJSON := `{"firewall":{"rules":[]}}`
			if tc.from != "" {
				apiJSON = `{"firewall":{"rules":[{"from":"` + tc.from + `","to":"any","protocol":"TCP","port":"9001"}]}}`
			}

			for cycle := 1; cycle <= 4; cycle++ {
				u.calls = nil
				if err := fc.SyncFirewallRules(context.Background(), apiJSON); err != nil {
					t.Fatalf("cycle %d: SyncFirewallRules: %v", cycle, err)
				}
				if got := u.state(); got != tc.want {
					t.Fatalf("cycle %d: host rules = %q, want %q (commands: %q)", cycle, got, tc.want, u.calls)
				}
				if cycle > 1 && len(u.calls) > 0 {
					t.Fatalf("cycle %d: expected no ufw command once in sync, got %q", cycle, u.calls)
				}
			}
		})
	}
}

// TestSyncFirewallRules_NarrowsDualStackRuleWithoutClosingIPv4 checks the exact
// command for an "any" rule narrowed to 0.0.0.0/0 in the dashboard: only the
// IPv6 half is deleted. Deleting "from any" would remove both halves and leave
// the port closed over IPv4 until the next cycle.
func TestSyncFirewallRules_NarrowsDualStackRuleWithoutClosingIPv4(t *testing.T) {
	fc := newTestCollector(t, false, ufwDefaultsFixture("yes"))
	u := &fakeUFW{ipv6: true, rules: map[string]bool{"v4 9001/tcp": true, "v6 9001/tcp": true}}
	fc.run = u.run

	apiJSON := `{"firewall":{"rules":[{"from":"0.0.0.0/0","to":"any","protocol":"tcp","port":"9001"}]}}`
	if err := fc.SyncFirewallRules(context.Background(), apiJSON); err != nil {
		t.Fatalf("SyncFirewallRules: %v", err)
	}
	want := []string{"ufw delete allow from ::/0 to any port 9001 proto tcp"}
	if strings.Join(u.calls, "\n") != strings.Join(want, "\n") {
		t.Errorf("commands = %q, want %q", u.calls, want)
	}
}

// TestSyncFirewallRules_UnknownIPv6SettingKeepsLiteralComparison checks that
// without a readable /etc/default/ufw the sync keeps comparing catch-all
// addresses literally, as before, instead of guessing the host's IPv6 setting.
func TestSyncFirewallRules_UnknownIPv6SettingKeepsLiteralComparison(t *testing.T) {
	fc := newTestCollector(t, false, "")
	u := &fakeUFW{ipv6: true, rules: map[string]bool{"v4 9001/tcp": true}}
	fc.run = u.run

	apiJSON := `{"firewall":{"rules":[{"from":"any","to":"any","protocol":"tcp","port":"9001"}]}}`
	if err := fc.SyncFirewallRules(context.Background(), apiJSON); err != nil {
		t.Fatalf("SyncFirewallRules: %v", err)
	}
	if len(u.calls) != 0 {
		t.Errorf("expected no ufw command, got %q", u.calls)
	}
}
