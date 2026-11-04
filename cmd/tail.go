package cmd

import (
	"bufio"
	"fmt"
	"os"

	"github.com/antero-software/lazy-ssm/logfile"
	"github.com/spf13/cobra"
)

var tailLines int

var tailCmd = &cobra.Command{
	Use:   "tail",
	Short: "Show recent daemon log output",
	Long: `Show the last N lines from the daemon log file.

Defaults to 25 lines. Use --lines to change the count.`,
	RunE: cmdTail,
}

func init() {
	tailCmd.Flags().IntVarP(&tailLines, "lines", "n", 25, "Number of lines to show")
}

func cmdTail(_ *cobra.Command, _ []string) error {
	path := logfile.Path()

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("No log file found at %s\n", path)
			fmt.Println("Has the service been started? Run: brew services start lazy-ssm")
			return nil
		}
		return fmt.Errorf("failed to open log file: %w", err)
	}
	defer f.Close()

	// Ring buffer to keep only the last tailLines lines.
	buf := make([]string, tailLines)
	pos := 0
	count := 0

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		buf[pos%tailLines] = scanner.Text()
		pos++
		count++
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading log file: %w", err)
	}

	if count == 0 {
		fmt.Println("Log file is empty.")
		return nil
	}

	start := pos
	n := tailLines
	if count < tailLines {
		start = 0
		n = count
	}

	for i := 0; i < n; i++ {
		fmt.Println(buf[(start+i)%tailLines])
	}

	return nil
}
