// Package diagnose runs read-only AWS checks that explain why a tunnel
// cannot connect.
package diagnose

import (
	"fmt"
	"io"
	"strings"
)

// Status is the outcome of one check.
type Status int

const (
	Pass Status = iota
	Warn
	Fail
	CouldNotCheck
)

func (s Status) String() string {
	switch s {
	case Pass:
		return "PASS"
	case Warn:
		return "WARN"
	case Fail:
		return "FAIL"
	default:
		return "COULD NOT CHECK"
	}
}

// Result is one check's outcome.
type Result struct {
	Check   string
	Status  Status
	Message string
	Fix     string // "" when none
}

// Report is every check's result for one tunnel, in check order.
type Report struct {
	Tunnel      string // tunnel Name
	Description string // tunnel Description, for the header
	Results     []Result
}

// AllPassedMessage is shown when a connection failed but every check passed.
const AllPassedMessage = "All checks passed. The cause may be outside what lazy-ssm can check (network ACLs, routing, database credentials)."

// Headline returns the result to show the user: the first Fail, else the
// first Warn. ok is false when there is neither.
func (r Report) Headline() (Result, bool) {
	for _, want := range []Status{Fail, Warn} {
		for _, res := range r.Results {
			if res.Status == want {
				return res, true
			}
		}
	}
	return Result{}, false
}

// HasFail reports whether any check failed.
func (r Report) HasFail() bool {
	for _, res := range r.Results {
		if res.Status == Fail {
			return true
		}
	}
	return false
}

// Lines returns one log line per result.
func (r Report) Lines() []string {
	lines := make([]string, 0, len(r.Results))
	for _, res := range r.Results {
		line := fmt.Sprintf("diagnose: %s: %s", res.Check, res.Status)
		if res.Message != "" {
			line += ": " + res.Message
		}
		if res.Fix != "" {
			line += " Fix: " + res.Fix
		}
		lines = append(lines, line)
	}
	return lines
}

// Write prints the report for the terminal.
func (r Report) Write(w io.Writer) {
	header := r.Tunnel
	if r.Description != "" {
		header += " (" + r.Description + ")"
	}
	fmt.Fprintln(w, header)
	indent := strings.Repeat(" ", 18)
	for _, res := range r.Results {
		line := res.Check
		if res.Message != "" {
			line += ": " + res.Message
		}
		fmt.Fprintf(w, "  %-16s%s\n", res.Status, line)
		if res.Fix != "" {
			fmt.Fprintf(w, "%sFix: %s\n", indent, res.Fix)
		}
	}
}
