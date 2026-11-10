package collectors

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"
)

// FirewallRule represents a firewall rule
type FirewallRule struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Protocol string `json:"protocol"`
	Port     string `json:"port"`
}

// String returns a normalized string representation of the rule.
// Normalization keeps the diff key stable across the API (which keeps
// /32 and /128 host suffixes and may use uppercase protocol) and ufw
// status output (which strips /32 and /128 and emits lowercase protocol).
func (r FirewallRule) String() string {
	n := r.normalized()
	return fmt.Sprintf("From: %s, To: %s, Protocol: %s, Port: %s", n.From, n.To, n.Protocol, n.Port)
}

// normalized returns the rule with every field in the form ufw stores it.
func (r FirewallRule) normalized() FirewallRule {
	protocol := strings.ToLower(strings.TrimSpace(r.Protocol))
	if protocol == "" {
		protocol = "any"
	}
	port := normalizePort(r.Port)
	if port == "" {
		port = "any"
	}
	return FirewallRule{From: normalizeAddr(r.From), To: normalizeAddr(r.To), Protocol: protocol, Port: port}
}

// normalizePort puts a multiport list in the form ufw stores and prints it:
// no spaces, entries sorted by their first port ("443, 80" is stored as
// "80,443", "9000,8000:8010" as "8000:8010,9000"). A single port or range is
// returned trimmed.
func normalizePort(port string) string {
	port = strings.ReplaceAll(strings.TrimSpace(port), " ", "")
	if !strings.Contains(port, ",") {
		return port
	}
	entries := strings.Split(port, ",")
	start := func(entry string) int {
		first, _, _ := strings.Cut(entry, ":")
		n, err := strconv.Atoi(first)
		if err != nil {
			return -1
		}
		return n
	}
	sort.SliceStable(entries, func(i, j int) bool { return start(entries[i]) < start(entries[j]) })
	return strings.Join(entries, ",")
}

// normalizeAddr canonicalizes a source/destination address the way ufw does
// before storing a rule, so the diff key is stable across the API and ufw
// status output. Empty values and the "anywhere" / "any" labels collapse to
// "any" (matched case-insensitively, since the API may send "ANY" while ufw
// status prints "Anywhere"). Host prefixes (/32, /128) are dropped, and a CIDR
// with host bits set is masked to its network address: the API accepts
// 72.46.85.137/31, but ufw stores and prints 72.46.85.136/31. Without that, the
// same rule lands in both toAdd and toRemove, and every other sync deletes it.
//
// A /0 prefix is kept as is: "any" covers both address families while
// 0.0.0.0/0 and ::/0 cover one. splitByFamily is where they are told apart.
func normalizeAddr(addr string) string {
	a := strings.TrimSpace(addr)
	if a == "" || strings.EqualFold(a, "Anywhere") || strings.EqualFold(a, "any") {
		return "any"
	}
	if prefix, err := netip.ParsePrefix(a); err == nil {
		prefix = prefix.Masked()
		if prefix.IsSingleIP() {
			return prefix.Addr().String()
		}
		return prefix.String()
	}
	if ip, err := netip.ParseAddr(a); err == nil {
		return ip.String()
	}
	return a
}

// ipFamily is the ufw table a rule lives in. ufw keeps IPv4 and IPv6 rules in
// separate tables (/etc/ufw/user.rules and user6.rules): a dual-stack "any"
// rule is one entry in each, while 0.0.0.0/0 and ::/0 are an entry in one
// table only. `ufw status` prints every catch-all address as "Anywhere" and
// tags rows of the IPv6 table with "(v6)", so the three can only be told apart
// by comparing the rules table by table.
type ipFamily string

const (
	ipv4 ipFamily = "v4"
	ipv6 ipFamily = "v6"
)

// tableRule is one entry of a ufw table. Its addresses are normalized, and a
// catch-all address (any, 0.0.0.0/0, ::/0) is always "any": the table already
// says which family it covers.
type tableRule struct {
	family ipFamily
	rule   FirewallRule
}

