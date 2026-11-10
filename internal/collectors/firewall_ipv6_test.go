package collectors

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
)

// newTestCollector returns a collector that runs ufw as root and sees a fixed
// IPv6 setting, so tests do not depend on the host's /etc/default/ufw.
func newTestCollector(caseSensitive, ipv6On bool) *FirewallCollector {
	fc := NewFirewallCollector("ufw", caseSensitive, logrus.New())
	fc.useSudo = false
	fc.ipv6Enabled = func() (bool, error) { return ipv6On, nil }
	return fc
}

func TestUFWIPv6Enabled(t *testing.T) {
	dir := t.TempDir()
	procIPv6 := filepath.Join(dir, "ipv6")
	if err := os.Mkdir(procIPv6, 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name     string
		defaults string
		noKernel bool
		want     bool
	}{
		{name: "stock yes", defaults: "# Set to yes to apply rules to support IPv6\nIPV6=yes\n\nDEFAULT_INPUT_POLICY=\"DROP\"\n", want: true},
		{name: "no", defaults: "IPV6=no\n", want: false},
		{name: "quoted and uppercase", defaults: "IPV6=\"YES\"\n", want: true},
		{name: "last setting wins", defaults: "IPV6=yes\nIPV6=no\n", want: false},
		{name: "commented out", defaults: "#IPV6=yes\n", want: false},
		{name: "indented is ignored like ufw does", defaults: "  IPV6=yes\n", want: false},
		{name: "missing key means no", defaults: "DEFAULT_INPUT_POLICY=\"DROP\"\n", want: false},
		{name: "kernel without IPv6", defaults: "IPV6=yes\n", noKernel: true, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ufw")
			if err := os.WriteFile(path, []byte(tc.defaults), 0o644); err != nil {
				t.Fatal(err)
			}
			proc := procIPv6
			if tc.noKernel {
				proc = filepath.Join(dir, "missing")
			}
			got, err := ufwIPv6Enabled(path, proc)
			if err != nil {
				t.Fatalf("ufwIPv6Enabled: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}

	if _, err := ufwIPv6Enabled(filepath.Join(dir, "absent"), procIPv6); err == nil {
		t.Error("expected an error for a missing defaults file")
	}
}

// TestParseUFWRules_AssignsTables checks that each status row lands in the ufw
// table it comes from, with catch-all addresses as "any".
func TestParseUFWRules_AssignsTables(t *testing.T) {
	fc := newTestCollector(false, true)
	status := `Status: active

To                         Action      From
--                         ------      ----
22/tcp                     ALLOW       Anywhere
443/tcp                    ALLOW       Anywhere
203.0.113.5 8080/tcp       ALLOW       Anywhere
22/tcp (v6)                ALLOW       Anywhere (v6)
2001:db8::1 8080/tcp       ALLOW       Anywhere (v6)
9000/tcp                   ALLOW       2001:db8::/32
`
	rules, err := fc.parseUFWRules(status)
	if err != nil {
		t.Fatalf("parseUFWRules: %v", err)
	}
	want := []string{
		"v4 From: any, To: any, Protocol: tcp, Port: 22",
		"v4 From: any, To: any, Protocol: tcp, Port: 443",
		"v4 From: any, To: 203.0.113.5, Protocol: tcp, Port: 8080",
		"v6 From: any, To: any, Protocol: tcp, Port: 22",
		"v6 From: any, To: 2001:db8::1, Protocol: tcp, Port: 8080",
		"v6 From: 2001:db8::/32, To: any, Protocol: tcp, Port: 9000",
	}
	var got []string
	for _, r := range rules {
		got = append(got, string(r.family)+" "+r.rule.String())
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tables:\n got %q\nwant %q", got, want)
	}
}

// fakeUFW stores rules per table like ufw does and prints them like
// `ufw status` (checked against ufw 0.36.2), so a test can run several sync
// cycles against it.
type fakeUFW struct {
	ipv6On   bool
	v4, v6   []string // "from|to|proto|port", catch-all addresses as "any"
	commands []string
}

func fakeFamily(addr string) string {
	switch {
	case addr == "any":
		return ""
	case strings.Contains(addr, ":"):
		return "v6"
	default:
		return "v4"
	}
}

func fakeCatchAll(addr string) string {
	if addr == "0.0.0.0/0" || addr == "::/0" {
		return "any"
	}
	return addr
}

// tables returns the tables a ufw command with these addresses touches.
func (f *fakeUFW) tables(from, to string) []*[]string {
	family := fakeFamily(from)
	if family == "" {
		family = fakeFamily(to)
	}
	switch family {
	case "v4":
		return []*[]string{&f.v4}
	case "v6":
		if !f.ipv6On {
			return nil
		}
		return []*[]string{&f.v6}
	}
	if f.ipv6On {
		return []*[]string{&f.v4, &f.v6}
	}
	return []*[]string{&f.v4}
}

func (f *fakeUFW) run(_ context.Context, name string, args ...string) ([]byte, error) {
	if name != "ufw" {
		return nil, fmt.Errorf("unexpected command %q", name)
	}
	cmd := strings.Join(args, " ")
	if cmd == "status" {
		return []byte(f.status()), nil
	}
	f.commands = append(f.commands, "ufw "+cmd)
	var from, to, proto, port string
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "from":
			from = args[i+1]
		case "to":
			to = args[i+1]
		case "proto":
			proto = args[i+1]
		case "port":
			port = args[i+1]
		}
	}
	entry := strings.Join([]string{fakeCatchAll(from), fakeCatchAll(to), proto, port}, "|")
	for _, table := range f.tables(from, to) {
		idx := -1
		for i, e := range *table {
			if e == entry {
				idx = i
			}
		}
		switch {
		case args[0] == "allow" && idx < 0:
			*table = append(*table, entry)
		case args[0] == "delete" && idx >= 0:
			*table = append((*table)[:idx], (*table)[idx+1:]...)
		}
	}
	return nil, nil
}

func (f *fakeUFW) status() string {
	var b strings.Builder
	b.WriteString("Status: active\n\nTo                         Action      From\n--                         ------      ----\n")
	row := func(entry string, v6 bool) {
		p := strings.Split(entry, "|")
		from, to, portProto := p[0], p[1], p[3]+"/"+p[2]
		// ufw tags the IPv6 port column only when both addresses are ::/0
		// ("9001/tcp (v6) ALLOW Anywhere (v6)"); "8443/tcp ALLOW 2001:db8::/32"
		// carries no tag.
		toCol := portProto
		if to != "any" {
			toCol = to + " " + portProto
		} else if v6 && from == "any" {
			toCol += " (v6)"
		}
		fromCol := from
		if from == "any" {
			fromCol = "Anywhere"
			if v6 {
				fromCol += " (v6)"
			}
		}
		fmt.Fprintf(&b, "%-26s ALLOW       %s\n", toCol, fromCol)
	}
	for _, e := range f.v4 {
		row(e, false)
	}
	for _, e := range f.v6 {
		row(e, true)
	}
	return b.String()
}

func sorted(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

// TestSyncFirewallRules_ConvergesPerTable runs each case for three sync cycles:
// the first must leave each ufw table as expected with exactly the listed
// commands, and the next two must issue no command at all.
func TestSyncFirewallRules_ConvergesPerTable(t *testing.T) {
	const anyRule = "any|any|tcp|9001"
	cases := []struct {
		name         string
		ipv6Off      bool
		v4, v6       []string // tables before the first sync
		api          string   // rules JSON
		wantV4       []string
		wantV6       []string
		wantCommands []string // first cycle
	}{
		{
			name:         "any is added once for both tables",
			api:          `{"from":"any","to":"any","protocol":"tcp","port":"9001"}`,
			wantV4:       []string{anyRule},
			wantV6:       []string{anyRule},
			wantCommands: []string{"ufw allow proto tcp from any to any port 9001"},
		},
		{
			name:         "0.0.0.0/0 stays IPv4 only and stops flapping",
			api:          `{"from":"0.0.0.0/0","to":"any","protocol":"tcp","port":"9001"}`,
			wantV4:       []string{anyRule},
			wantCommands: []string{"ufw allow proto tcp from 0.0.0.0/0 to any port 9001"},
		},
		{
			name:         "::/0 stays IPv6 only",
			api:          `{"from":"::/0","to":"any","protocol":"tcp","port":"9001"}`,
			wantV6:       []string{anyRule},
			wantCommands: []string{"ufw allow proto tcp from ::/0 to any port 9001"},
		},
		{
			name:         "::/0 removed from the dashboard is deleted",
			v6:           []string{anyRule},
			api:          ``,
			wantCommands: []string{"ufw delete allow from ::/0 to any port 9001 proto tcp"},
		},
		{
			name:         "dual-stack any removed is deleted with one command",
			v4:           []string{anyRule},
			v6:           []string{anyRule},
			api:          ``,
			wantCommands: []string{"ufw delete allow from any to any port 9001 proto tcp"},
		},
		{
			name:         "narrowing any to 0.0.0.0/0 deletes only the IPv6 half",
			v4:           []string{anyRule},
			v6:           []string{anyRule},
			api:          `{"from":"0.0.0.0/0","to":"any","protocol":"tcp","port":"9001"}`,
			wantV4:       []string{anyRule},
			wantCommands: []string{"ufw delete allow from ::/0 to any port 9001 proto tcp"},
		},
		{
			name:         "widening ::/0 to any adds only the IPv4 half",
			v6:           []string{anyRule},
			api:          `{"from":"any","to":"any","protocol":"tcp","port":"9001"}`,
			wantV4:       []string{anyRule},
			wantV6:       []string{anyRule},
			wantCommands: []string{"ufw allow proto tcp from 0.0.0.0/0 to any port 9001"},
		},
		{
			name:         "any that lost its IPv6 half is repaired",
			v4:           []string{anyRule},
			api:          `{"from":"any","to":"any","protocol":"tcp","port":"9001"}`,
			wantV4:       []string{anyRule},
			wantV6:       []string{anyRule},
			wantCommands: []string{"ufw allow proto tcp from ::/0 to any port 9001"},
		},
		{
			name:   "0.0.0.0/0 replaced by ::/0 swaps tables",
			v4:     []string{anyRule},
			api:    `{"from":"::/0","to":"any","protocol":"tcp","port":"9001"}`,
			wantV6: []string{anyRule},
			wantCommands: []string{
				"ufw allow proto tcp from ::/0 to any port 9001",
				"ufw delete allow from 0.0.0.0/0 to any port 9001 proto tcp",
			},
		},
		{
			name:   "catch-all source with a specific destination is in place",
			v4:     []string{"any|203.0.113.5|tcp|22"},
			v6:     []string{"any|2001:db8::1|tcp|22"},
			api:    `{"from":"0.0.0.0/0","to":"203.0.113.5/32","protocol":"TCP","port":"22"},{"from":"::/0","to":"2001:db8::1","protocol":"tcp","port":"22"}`,
			wantV4: []string{"any|203.0.113.5|tcp|22"},
			wantV6: []string{"any|2001:db8::1|tcp|22"},
		},
		{
			name:         "IPv6 off: any is an IPv4 rule",
			ipv6Off:      true,
			api:          `{"from":"any","to":"any","protocol":"tcp","port":"9001"}`,
			wantV4:       []string{anyRule},
			wantCommands: []string{"ufw allow proto tcp from 0.0.0.0/0 to any port 9001"},
		},
		{
			name:    "IPv6 off: 0.0.0.0/0 matches the Anywhere row",
			ipv6Off: true,
			v4:      []string{anyRule},
			api:     `{"from":"0.0.0.0/0","to":"any","protocol":"tcp","port":"9001"}`,
			wantV4:  []string{anyRule},
		},
		{
			name:    "IPv6 off: ::/0 is skipped",
			ipv6Off: true,
			api:     `{"from":"::/0","to":"any","protocol":"tcp","port":"9001"}`,
		},
		{
			name:         "multiport is added in ufw's order",
			api:          `{"from":"any","to":"any","protocol":"tcp","port":"443, 80"}`,
			wantV4:       []string{"any|any|tcp|80,443"},
			wantV6:       []string{"any|any|tcp|80,443"},
			wantCommands: []string{"ufw allow proto tcp from any to any port 80,443"},
		},
		{
			name:         "multiport removed from the dashboard is deleted",
			v4:           []string{"any|any|tcp|80,443"},
			v6:           []string{"any|any|tcp|80,443"},
			api:          ``,
			wantCommands: []string{"ufw delete allow from any to any port 80,443 proto tcp"},
		},
		{
			name:   "multiport with a range to a host is in place",
			v4:     []string{"10.0.0.0/8|203.0.113.5|udp|8000:8010,9000"},
			api:    `{"from":"10.0.0.0/8","to":"203.0.113.5","protocol":"UDP","port":"9000,8000:8010"}`,
			wantV4: []string{"10.0.0.0/8|203.0.113.5|udp|8000:8010,9000"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ufw := &fakeUFW{ipv6On: !tc.ipv6Off, v4: tc.v4, v6: tc.v6}
			fc := newTestCollector(false, !tc.ipv6Off)
			fc.run = ufw.run
			api := `{"firewall":{"rules":[` + tc.api + `]}}`

			for cycle := 1; cycle <= 3; cycle++ {
				ufw.commands = nil
				if err := fc.SyncFirewallRules(context.Background(), api); err != nil {
					t.Fatalf("cycle %d: %v", cycle, err)
				}
				want := tc.wantCommands
				if cycle > 1 {
					want = nil
				}
				if !reflect.DeepEqual(ufw.commands, want) {
					t.Errorf("cycle %d commands:\n got %q\nwant %q", cycle, ufw.commands, want)
				}
				if !reflect.DeepEqual(sorted(ufw.v4), sorted(tc.wantV4)) || !reflect.DeepEqual(sorted(ufw.v6), sorted(tc.wantV6)) {
					t.Errorf("cycle %d tables: v4=%q v6=%q, want v4=%q v6=%q", cycle, ufw.v4, ufw.v6, tc.wantV4, tc.wantV6)
				}
			}
		})
	}
}

// TestSyncFirewallRules_UnknownIPv6SettingKeepsIPv6 checks that when the ufw
// setting cannot be read the IPv6 half of an "any" rule is not deleted.
func TestSyncFirewallRules_UnknownIPv6SettingKeepsIPv6(t *testing.T) {
	ufw := &fakeUFW{ipv6On: true, v4: []string{"any|any|tcp|22"}, v6: []string{"any|any|tcp|22"}}
	fc := newTestCollector(false, true)
	fc.ipv6Enabled = func() (bool, error) { return false, errors.New("permission denied") }
	fc.run = ufw.run

	api := `{"firewall":{"rules":[{"from":"any","to":"any","protocol":"tcp","port":"22"}]}}`
	if err := fc.SyncFirewallRules(context.Background(), api); err != nil {
		t.Fatalf("SyncFirewallRules: %v", err)
	}
	if len(ufw.commands) != 0 {
		t.Errorf("expected no commands, got %q", ufw.commands)
	}
}

// TestSyncFirewallRules_SkipsMixedFamilyRule checks that an API rule pairing an
// IPv4 and an IPv6 address, which ufw rejects, is skipped instead of retried.
func TestSyncFirewallRules_SkipsMixedFamilyRule(t *testing.T) {
	ufw := &fakeUFW{ipv6On: true}
	fc := newTestCollector(false, true)
	fc.run = ufw.run

	api := `{"firewall":{"rules":[{"from":"10.0.0.1","to":"2001:db8::1","protocol":"tcp","port":"22"}]}}`
	if err := fc.SyncFirewallRules(context.Background(), api); err != nil {
		t.Fatalf("SyncFirewallRules: %v", err)
	}
	if len(ufw.commands) != 0 {
		t.Errorf("expected no commands, got %q", ufw.commands)
	}
}
