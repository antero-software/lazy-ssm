package cmd

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/antero-software/lazy-ssm/config"
	"github.com/antero-software/lazy-ssm/tunnel"
	"github.com/spf13/cobra"
)

var (
	configPath string
	awsProfile string
	awsRegion  string
)

// RootCmd represents the base command when called without any subcommands
var RootCmd = &cobra.Command{
	Use:   "lazy-ssm",
	Short: "Lazy SSM Tunnel Manager - On-demand AWS SSM tunnels",
	Long: `Lazy SSM Tunnel Manager provides on-demand AWS Systems Manager
tunnels to RDS databases through EC2 bastion instances.

Tunnels are automatically started when connections are detected and
closed after a period of inactivity to optimize resource usage.

Intended to be run as a service via: brew services start lazy-ssm`,
	RunE: runTunnelManager,
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() error {
	return RootCmd.Execute()
}

func init() {
	RootCmd.PersistentFlags().StringVarP(&configPath, "config", "c", "", "Path to configuration file (default: ~/.lazy-ssm/config.yaml)")
	RootCmd.PersistentFlags().StringVarP(&awsProfile, "profile", "p", "", "AWS profile to use (overrides config file)")
	RootCmd.PersistentFlags().StringVarP(&awsRegion, "region", "r", "", "AWS region to use (overrides config file)")

	RootCmd.AddCommand(statusCmd)
	RootCmd.AddCommand(tailCmd)
	RootCmd.AddCommand(versionCmd)
}

func runTunnelManager(_ *cobra.Command, _ []string) error {
	cfg, err := config.Load(configPath, awsProfile, awsRegion)
	if err != nil {
		return err
	}

	log.Printf("Loaded %d tunnel(s) from configuration", len(cfg.Tunnels))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down...")
		cancel()
	}()

	log.Println("SSM Tunnel Manager starting...")

	m := tunnel.NewTunnelManager(cfg)
	if err := m.Start(ctx); err != nil && err != context.Canceled {
		return err
	}

	return nil
}