// addrFamily returns the family an address pins a rule to, or "" for "any".
func addrFamily(addr string) (ipFamily, error) {
	if addr == "any" {
		return "", nil
	}
	ip, err := netip.ParseAddr(addr)
	if err != nil {
		prefix, perr := netip.ParsePrefix(addr)
		if perr != nil {
			return "", fmt.Errorf("unsupported address %q", addr)
		}
		ip = prefix.Addr()
	}
	if ip.Is4() {
		return ipv4, nil
	}
	return ipv6, nil
}

// isCatchAll reports whether a normalized address matches every host of its
// family.
func isCatchAll(addr string) bool {
	return addr == "any" || addr == "0.0.0.0/0" || addr == "::/0"
}

// splitByFamily normalizes a rule and returns the family its addresses pin it
// to ("" when both are "any", i.e. it is a dual-stack rule), with catch-all
// addresses rewritten to "any".
func splitByFamily(r FirewallRule) (FirewallRule, ipFamily, error) {
	n := r.normalized()
	from, err := addrFamily(n.From)
	if err != nil {
		return n, "", err
	}
	to, err := addrFamily(n.To)
	if err != nil {
		return n, "", err
	}
	if from != "" && to != "" && from != to {
		return n, "", fmt.Errorf("mixes IPv4 and IPv6 addresses")
	}
	family := from
	if family == "" {
		family = to
	}
	if isCatchAll(n.From) {
		n.From = "any"
	}
	if isCatchAll(n.To) {
		n.To = "any"
	}
	return n, family, nil
}

// FirewallResponse represents the API response structure
type FirewallResponse struct {
	Firewall struct {
		Rules []FirewallRule `json:"rules"`
	} `json:"firewall"`
}

// commandRunner executes an external command and returns its combined output.
// It is a field on FirewallCollector so tests can capture the exact ufw
// commands a sync issues (e.g. to assert that no `ufw reload` is run).
type commandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

func execRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// FirewallCollector handles firewall rule collection and synchronization
type FirewallCollector struct {
	ufwBinary     string
	caseSensitive bool
	logger        *logrus.Logger
	run           commandRunner
	// useSudo prefixes ufw with sudo. Only needed when the agent is started
	// by an unprivileged user (local testing); under systemd it runs as root
	// and calls ufw directly, so hosts without sudo work too.
	useSudo bool
	// ipv6Enabled reports whether ufw manages IPv6 on this host. It is read
	// on every sync so a change to /etc/default/ufw needs no agent restart.
	ipv6Enabled func() (bool, error)
}

// NewFirewallCollector creates a new firewall collector
func NewFirewallCollector(ufwBinary string, caseSensitive bool, logger *logrus.Logger) *FirewallCollector {
	return &FirewallCollector{
		ufwBinary:     ufwBinary,
		caseSensitive: caseSensitive,
		logger:        logger,
		run:           execRunner,
		useSudo:       os.Geteuid() != 0,
		ipv6Enabled: func() (bool, error) {
			return ufwIPv6Enabled("/etc/default/ufw", "/proc/sys/net/ipv6")
		},
	}
}

// ufwDefaultRegex matches a setting the way ufw's _get_defaults reads
// /etc/default/ufw: KEY=value at the start of the line, optionally quoted.
var ufwDefaultRegex = regexp.MustCompile(`^(\w+)="?(\w+)"?`)

// ufwIPv6Enabled reports whether ufw manages IPv6, deciding it as ufw does:
// IPV6=yes in its defaults file (the last setting wins, a missing one means
// no) and a kernel with IPv6 support.
func ufwIPv6Enabled(defaultsPath, procIPv6Path string) (bool, error) {
	data, err := os.ReadFile(defaultsPath)
	if err != nil {
		return false, err
	}
	enabled := false
	for _, line := range strings.Split(string(data), "\n") {
		if m := ufwDefaultRegex.FindStringSubmatch(line); m != nil && strings.EqualFold(m[1], "IPV6") {
			enabled = strings.EqualFold(m[2], "yes")
		}
	}
	if !enabled {
		return false, nil
	}
	if _, err := os.Stat(procIPv6Path); err != nil {
		return false, nil
	}
	return true, nil
}

