// Package awscli runs the AWS CLI. Every AWS call in lazy-ssm goes through
// the CLI so it uses the same profiles, SSO cache and config as the user.
package awscli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const defaultTimeout = 10 * time.Second

// Runner runs the AWS CLI with args and returns stdout.
type Runner interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
}

// CLI runs the real AWS CLI binary.
type CLI struct {
	Path, Profile, Region string
	Timeout               time.Duration // 0 means 10s
}

// Run adds --region and --profile when set, runs the CLI with a time-out,
// and returns stdout. A failure returns *Error.
func (c CLI) Run(ctx context.Context, args ...string) ([]byte, error) {
	timeout := c.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, c.Path, buildArgs(c.Profile, c.Region, args)...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, &Error{Args: args, Stderr: stderr.String(), Err: err}
	}
	return stdout.Bytes(), nil
}

// Error is a failed AWS CLI call. Stderr can contain the caller ARN, which
// usually contains the user's email address, so Error() never includes it.
type Error struct {
	Args   []string
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	name := "aws"
	for i := 0; i < len(e.Args) && i < 2; i++ {
		name += " " + e.Args[i]
	}
	return fmt.Sprintf("%s: %v", name, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// IsAccessDenied reports whether err is an AWS CLI call that AWS refused
// because the caller lacks permission.
func IsAccessDenied(err error) bool {
	var e *Error
	if !errors.As(err, &e) {
		return false
	}
	for _, s := range []string{"AccessDenied", "UnauthorizedOperation", "not authorized"} {
		if strings.Contains(e.Stderr, s) {
			return true
		}
	}
	return false
}

// buildArgs prepends --region then --profile when set, in the same order the
// rest of lazy-ssm has always used.
func buildArgs(profile, region string, args []string) []string {
	out := make([]string, 0, len(args)+4)
	if region != "" {
		out = append(out, "--region", region)
	}
	if profile != "" {
		out = append(out, "--profile", profile)
	}
	return append(out, args...)
}
