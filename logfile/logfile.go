// Package logfile finds the lazy-ssm service log file.
package logfile

import (
	"os"
	"path/filepath"
)

// Path returns the first log file path that exists, probing Homebrew
// var/log locations before falling back to /tmp.
func Path() string {
	candidates := []string{}

	if prefix := os.Getenv("HOMEBREW_PREFIX"); prefix != "" {
		candidates = append(candidates, filepath.Join(prefix, "var/log/lazy-ssm.log"))
	}
	candidates = append(candidates,
		"/home/linuxbrew/.linuxbrew/var/log/lazy-ssm.log", // Linux Homebrew
		"/opt/homebrew/var/log/lazy-ssm.log",              // Apple Silicon
		"/usr/local/var/log/lazy-ssm.log",                 // Intel Mac / Linux fallback
		"/tmp/lazy-ssm.log",
	)

	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	// Nothing exists yet — return the most likely Homebrew path so the error
	// message is helpful.
	if _, err := os.Stat("/home/linuxbrew/.linuxbrew"); err == nil {
		return "/home/linuxbrew/.linuxbrew/var/log/lazy-ssm.log"
	}
	if _, err := os.Stat("/opt/homebrew"); err == nil {
		return "/opt/homebrew/var/log/lazy-ssm.log"
	}
	return "/usr/local/var/log/lazy-ssm.log"
}
