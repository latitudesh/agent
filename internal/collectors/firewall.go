package collectors

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"regexp"
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
	from := normalizeAddr(r.From)
	to := normalizeAddr(r.To)
	protocol := strings.ToLower(strings.TrimSpace(r.Protocol))
	if protocol == "" {
		protocol = "any"
	}
	port := strings.TrimSpace(r.Port)
	if port == "" {
		port = "any"
	}
	return fmt.Sprintf("From: %s, To: %s, Protocol: %s, Port: %s", from, to, protocol, port)
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
// A /0 prefix is deliberately not collapsed to "any": ufw status prints
// "Anywhere" for both a dual-stack "any" rule and a single-family 0.0.0.0/0 or
// ::/0 rule, so equating them could keep access open in an address family the
// API no longer allows. parseUFWRules tells the two apart by the "(v6)" rows.
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
}

// NewFirewallCollector creates a new firewall collector
func NewFirewallCollector(ufwBinary string, caseSensitive bool, logger *logrus.Logger) *FirewallCollector {
	return &FirewallCollector{
		ufwBinary:     ufwBinary,
		caseSensitive: caseSensitive,
		logger:        logger,
		run:           execRunner,
		useSudo:       os.Geteuid() != 0,
	}
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

// GetCurrentUFWRules retrieves current UFW rules from the system
func (fc *FirewallCollector) GetCurrentUFWRules(ctx context.Context) ([]FirewallRule, error) {
	output, err := fc.ufw(ctx, "status")
	if err != nil {
		return nil, fmt.Errorf("failed to get UFW status: %w", err)
	}

	return fc.parseUFWRules(string(output))
}

// parseUFWRules parses UFW status output into FirewallRule structs
func (fc *FirewallCollector) parseUFWRules(output string) ([]FirewallRule, error) {
	var rules []FirewallRule
	lines := strings.Split(output, "\n")

	// Regular expression to match UFW rules. The "To" column carries an
	// optional destination before the port/proto: when the destination is
	// "any" ufw prints just the port (e.g. "22/tcp ALLOW Anywhere"), and
	// when it is a specific host it prefixes it (e.g. "203.51.16.0 8081/tcp
	// ALLOW 8.8.8.8"). Group 1 captures that optional destination. The
	// port/proto (group 2) is either a single port ("22/tcp") or a port
	// range ("1:65535/tcp"); without the range branch, range rules fail to
	// parse and the sync re-adds + reloads them every cycle.
	ruleRegex := regexp.MustCompile(`^(?:(\S+)\s+)?([0-9]+(?::[0-9]+)?/[a-z]+)\s+ALLOW\s+(.+)$`)

	// ufw tags a row "(v6)" only when the rule's address on that side is
	// ::/0, so these rows are either the IPv6 half of a dual-stack "any" rule
	// (which also has an IPv4 row) or a rule that applies to IPv6 only. Only
	// the former can be dropped: skipping the latter means a ::/0 rule, or an
	// "any" rule to an IPv6 destination, is never seen, so it is re-added on
	// every sync and never removed once the API stops returning it.
	var v6Rows []FirewallRule
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "ALLOW") {
			continue
		}
		v6 := strings.Contains(line, "(v6)")
		if v6 {
			line = strings.ReplaceAll(line, " (v6)", "")
		}
		matches := ruleRegex.FindStringSubmatch(line)
		if len(matches) < 4 {
			continue
		}
		to := strings.TrimSpace(matches[1])
		portProto := matches[2]
		from := strings.TrimSpace(matches[3])

		// Parse port and protocol
		parts := strings.Split(portProto, "/")
		if len(parts) != 2 {
			continue
		}

		rule := FirewallRule{
			From:     normalizeAddr(from),
			To:       normalizeAddr(to),
			Protocol: parts[1],
			Port:     parts[0],
		}
		if v6 {
			v6Rows = append(v6Rows, rule)
			continue
		}
		rules = append(rules, rule)
	}

	for _, rule := range v6Rows {
		if rule.From == "any" && rule.To == "any" {
			if containsRule(rules, rule) {
				continue // IPv6 half of a dual-stack "any" rule
			}
			rule.From = "::/0"
		}
		rules = append(rules, rule)
	}

	return rules, nil
}

// containsRule reports whether rules holds a rule with the same address,
// protocol and port as r.
func containsRule(rules []FirewallRule, r FirewallRule) bool {
	for _, c := range rules {
		if c.String() == r.String() {
			return true
		}
	}
	return false
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

	// Get current UFW rules
	currentRules, err := fc.GetCurrentUFWRules(ctx)
	if err != nil {
		return fmt.Errorf("failed to get current UFW rules: %w", err)
	}
	fc.logger.Infof("Found %d current UFW rules", len(currentRules))

	// Convert to string sets for comparison
	currentRuleStrings := fc.rulesToStringSet(currentRules)
	apiRuleStrings := fc.rulesToStringSet(apiRules)

	// Find rules to add and remove
	rulesToAdd := fc.findRulesToAdd(currentRuleStrings, apiRuleStrings, apiRules)
	rulesToRemove := fc.findRulesToRemove(currentRuleStrings, apiRuleStrings, currentRules)

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

// rulesToStringSet converts rules to a set of normalized strings
func (fc *FirewallCollector) rulesToStringSet(rules []FirewallRule) map[string]FirewallRule {
	ruleSet := make(map[string]FirewallRule)
	for _, rule := range rules {
		key := rule.String()
		if !fc.caseSensitive {
			key = strings.ToLower(key)
		}
		ruleSet[key] = rule
	}
	return ruleSet
}

// findRulesToAdd finds rules that exist in API but not in current UFW
func (fc *FirewallCollector) findRulesToAdd(currentSet, apiSet map[string]FirewallRule, apiRules []FirewallRule) []FirewallRule {
	var rulesToAdd []FirewallRule
	for _, rule := range apiRules {
		key := rule.String()
		if !fc.caseSensitive {
			key = strings.ToLower(key)
		}
		if _, exists := currentSet[key]; !exists {
			rulesToAdd = append(rulesToAdd, rule)
		}
	}
	return rulesToAdd
}

// findRulesToRemove finds rules that exist in current UFW but not in API
func (fc *FirewallCollector) findRulesToRemove(currentSet, apiSet map[string]FirewallRule, currentRules []FirewallRule) []FirewallRule {
	var rulesToRemove []FirewallRule
	for _, rule := range currentRules {
		key := rule.String()
		if !fc.caseSensitive {
			key = strings.ToLower(key)
		}
		if _, exists := apiSet[key]; !exists {
			rulesToRemove = append(rulesToRemove, rule)
		}
	}
	return rulesToRemove
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
