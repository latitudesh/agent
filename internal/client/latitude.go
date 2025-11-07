package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/latitudesh/agent/internal/collectors"
	"github.com/sirupsen/logrus"
)

// LatitudeClient handles communication with Latitude.sh API
type LatitudeClient struct {
	httpClient  *http.Client
	apiEndpoint string
	bearerToken string
	projectID   string
	firewallID  string
	publicIP    string
	logger      *logrus.Logger
}

// PingRequest represents the request structure for the ping endpoint
type PingRequest struct {
	IPAddress string `json:"ip_address"`
}

// FirewallResponse represents the firewall rules response
type FirewallResponse struct {
	Firewall struct {
		Rules []FirewallRule `json:"rules"`
	} `json:"firewall"`
}

// FirewallRule represents a single firewall rule from the API
type FirewallRule struct {
	From     string `json:"from"`
	Protocol string `json:"protocol"`
	Port     string `json:"port"`
}

// NewLatitudeClient creates a new Latitude.sh API client
func NewLatitudeClient(bearerToken, apiEndpoint, projectID, firewallID, publicIP string, logger *logrus.Logger) *LatitudeClient {
	return &LatitudeClient{
		httpClient:  &http.Client{},
		apiEndpoint: apiEndpoint,
		bearerToken: bearerToken,
		projectID:   projectID,
		firewallID:  firewallID,
		publicIP:    publicIP,
		logger:      logger,
	}
}

// PingAndGetFirewallRules sends a ping to the API and retrieves firewall rules
func (lc *LatitudeClient) PingAndGetFirewallRules(ctx context.Context) (string, error) {
	lc.logger.Infof("Pinging Latitude.sh API at %s", lc.apiEndpoint)

	// Prepare request body
	pingReq := PingRequest{
		IPAddress: lc.publicIP,
	}

	reqBody, err := json.Marshal(pingReq)
	if err != nil {
		return "", fmt.Errorf("failed to marshal ping request: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "GET", lc.apiEndpoint, bytes.NewBuffer(reqBody))
	if err != nil {
		return "", fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	if lc.bearerToken != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", lc.bearerToken))
	} else if token := os.Getenv("LATITUDESH_AUTH_TOKEN"); token != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	}

	// Execute request
	resp, err := lc.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	// Check HTTP status
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	lc.logger.Info("Successfully retrieved firewall rules from API")
	return string(body), nil
}

// ValidateFirewallResponse validates that the API response contains expected firewall data
func (lc *LatitudeClient) ValidateFirewallResponse(responseBody string) error {
	var response FirewallResponse
	if err := json.Unmarshal([]byte(responseBody), &response); err != nil {
		return fmt.Errorf("invalid JSON response: %w", err)
	}

	// Check if firewall is disabled (empty array)
	if len(response.Firewall.Rules) == 0 {
		lc.logger.Warn("Firewall is disabled or no rules exist for this server")
		return nil
	}

	lc.logger.Infof("Validated firewall response with %d rules", len(response.Firewall.Rules))
	return nil
}

// GetProjectDetails retrieves project details (placeholder for future SDK integration)
func (lc *LatitudeClient) GetProjectDetails(ctx context.Context) error {
	lc.logger.Info("Project details retrieval - SDK integration pending")
	return nil
}

// HealthCheck performs a basic health check against the API
func (lc *LatitudeClient) HealthCheck(ctx context.Context) error {
	lc.logger.Info("Performing health check")

	req, err := http.NewRequestWithContext(ctx, "GET", lc.apiEndpoint, nil)
	if err != nil {
		return fmt.Errorf("failed to create health check request: %w", err)
	}

	if lc.bearerToken != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", lc.bearerToken))
	} else if token := os.Getenv("LATITUDESH_AUTH_TOKEN"); token != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	}

	resp, err := lc.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("health check request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("health check failed with status %d", resp.StatusCode)
	}

	lc.logger.Info("Health check passed")
	return nil
}

// GetFirewallRulesForDisplay formats firewall rules for display
func (lc *LatitudeClient) GetFirewallRulesForDisplay(responseBody string) ([]string, error) {
	var response FirewallResponse
	if err := json.Unmarshal([]byte(responseBody), &response); err != nil {
		return nil, fmt.Errorf("failed to parse firewall response: %w", err)
	}

	var displayRules []string
	for _, rule := range response.Firewall.Rules {
		from := rule.From
		if from == "" {
			from = "any"
		}
		protocol := rule.Protocol
		if protocol == "" {
			protocol = "any"
		}
		port := rule.Port
		if port == "" {
			port = "any"
		}

		displayRule := fmt.Sprintf("From: %s, To: any, Protocol: %s, Port: %s", from, protocol, port)
		displayRules = append(displayRules, displayRule)
	}

	return displayRules, nil
}

// SendHealthMetrics sends health metrics to the API
func (lc *LatitudeClient) SendHealthMetrics(ctx context.Context, health *collectors.ServerHealth) error {
	lc.logger.Info("Sending health metrics to Latitude.sh API")

	// Prepare request body
	reqBody, err := json.Marshal(health)
	if err != nil {
		return fmt.Errorf("failed to marshal health metrics: %w", err)
	}

	// Create HTTP request to health endpoint
	healthEndpoint := strings.Replace(lc.apiEndpoint, "/agent/ping", "/agent/health", 1)
	req, err := http.NewRequestWithContext(ctx, "POST", healthEndpoint, bytes.NewBuffer(reqBody))
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	if lc.bearerToken != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", lc.bearerToken))
	} else if token := os.Getenv("LATITUDESH_AUTH_TOKEN"); token != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	}

	// Execute request
	resp, err := lc.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	// Check HTTP status
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	lc.logger.Info("Successfully sent health metrics to API")
	return nil
}
