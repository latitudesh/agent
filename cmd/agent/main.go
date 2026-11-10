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

	// SIGINT/SIGTERM cancel ctx, which aborts an API request in flight: a
	// stalled call would otherwise hold `systemctl stop` until the request
	// timeout, once for the health check and again for the first cycle.
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

	// Perform initial health check
	if err := latitudeClient.HealthCheck(ctx); err != nil {
		log.WithError(err).Error("Initial health check failed")
		// Don't exit immediately, allow retry in main loop
	}

	// Parse interval
	interval, err := time.ParseDuration(cfg.Agent.Interval)
	if err != nil {
		log.Fatalf("Invalid interval %s: %v", cfg.Agent.Interval, err)
	}

	log.Infof("Starting agent with %s interval", interval)

	// Main execution loop
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Run immediately on startup
	if err := runCollection(ctx, latitudeClient, firewallCollector, cfg, log); err != nil && ctx.Err() == nil {
		log.WithError(err).Error("Initial collection failed")
	}

	// Main loop
	for {
		select {
		case <-ctx.Done():
			log.LogAgentStop("received shutdown signal")
			return
		case <-ticker.C:
			if err := runCollection(ctx, latitudeClient, firewallCollector, cfg, log); err != nil && ctx.Err() == nil {
				log.WithError(err).Error("Collection cycle failed")
				// Continue running despite errors
			}
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

	// Synchronize firewall rules if firewall collector is enabled. Once rules
	// are being applied, a shutdown signal does not cancel the ufw commands:
	// each is short, and killing one mid-change could leave the host with half
	// of a rule set. A cycle that was signalled before this point stops here.
	if firewallCollector != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
		ctx := context.WithoutCancel(ctx)
		collectorStart := time.Now()
		err := firewallCollector.SyncFirewallRules(ctx, rulesJSON)
		duration := time.Since(collectorStart)

		log.LogCollectorRun("firewall", duration.String(), err == nil, err)

		if err != nil {
			return fmt.Errorf("firewall synchronization failed: %w", err)
		}

		// Display final UFW status
		status, err := firewallCollector.GetFirewallStatus(ctx)
		if err != nil {
			log.WithError(err).Warn("Failed to get final UFW status")
		} else {
			log.Info("Final UFW status:")
			log.Info(status)
		}
	}

	duration := time.Since(start)
	log.WithComponent("agent").Infof("Collection cycle completed successfully in %s", duration)

	return nil
}
