package cmd

import (
	"fmt"
	"syscall"
	"time"

	"github.com/antero-software/lazy-ssm/daemon"
	"github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the running daemon",
	Long: `Stop the running daemon gracefully.

This command will:
1. Try graceful shutdown via control socket
2. Wait for active connections to finish (up to 10 seconds)
3. Fall back to SIGTERM if needed
4. Force kill (SIGKILL) if timeout is exceeded`,
	RunE: cmdStop,
}

func cmdStop(_ *cobra.Command, _ []string) error {
	d := daemon.New()

	if !d.IsRunning() {
		return fmt.Errorf("daemon is not running")
	}

	fmt.Println("Stopping daemon...")

	// Try graceful shutdown first via control socket
	resp, err := daemon.SendCommand(daemon.CmdStop)
	if err == nil && resp.Success {
		fmt.Println(resp.Message)

		// Wait for process to exit
		pid, _ := d.GetPID()
		if err := daemon.WaitForProcessExit(pid, 10*time.Second); err != nil {
			fmt.Println("Warning: graceful shutdown timeout, forcing...")
			if killErr := daemon.KillProcess(pid, syscall.SIGKILL); killErr != nil {
				return fmt.Errorf("failed to force kill: %w", killErr)
			}
		}

		fmt.Println("Daemon stopped")
		return nil
	}

	// If control socket fails, try SIGTERM
	pid, err := d.GetPID()
	if err != nil {
		return fmt.Errorf("failed to get daemon PID: %w", err)
	}

	fmt.Printf("Sending SIGTERM to process %d...\n", pid)
	if err := daemon.KillProcess(pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to stop daemon: %w", err)
	}

	// Wait for process to exit
	if err := daemon.WaitForProcessExit(pid, 10*time.Second); err != nil {
		fmt.Println("Warning: graceful shutdown timeout, forcing...")
		if killErr := daemon.KillProcess(pid, syscall.SIGKILL); killErr != nil {
			return fmt.Errorf("failed to force kill: %w", killErr)
		}
	}

	fmt.Println("Daemon stopped")
	return nil
}
