package cmd

import (
	"fmt"

	"github.com/antero-software/lazy-ssm/daemon"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show daemon status",
	Long: `Show the current status of the daemon including:
- Process ID (PID)
- Number of configured tunnels
- Status of each tunnel (ports, endpoints, active connections)`,
	RunE: cmdStatus,
}

func cmdStatus(_ *cobra.Command, _ []string) error {
	d := daemon.New()

	if !d.IsRunning() {
		fmt.Println("Daemon is not running")
		return nil
	}

	pid, _ := d.GetPID()
	fmt.Printf("Daemon is running with PID %d\n", pid)

	// Try to get detailed status
	resp, err := daemon.SendCommand(daemon.CmdStatus)
	if err != nil {
		fmt.Printf("Warning: could not get detailed status: %v\n", err)
		return nil
	}

	if resp.Success && resp.Data != nil {
		if tunnelCount, ok := resp.Data["tunnel_count"].(float64); ok {
			fmt.Printf("Tunnels: %d\n", int(tunnelCount))
		}
		if tunnels, ok := resp.Data["tunnels"].([]interface{}); ok {
			fmt.Println("\nActive tunnels:")
			for _, t := range tunnels {
				if tunnelData, ok := t.(map[string]interface{}); ok {
					fmt.Printf("  - %s (%s)\n", tunnelData["name"], tunnelData["description"])
					fmt.Printf("    Local port: %v\n", tunnelData["local_port"])
					fmt.Printf("    RDS endpoint: %v\n", tunnelData["rds_endpoint"])
					fmt.Printf("    Active connections: %v\n", tunnelData["connections"])
				}
			}
		}
	}

	return nil
}
