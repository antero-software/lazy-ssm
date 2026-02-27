package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"

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

// logFilePath returns the first log file path that exists, probing Homebrew
// var/log locations before falling back to /tmp.
func logFilePath() string {
	candidates := []string{}

	if prefix := os.Getenv("HOMEBREW_PREFIX"); prefix != "" {
		candidates = append(candidates, filepath.Join(prefix, "var/log/lazy-ssm.log"))
	}
	candidates = append(candidates,
		"/opt/homebrew/var/log/lazy-ssm.log", // Apple Silicon
		"/usr/local/var/log/lazy-ssm.log",    // Intel
		"/tmp/lazy-ssm.log",                  // fallback
	)

	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	// Nothing exists yet — return the most likely Homebrew path so the error
	// message is helpful.
	if _, err := os.Stat("/opt/homebrew"); err == nil {
		return "/opt/homebrew/var/log/lazy-ssm.log"
	}
	return "/usr/local/var/log/lazy-ssm.log"
}

func cmdTail(_ *cobra.Command, _ []string) error {
	path := logFilePath()

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
