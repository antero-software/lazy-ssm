package cmd

import (
	"fmt"

	"github.com/antero-software/lazy-ssm/daemon"
	"github.com/spf13/cobra"
)

var reloadCmd = &cobra.Command{
	Use:   "reload",
	Short: "Reload configuration without stopping tunnels",
	Long: `Reload configuration from disk and restart tunnels without stopping the daemon.

This allows you to update tunnel configurations without downtime for the service itself.
The daemon will:
1. Load the new configuration file
2. Stop all existing tunnels gracefully
3. Create new tunnels based on the updated configuration
4. Start the new tunnels`,
	RunE: cmdReload,
}

func cmdReload(_ *cobra.Command, _ []string) error {
	d := daemon.New()

	if !d.IsRunning() {
		return fmt.Errorf("daemon is not running")
	}

	fmt.Println("Reloading configuration...")

	resp, err := daemon.SendCommand(daemon.CmdReload)
	if err != nil {
		return fmt.Errorf("failed to send reload command: %w", err)
	}

	if !resp.Success {
		return fmt.Errorf("reload failed: %s", resp.Message)
	}

	fmt.Println(resp.Message)
	return nil
}