// ufwCommand returns the command and arguments that run ufw with args.
func (fc *FirewallCollector) ufwCommand(args ...string) (string, []string) {
	if fc.useSudo {
		return "sudo", append([]string{fc.ufwBinary}, args...)
	}
	return fc.ufwBinary, args
}

// ufw runs ufw with args through fc.run.
func (fc *FirewallCollector) ufw(ctx context.Context, args ...string) ([]byte, error) {
	name, cmdArgs := fc.ufwCommand(args...)
	return fc.run(ctx, name, cmdArgs...)
}

// currentTableRules reads the rules ufw has in place, per table.
func (fc *FirewallCollector) currentTableRules(ctx context.Context) ([]tableRule, error) {
	output, err := fc.ufw(ctx, "status")
	if err != nil {
		return nil, fmt.Errorf("failed to get UFW status: %w", err)
	}

	return fc.parseUFWRules(string(output))
}

// ufwRuleRegex matches an ALLOW row of `ufw status`. The "To" column carries an
// optional destination before the port/proto: when the destination is "any"
// ufw prints just the port (e.g. "22/tcp ALLOW Anywhere"), and when it is a
// specific host it prefixes it (e.g. "203.51.16.0 8081/tcp ALLOW 8.8.8.8").
// Group 1 captures that optional destination. The port/proto (group 2) is a
// port ("22/tcp"), a range ("1:65535/tcp"), or a comma-separated list of either
// ("80,443/tcp", "8000:8010,9000/tcp"), which ufw accepts as a multiport rule.
// A row that does not match is invisible to the sync: the rule is re-added
// every cycle and, worse, never deleted once the API drops it.
// The "(v6)" tags are stripped before matching.
var ufwRuleRegex = regexp.MustCompile(`^(?:(\S+)\s+)?([0-9]+(?::[0-9]+)?(?:,[0-9]+(?::[0-9]+)?)*/[a-z]+)\s+ALLOW\s+(.+)$`)

// parseUFWRules parses `ufw status` output into the rules of each ufw table.
// A row belongs to the IPv6 table when ufw tags it "(v6)" (it does so when an
// address is ::/0, printed as "Anywhere (v6)") or when an address is IPv6.
func (fc *FirewallCollector) parseUFWRules(output string) ([]tableRule, error) {
	var rules []tableRule

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "ALLOW") {
			continue
		}
		tagged := strings.Contains(line, "(v6)")
		line = strings.ReplaceAll(line, " (v6)", "")

		matches := ufwRuleRegex.FindStringSubmatch(line)
		if matches == nil {
			continue
		}
		port, protocol, ok := strings.Cut(matches[2], "/")
		if !ok {
			continue
		}

		rule, family, err := splitByFamily(FirewallRule{
			From:     strings.TrimSpace(matches[3]),
			To:       strings.TrimSpace(matches[1]),
			Protocol: protocol,
			Port:     port,
		})
		if err != nil {
			fc.logger.Warnf("Ignoring ufw rule %q: %v", line, err)
			continue
		}
		switch {
		case tagged && family == ipv4:
			fc.logger.Warnf("Ignoring ufw rule %q: IPv4 address in an IPv6 row", line)
			continue
		case tagged:
			family = ipv6
		case family == "":
			family = ipv4
		}
		rules = append(rules, tableRule{family: family, rule: rule})
	}

	return rules, nil
}

