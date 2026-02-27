package cmd

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show service status",
	Long:  `Show the current status of the lazy-ssm brew service.`,
	RunE:  cmdStatus,
}

func cmdStatus(_ *cobra.Command, _ []string) error {
	out, err := exec.Command("brew", "services", "list").Output()
	if err != nil {
		fmt.Println("Could not run 'brew services list'. Is Homebrew installed?")
		return nil
	}

	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "lazy-ssm") {
			fmt.Println(line)
			return nil
		}
	}

	fmt.Println("lazy-ssm is not registered as a brew service.")
	fmt.Println("To install: brew services start lazy-ssm")
	return nil
}
