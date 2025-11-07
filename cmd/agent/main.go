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

const Version = "1.0.0"

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

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set up signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

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

	var healthCollector *collectors.HealthCollector
	if cfg.Health.Enabled {
		healthCollector = collectors.NewHealthCollector(
			cfg.Latitude.ProjectID, // ou server ID
			Version,
			log.Logger,
		)
		log.Info("Health monitoring enabled")
	}

	// Parse interval
	interval, err := time.ParseDuration(cfg.Agent.Interval)
	if err != nil {
		log.Fatalf("Invalid interval %s: %v", cfg.Agent.Interval, err)
	}

	var healthInterval time.Duration
	if cfg.Health.Enabled {
		healthInterval, err = time.ParseDuration(cfg.Health.Interval)
		if err != nil {
			log.Fatalf("Invalid health interval %s: %v", cfg.Health.Interval, err)
		}
	}

	log.Infof("Starting agent with firewall interval: %s, health interval: %s", interval, healthInterval)

	// Main execution loop
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var healthTicker *time.Ticker
	if healthCollector != nil {
		healthTicker = time.NewTicker(healthInterval)
		defer healthTicker.Stop()

		// Run health collection immediately on startup
		if err := runHealthCollection(ctx, latitudeClient, healthCollector, log); err != nil {
			log.WithError(err).Error("Initial health collection failed")
		}
	}

	// Run immediately on startup
	if err := runCollection(ctx, latitudeClient, firewallCollector, cfg, log); err != nil {
		log.WithError(err).Error("Initial collection failed")
	}

	// Main loop
	for {
		select {
		case <-ctx.Done():
			log.LogAgentStop("context cancelled")
			return
		case sig := <-sigChan:
			log.LogAgentStop(fmt.Sprintf("received signal: %s", sig))
			cancel()
			return
		case <-ticker.C:
			// Firewall sync
			if err := runCollection(ctx, latitudeClient, firewallCollector, cfg, log); err != nil {
				log.WithError(err).Error("Collection cycle failed")
			}
		case <-healthTicker.C:
			// Health monitoring (only if enabled)
			if healthCollector != nil {
				if err := runHealthCollection(ctx, latitudeClient, healthCollector, log); err != nil {
					log.WithError(err).Error("Health collection cycle failed")
				}
			}
		}
	}
}

// runHealthCollection performs a single health collection cycle
func runHealthCollection(ctx context.Context, latitudeClient *client.LatitudeClient,
	healthCollector *collectors.HealthCollector, log *logger.Logger) error {

	start := time.Now()
	log.WithComponent("health").Info("Starting health collection cycle")

	// Collect health metrics
	health, err := healthCollector.Collect(ctx)
	if err != nil {
		return fmt.Errorf("failed to collect health metrics: %w", err)
	}

	log.WithComponent("health").Infof("Overall health status: %s", health.OverallStatus)

	// Log component statuses
	log.WithComponent("health").Debugf("Hardware: %s, Network: %s, Disk: %s, Memory: %s, CPU: %s",
		health.Hardware.Status, health.Network.Status, health.Disk.Status,
		health.Memory.Status, health.CPU.Status)

	// Send metrics to API
	if err := latitudeClient.SendHealthMetrics(ctx, health); err != nil {
		log.WithError(err).Warn("Failed to send health metrics to API (will retry next cycle)")
		// Don't return error - we'll retry in next cycle
	}

	duration := time.Since(start)
	log.WithComponent("health").Infof("Health collection cycle completed in %s", duration)

	return nil
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

	// Save rules to file
	if err := firewallCollector.SaveRulesToFile(rulesJSON, cfg.Firewall.OutputFile); err != nil {
		log.WithError(err).Warn("Failed to save rules to file")
	}

	// Synchronize firewall rules if firewall collector is enabled
	if firewallCollector != nil {
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
