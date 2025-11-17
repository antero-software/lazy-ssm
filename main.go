package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/antero-software/lazy-ssm/config"
	"github.com/antero-sofware/lazy-ssm/tunnel"
	"github.com/spf13/cobra"
)

var (
	configPath string
	awsProfile string
	awsRegion  string
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "lazy-ssm",
		Short: "Lazy SSM Tunnel Manager - On-demand AWS SSM tunnels",
		Long: `Lazy SSM Tunnel Manager provides on-demand AWS Systems Manager
tunnels to RDS databases through EC2 bastion instances.

Tunnels are automatically started when connections are detected and
closed after a period of inactivity to optimize resource usage.`,
		RunE: runTunnelManager,
	}

	// Add flags
	rootCmd.Flags().StringVarP(&configPath, "config", "c", "", "Path to configuration file (default: ./config.yaml)")
	rootCmd.Flags().StringVarP(&awsProfile, "profile", "p", "", "AWS profile to use (overrides config file)")
	rootCmd.Flags().StringVarP(&awsRegion, "region", "r", "", "AWS region to use (overrides config file)")

	if err := rootCmd.Execute(); err != nil {
		log.Fatalf("Error: %v", err)
	}
}

func runTunnelManager(cmd *cobra.Command, args []string) error {
	// Load configuration
	cfg, err := config.Load(configPath, awsProfile, awsRegion)
	if err != nil {
		return err
	}

	log.Printf("Loaded %d tunnel(s) from configuration", len(cfg.Tunnels))

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down...")
		cancel()
	}()

	// Start tunnel manager
	manager := tunnel.NewTunnelManager(cfg)
	log.Println("SSM Tunnel Manager starting...")

	if err := manager.Start(ctx); err != nil {
		return err
	}

	return nil
}
