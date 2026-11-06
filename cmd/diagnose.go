package cmd

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/antero-software/lazy-ssm/awscli"
	"github.com/antero-software/lazy-ssm/config"
	"github.com/antero-software/lazy-ssm/diagnose"
	"github.com/spf13/cobra"
)

var diagnoseCmd = &cobra.Command{
	Use:   "diagnose [tunnel-name]",
	Short: "Check why a tunnel cannot connect",
	Long: `Run read-only AWS checks for a tunnel (or every tunnel) and print
where the connection breaks: AWS login, instance, SSM, database, VPC and
firewall. Shows no pop-ups and never starts an AWS login.

Exits with code 1 when any check fails.`,
	Args:         cobra.MaximumNArgs(1),
	SilenceUsage: true,
	RunE:         cmdDiagnose,
}

func cmdDiagnose(_ *cobra.Command, args []string) error {
	cfg, err := config.Load(configPath, awsProfile, awsRegion)
	if err != nil {
		return err
	}
	name := ""
	if len(args) == 1 {
		name = args[0]
	}
	tunnels, err := selectTunnels(cfg, name)
	if err != nil {
		return err
	}

	failed := false
	for i, tc := range tunnels {
		if i > 0 {
			fmt.Println()
		}
		profile := cfg.ProfileFor(tc)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		rep := diagnose.Run(ctx, tc, diagnose.Deps{
			AWS:        awscli.CLI{Path: cfg.AWS.CLIPath, Profile: profile, Region: cfg.RegionFor(tc)},
			LookupHost: net.DefaultResolver.LookupHost,
			Profile:    profile,
		})
		cancel()
		rep.Write(os.Stdout)
		failed = failed || rep.HasFail()
	}
	if failed {
		os.Exit(1)
	}
	return nil
}

// selectTunnels returns the tunnel called name, or every tunnel when name is "".
func selectTunnels(cfg *config.Config, name string) ([]config.TunnelConfig, error) {
	if name == "" {
		return cfg.Tunnels, nil
	}
	names := make([]string, 0, len(cfg.Tunnels))
	for _, tc := range cfg.Tunnels {
		if tc.Name == name {
			return []config.TunnelConfig{tc}, nil
		}
		names = append(names, tc.Name)
	}
	return nil, fmt.Errorf("no tunnel named '%s'. Tunnels: %s", name, strings.Join(names, ", "))
}
