package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var Version = "dev"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version information",
	Long:  `Show the current version of lazy-ssm.`,
	RunE:  ShowVersion,
}

func ShowVersion(_ *cobra.Command, _ []string) error {
	fmt.Println("lazy-ssm version:", Version)
	return nil
}
