package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/latitudesh/agent/internal/client"
	"github.com/latitudesh/agent/internal/collectors"
	"github.com/latitudesh/agent/internal/config"
	"github.com/latitudesh/agent/internal/logger"
)

// Version is set at build time via -ldflags "-X main.Version=...". It has to be
// a var: the linker cannot rewrite a const.
var Version = "dev"

func main() {
	// Parse command line flags
	var (
		configPath  = flag.String("config", config.DefaultConfigPath(), "Path to configuration file")
		version     = flag.Bool("version", false, "Show version and exit")
		checkConfig = flag.Bool("check-config", false, "Check configuration and exit")
	)
	flag.Parse()

	if *version {
		fmt.Printf("Latitude.sh Agent v%s\n", Version)
		os.Exit(0)
	}

	// Load configuration
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	if *checkConfig {
		fmt.Println("Configuration is valid")
		os.Exit(0)
	}

	// Initialize logger
	log, err := logger.New(cfg.Logging.Level, cfg.Logging.Format)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}

	log.LogAgentStart(Version, *configPath)

	// SIGINT/SIGTERM cancel ctx, and with it the API request in flight. Waiting
	// for a stalled request to time out instead (30s for the health check, 30s
	// more for the first cycle) runs past systemd's stop timeout and the agent
	// is SIGKILLed, so every restart or upgrade of the host waits ~90s.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Initialize Latitude.sh API client
	latitudeClient := client.NewLatitudeClient(
		cfg.Latitude.BearerToken,
		cfg.Latitude.APIEndpoint,
		cfg.Latitude.ProjectID,
		cfg.Latitude.FirewallID,
		cfg.Latitude.PublicIP,
		log.Logger,
	)

	// Initialize firewall collector
	var firewallCollector *collectors.FirewallCollector
	if cfg.Firewall.Enabled {
		firewallCollector = collectors.NewFirewallCollector(
			cfg.Firewall.UFWBinary,
			cfg.Firewall.CaseSensitive,
			log.Logger,
		)
	}

	// Perform initial health check; a failure is retried by the loop below.
	if err := latitudeClient.HealthCheck(ctx); err != nil && ctx.Err() == nil {
		log.WithError(err).Error("Initial health check failed")
	}

	log.Infof("Starting agent with %s interval", cfg.Agent.SyncInterval)

	runLoop(ctx, cfg.Agent.SyncInterval, func(ctx context.Context) error {
		return runCollection(ctx, latitudeClient, firewallCollector, cfg, log)
	}, func(err error) {
		log.WithError(err).Error("Collection cycle failed")
	})

	log.LogAgentStop("received shutdown signal")
}

// runLoop runs cycle right away and then on every tick, until ctx is
// cancelled. Cancelling ctx interrupts the cycle in flight; the error that
// causes is part of the shutdown, not a failed cycle, so it isn't reported.
func runLoop(ctx context.Context, interval time.Duration, cycle func(context.Context) error, onError func(error)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for ctx.Err() == nil {
		if err := cycle(ctx); err != nil && ctx.Err() == nil {
			onError(err)
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
}

// runCollection performs a single collection cycle
func runCollection(ctx context.Context, latitudeClient *client.LatitudeClient, firewallCollector *collectors.FirewallCollector, cfg *config.Config, log *logger.Logger) error {
	start := time.Now()
	log.WithComponent("agent").Info("Starting collection cycle")

	// Fetch firewall rules from API
	rulesJSON, err := latitudeClient.PingAndGetFirewallRules(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch firewall rules: %w", err)
	}

	if err := latitudeClient.VerifyFirewallIdentity(rulesJSON); err != nil {
		return fmt.Errorf("firewall identity check failed, not applying rules: %w", err)
	}

	// Validate API response
	if err := latitudeClient.ValidateFirewallResponse(rulesJSON); err != nil {
		return fmt.Errorf("API response validation failed: %w", err)
	}

	// Display received rules
	displayRules, err := latitudeClient.GetFirewallRulesForDisplay(rulesJSON)
	if err != nil {
		log.WithError(err).Warn("Failed to format rules for display")
	} else {
		log.Info("Firewall rules received from the server:")
		for _, rule := range displayRules {
			log.Info(rule)
		}
	}

	// Synchronize firewall rules if firewall collector is enabled. A shutdown
	// that arrived while the rules were fetched stops the cycle here; once the
	// sync has started it runs to the end, since cancelling would kill a ufw
	// command half way through and leave only part of the rule set applied.
	if firewallCollector != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
		collectorStart := time.Now()
		err := firewallCollector.SyncFirewallRules(context.WithoutCancel(ctx), rulesJSON)
		duration := time.Since(collectorStart)

		log.LogCollectorRun("firewall", duration.String(), err == nil, err)

		if err != nil {
			return fmt.Errorf("firewall synchronization failed: %w", err)
		}

		// Display final UFW status, unless a shutdown arrived during the sync:
		// it is only informational, and with ctx cancelled it would just log
		// a failure for a sync that succeeded.
		if ctx.Err() == nil {
			status, err := firewallCollector.GetFirewallStatus(ctx)
			if err != nil {
				log.WithError(err).Warn("Failed to get final UFW status")
			} else {
				log.Info("Final UFW status:")
				log.Info(status)
			}
		}
	}

	duration := time.Since(start)
	log.WithComponent("agent").Infof("Collection cycle completed successfully in %s", duration)

	return nil
}