// desiredTableRules expands the API rules into the ufw table entries they
// should produce. "any" is a rule in each table ufw manages, so in the IPv4
// table only when ufw has IPv6 off; 0.0.0.0/0, ::/0 and specific addresses are
// a rule in their own family's table.
func (fc *FirewallCollector) desiredTableRules(apiRules []FirewallRule, ipv6On bool) []tableRule {
	var rules []tableRule
	for _, apiRule := range apiRules {
		rule, family, err := splitByFamily(apiRule)
		if err != nil {
			fc.logger.Warnf("Skipping API rule %s: %v", apiRule.String(), err)
			continue
		}
		switch family {
		case "":
			rules = append(rules, tableRule{ipv4, rule})
			if ipv6On {
				rules = append(rules, tableRule{ipv6, rule})
			}
		case ipv6:
			if !ipv6On {
				fc.logger.Warnf("Skipping API rule %s: IPv6 is disabled in ufw (IPV6 in /etc/default/ufw)", apiRule.String())
				continue
			}
			rules = append(rules, tableRule{ipv6, rule})
		default:
			rules = append(rules, tableRule{ipv4, rule})
		}
	}
	return rules
}

// key identifies a table rule for the diff.
func (fc *FirewallCollector) key(r tableRule) string {
	key := string(r.family) + " " + r.rule.String()
	if !fc.caseSensitive {
		key = strings.ToLower(key)
	}
	return key
}

// diffTableRules returns the entries of want missing from have, in want's
// order and without duplicates.
func (fc *FirewallCollector) diffTableRules(want, have []tableRule) []tableRule {
	seen := make(map[string]bool, len(have))
	for _, r := range have {
		seen[fc.key(r)] = true
	}
	var missing []tableRule
	for _, r := range want {
		k := fc.key(r)
		if !seen[k] {
			missing = append(missing, r)
			seen[k] = true
		}
	}
	return missing
}

// ufwRules turns table entries into the rules to pass to `ufw allow` or
// `ufw delete allow`. A rule with only catch-all addresses applies to every
// table when its addresses are "any", so:
//   - when it is listed for both tables it becomes a single "any" rule, as the
//     dashboard sends it;
//   - when it is listed for one table only, its source is spelled 0.0.0.0/0 or
//     ::/0, so ufw adds or deletes that table's entry and leaves the other
//     family alone. A rule with a specific address already pins the family.
func ufwRules(entries []tableRule) []FirewallRule {
	families := make(map[FirewallRule]int)
	for _, e := range entries {
		families[e.rule]++
	}
	var rules []FirewallRule
	emitted := make(map[FirewallRule]bool)
	for _, e := range entries {
		r := e.rule
		if r.From == "any" && r.To == "any" {
			if families[r] > 1 {
				if emitted[r] {
					continue
				}
				emitted[r] = true
			} else if e.family == ipv6 {
				r.From = "::/0"
			} else {
				r.From = "0.0.0.0/0"
			}
		}
		rules = append(rules, r)
	}
	return rules
}

// planSync compares the API rules with the rules in place, table by table, and
// returns the ufw rules to add and to delete.
func (fc *FirewallCollector) planSync(apiRules []FirewallRule, current []tableRule, ipv6On bool) (toAdd, toRemove []FirewallRule) {
	desired := fc.desiredTableRules(apiRules, ipv6On)
	return ufwRules(fc.diffTableRules(desired, current)), ufwRules(fc.diffTableRules(current, desired))
}

