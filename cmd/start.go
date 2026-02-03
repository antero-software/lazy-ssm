package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/antero-software/lazy-ssm/config"
	"github.com/antero-software/lazy-ssm/daemon"
	"github.com/spf13/cobra"
)

var daemonMode bool

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the tunnel manager as a daemon",
	Long: `Start the tunnel manager as a background daemon process.

By default, the process will fork and run in the background. Use --daemon=false
to run in the foreground for debugging purposes.`,
	RunE: cmdStart,
}

func init() {
	startCmd.Flags().BoolVarP(&daemonMode, "daemon", "d", true, "Run as daemon (default: true)")
}

func cmdStart(_ *cobra.Command, _ []string) error {
	d := daemon.New()

	// Check if already running
	if d.IsRunning() {
		pid, _ := d.GetPID()
		return fmt.Errorf("daemon already running with PID %d", pid)
	}

	if daemonMode {
		// First, validate configuration before forking
		fmt.Println("Validating configuration...")
		if _, err := config.Load(configPath, awsProfile, awsRegion); err != nil {
			return fmt.Errorf("configuration validation failed: %w\n\nRun with --daemon=false to see full error details", err)
		}

		// Fork process to run in background
		fmt.Println("Starting daemon...")

		// Build command args without the "start" command
		args := []string{}
		if configPath != "" {
			args = append(args, "--config", configPath)
		}
		if awsProfile != "" {
			args = append(args, "--profile", awsProfile)
		}
		if awsRegion != "" {
			args = append(args, "--region", awsRegion)
		}

		// Start the process in background using exec.Command for better control
		executable, err := os.Executable()
		if err != nil {
			return fmt.Errorf("failed to get executable path: %w", err)
		}

		cmd := exec.Command(executable, args...)

		// Create log file for daemon output
		logFile, err := os.OpenFile("/tmp/lazy-ssm.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return fmt.Errorf("failed to create log file: %w", err)
		}
		defer logFile.Close()

		// Redirect output to log file
		cmd.Stdout = logFile
		cmd.Stderr = logFile

		// Start the process
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("failed to start daemon: %w", err)
		}

		// Wait a moment for daemon to start and write PID
		time.Sleep(2 * time.Second)

		// Verify it started
		if !d.IsRunning() {
			// Try to read last few lines of log for error details
			logFile.Seek(-1024, io.SeekEnd)
			logData := make([]byte, 1024)
			n, _ := logFile.Read(logData)

			return fmt.Errorf("daemon failed to start\n\nCheck /tmp/lazy-ssm.log for details. Last output:\n%s", string(logData[:n]))
		}

		pid, _ := d.GetPID()
		fmt.Printf("Daemon started with PID %d\n", pid)
		fmt.Printf("Logs: /tmp/lazy-ssm.log\n")
	} else {
		// Run in foreground
		return runTunnelManager(nil, nil)
	}

	return nil
}
