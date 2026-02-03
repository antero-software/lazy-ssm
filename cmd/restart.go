package cmd

import (
	"time"

	"github.com/antero-software/lazy-ssm/daemon"
	"github.com/spf13/cobra"
)

var restartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the daemon",
	Long: `Restart the daemon by stopping and then starting it.

This is equivalent to running 'lazy-ssm stop && lazy-ssm start'.`,
	RunE: cmdRestart,
}

func cmdRestart(_ *cobra.Command, _ []string) error {
	d := daemon.New()

	if d.IsRunning() {
		if err := cmdStop(nil, nil); err != nil {
			return err
		}
		// Wait a moment for cleanup
		time.Sleep(1 * time.Second)
	}

	return cmdStart(nil, nil)
}
