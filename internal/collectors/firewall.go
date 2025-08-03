package collectors

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// FirewallRule represents a firewall rule
type FirewallRule struct {
	From     string `json:"from"`
	Protocol string `json:"protocol"`
	Port     string `json:"port"`
}

// String returns a normalized string representation of the rule
func (r FirewallRule) String() string {
	from := r.From
	if from == "" {
		from = "any"
	}
	protocol := r.Protocol
	if protocol == "" {
		protocol = "any"
	}
	port := r.Port
	if port == "" {
		port = "any"
	}
	return fmt.Sprintf("From: %s, Protocol: %s, Port: %s", from, protocol, port)
}

// FirewallResponse represents the API response structure
type FirewallResponse struct {
	Firewall struct {
		Rules []FirewallRule `json:"rules"`
	} `json:"firewall"`
}

// FirewallCollector handles firewall rule collection and synchronization
type FirewallCollector struct {
	ufwBinary     string
	caseSensitive bool
	logger        *logrus.Logger
}

// NewFirewallCollector creates a new firewall collector
func NewFirewallCollector(ufwBinary string, caseSensitive bool, logger *logrus.Logger) *FirewallCollector {
	return &FirewallCollector{
		ufwBinary:     ufwBinary,
		caseSensitive: caseSensitive,
		logger:        logger,
	}
}

// GetCurrentUFWRules retrieves current UFW rules from the system
func (fc *FirewallCollector) GetCurrentUFWRules(ctx context.Context) ([]FirewallRule, error) {
	cmd := exec.CommandContext(ctx, "sudo", fc.ufwBinary, "status")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get UFW status: %w", err)
	}

	return fc.parseUFWRules(string(output))
}

// parseUFWRules parses UFW status output into FirewallRule structs
func (fc *FirewallCollector) parseUFWRules(output string) ([]FirewallRule, error) {
	var rules []FirewallRule
	lines := strings.Split(output, "\n")

	// Regular expression to match UFW rules
	// Example: "22/tcp                     ALLOW       Anywhere"
	ruleRegex := regexp.MustCompile(`^([0-9]+/[a-z]+)\s+ALLOW\s+(.+)$`)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "ALLOW") && !strings.Contains(line, "(v6)") {
			matches := ruleRegex.FindStringSubmatch(line)
			if len(matches) >= 3 {
				portProto := matches[1]
				from := strings.TrimSpace(matches[2])

				// Parse port and protocol
				parts := strings.Split(portProto, "/")
				if len(parts) != 2 {
					continue
				}

				port := parts[0]
				protocol := parts[1]

				// Normalize "from" field
				if from == "Anywhere" {
					from = "any"
				}

				rules = append(rules, FirewallRule{
					From:     from,
					Protocol: protocol,
					Port:     port,
				})
			}
		}
	}

	return rules, nil
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

	// Reload UFW if changes were made
	if changesMade {
		fc.logger.Info("Reloading UFW to apply changes")
		if err := fc.reloadUFW(ctx); err != nil {
			return fmt.Errorf("failed to reload UFW: %w", err)
		}
	} else {
		fc.logger.Info("No changes made, skipping UFW reload")
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
	if from == "any" {
		from = "any"
	}

	cmd := exec.CommandContext(ctx, "sudo", fc.ufwBinary, "allow", 
		"proto", rule.Protocol, 
		"from", from, 
		"to", "any", 
		"port", rule.Port)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("UFW command failed: %w, output: %s", err, string(output))
	}

	return nil
}

// removeUFWRule removes a single UFW rule
func (fc *FirewallCollector) removeUFWRule(ctx context.Context, rule FirewallRule) error {
	from := rule.From
	if from == "any" {
		from = "any"
	}

	cmd := exec.CommandContext(ctx, "sudo", fc.ufwBinary, "delete", "allow",
		"from", from,
		"to", "any",
		"port", rule.Port,
		"proto", rule.Protocol)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("UFW delete command failed: %w, output: %s", err, string(output))
	}

	return nil
}

// reloadUFW reloads the UFW firewall
func (fc *FirewallCollector) reloadUFW(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "sudo", fc.ufwBinary, "reload")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("UFW reload failed: %w, output: %s", err, string(output))
	}
	return nil
}

// GetFirewallStatus returns the current UFW status
func (fc *FirewallCollector) GetFirewallStatus(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "sudo", fc.ufwBinary, "status", "numbered")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get UFW status: %w", err)
	}
	return string(output), nil
}

// SaveRulesToFile saves firewall rules to a JSON file with timestamp
func (fc *FirewallCollector) SaveRulesToFile(rules string, outputFile string) error {
	// Add timestamp
	rulesWithTimestamp := rules + fmt.Sprintf("\nLast updated: %s", time.Now().Format(time.RFC3339))
	
	return os.WriteFile(outputFile, []byte(rulesWithTimestamp), 0644)
}