// SyncFirewallRules synchronizes UFW rules with API rules
func (fc *FirewallCollector) SyncFirewallRules(ctx context.Context, apiRulesJSON string) error {
	fc.logger.Info("Starting firewall rule synchronization")

	// Parse API rules
	var response FirewallResponse
	if err := json.Unmarshal([]byte(apiRulesJSON), &response); err != nil {
		return fmt.Errorf("failed to parse API rules JSON: %w", err)
	}

	apiRules := response.Firewall.Rules
	fc.logger.Infof("Found %d API rules", len(apiRules))

	// When the ufw setting cannot be read, assume IPv6 is on: at worst an
	// "any" rule is re-added every cycle (ufw skips what it already has),
	// whereas assuming it off would delete the IPv6 half of every "any" rule.
	ipv6On, err := fc.ipv6Enabled()
	if err != nil {
		fc.logger.Warnf("Could not read the ufw IPv6 setting, assuming IPv6 is enabled: %v", err)
		ipv6On = true
	}

	currentRules, err := fc.currentTableRules(ctx)
	if err != nil {
		return fmt.Errorf("failed to get current UFW rules: %w", err)
	}
	fc.logger.Infof("Found %d current UFW rules", len(currentRules))

	rulesToAdd, rulesToRemove := fc.planSync(apiRules, currentRules, ipv6On)

	fc.logger.Infof("Rules to add: %d", len(rulesToAdd))
	fc.logger.Infof("Rules to remove: %d", len(rulesToRemove))

	changesMade := false

	// Add new rules
	if len(rulesToAdd) > 0 {
		fc.logger.Info("Adding new UFW rules")
		for _, rule := range rulesToAdd {
			if err := fc.addUFWRule(ctx, rule); err != nil {
				fc.logger.Errorf("Failed to add rule %s: %v", rule.String(), err)
			} else {
				fc.logger.Infof("Added rule: %s", rule.String())
				changesMade = true
			}
		}
	}

	// Remove obsolete rules
	if len(rulesToRemove) > 0 {
		fc.logger.Info("Removing obsolete UFW rules")
		for _, rule := range rulesToRemove {
			if err := fc.removeUFWRule(ctx, rule); err != nil {
				fc.logger.Errorf("Failed to remove rule %s: %v", rule.String(), err)
			} else {
				fc.logger.Infof("Removed rule: %s", rule.String())
				changesMade = true
			}
		}
	}

	// Do NOT run `ufw reload` here. `ufw allow`/`ufw delete` already apply each
	// change to the running firewall: a new rule is appended live with a single
	// `iptables -A` (atomic, non-flushing), and ufw handles removals itself. An
	// explicit reload is redundant, and it is disruptive: `ufw reload` flushes
	// and rebuilds the whole ruleset, briefly dropping in-flight packets. With
	// the sync running on a short interval, that becomes periodic packet loss.
	if changesMade {
		fc.logger.Info("Firewall changes applied live (no ufw reload needed)")
	} else {
		fc.logger.Info("No changes made; firewall already in sync")
	}

	return nil
}

// addUFWRule adds a single UFW rule
func (fc *FirewallCollector) addUFWRule(ctx context.Context, rule FirewallRule) error {
	from := rule.From
	if from == "" {
		from = "any"
	}

	to := rule.To
	if to == "" {
		to = "any"
	}

	// UFW requires lowercase protocol names
	protocol := strings.ToLower(rule.Protocol)

	output, err := fc.ufw(ctx, "allow",
		"proto", protocol,
		"from", from,
		"to", to,
		"port", rule.Port)
	if err != nil {
		return fmt.Errorf("UFW command failed: %w, output: %s", err, string(output))
	}

	return nil
}

// removeUFWRule removes a single UFW rule
func (fc *FirewallCollector) removeUFWRule(ctx context.Context, rule FirewallRule) error {
	from := rule.From
	if from == "" {
		from = "any"
	}

	to := rule.To
	if to == "" {
		to = "any"
	}

	// UFW requires lowercase protocol names
	protocol := strings.ToLower(rule.Protocol)

	output, err := fc.ufw(ctx, "delete", "allow",
		"from", from,
		"to", to,
		"port", rule.Port,
		"proto", protocol)
	if err != nil {
		return fmt.Errorf("UFW delete command failed: %w, output: %s", err, string(output))
	}

	return nil
}

// GetFirewallStatus returns the current UFW status
func (fc *FirewallCollector) GetFirewallStatus(ctx context.Context) (string, error) {
	name, args := fc.ufwCommand("status", "numbered")
	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get UFW status: %w", err)
	}
	return string(output), nil
}
