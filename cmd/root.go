package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/antero-software/lazy-ssm/config"
	"github.com/antero-software/lazy-ssm/daemon"
	"github.com/antero-software/lazy-ssm/tunnel"
	"github.com/spf13/cobra"
)

var (
	configPath string
	awsProfile string
	awsRegion  string
	manager    *tunnel.TunnelManager
)

// RootCmd represents the base command when called without any subcommands
var RootCmd = &cobra.Command{
	Use:   "lazy-ssm",
	Short: "Lazy SSM Tunnel Manager - On-demand AWS SSM tunnels",
	Long: `Lazy SSM Tunnel Manager provides on-demand AWS Systems Manager
tunnels to RDS databases through EC2 bastion instances.

Tunnels are automatically started when connections are detected and
closed after a period of inactivity to optimize resource usage.`,
	RunE: runTunnelManager,
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() error {
	return RootCmd.Execute()
}

func init() {
	// Add persistent flags available to all commands
	RootCmd.PersistentFlags().StringVarP(&configPath, "config", "c", "", "Path to configuration file (default: ./config.yaml)")
	RootCmd.PersistentFlags().StringVarP(&awsProfile, "profile", "p", "", "AWS profile to use (overrides config file)")
	RootCmd.PersistentFlags().StringVarP(&awsRegion, "region", "r", "", "AWS region to use (overrides config file)")

	// Add subcommands
	RootCmd.AddCommand(startCmd)
	RootCmd.AddCommand(stopCmd)
	RootCmd.AddCommand(restartCmd)
	RootCmd.AddCommand(reloadCmd)
	RootCmd.AddCommand(statusCmd)
	RootCmd.AddCommand(versionCmd)
}

// runTunnelManager is the main function that runs the tunnel manager
func runTunnelManager(_ *cobra.Command, _ []string) error {
	// Load configuration
	cfg, err := config.Load(configPath, awsProfile, awsRegion)
	if err != nil {
		return err
	}

	log.Printf("Loaded %d tunnel(s) from configuration", len(cfg.Tunnels))

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create daemon
	d := daemon.New()

	// Start daemon (if not already running)
	if err := d.Start(ctx); err != nil {
		return err
	}
	defer func() {
		_ = d.Stop()
	}()

	// Create tunnel manager
	manager = tunnel.NewTunnelManager(cfg)

	// Register daemon command handlers
	d.RegisterHandler(daemon.CmdReload, func(ctx context.Context) daemon.Response {
		if err := manager.Reload(configPath, awsProfile, awsRegion); err != nil {
			return daemon.Response{
				Success: false,
				Message: fmt.Sprintf("Failed to reload: %v", err),
			}
		}
		return daemon.Response{
			Success: true,
			Message: "Configuration reloaded successfully",
		}
	})

	d.RegisterHandler(daemon.CmdStatus, func(_ context.Context) daemon.Response {
		status := manager.GetStatus()
		return daemon.Response{
			Success: true,
			Message: "Daemon is running",
			Data:    status,
		}
	})

	d.RegisterHandler(daemon.CmdStop, func(_ context.Context) daemon.Response {
		log.Println("Received stop command")
		cancel()
		return daemon.Response{
			Success: true,
			Message: "Daemon stopping",
		}
	})

	d.RegisterHandler(daemon.CmdPing, func(_ context.Context) daemon.Response {
		return daemon.Response{
			Success: true,
			Message: "pong",
		}
	})

	// Handle shutdown signals
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down...")
		cancel()
	}()

	log.Println("SSM Tunnel Manager starting...")

	// Start tunnel manager
	if err := manager.Start(ctx); err != nil && err != context.Canceled {
		return err
	}

	return nil
}
