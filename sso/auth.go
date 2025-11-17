package sso

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"
)

// AuthChecker handles AWS SSO authentication checks
type AuthChecker struct {
	cliPath string
}

// NewAuthChecker creates a new SSO auth checker
func NewAuthChecker(cliPath string) *AuthChecker {
	return &AuthChecker{
		cliPath: cliPath,
	}
}

// CheckAndLogin checks if the AWS profile is authenticated and initiates SSO login if needed
func (a *AuthChecker) CheckAndLogin(ctx context.Context, profile, region string) error {
	// Check if profile needs authentication
	needsAuth, err := a.needsAuthentication(ctx, profile, region)
	if err != nil {
		return fmt.Errorf("failed to check authentication status: %w", err)
	}

	if !needsAuth {
		// Already authenticated
		return nil
	}

	// Check if this is an SSO profile
	isSSO, err := a.isSSOProfile(ctx, profile)
	if err != nil {
		return fmt.Errorf("failed to check if profile is SSO: %w", err)
	}

	if !isSSO {
		return fmt.Errorf("profile '%s' requires authentication but is not configured for SSO", profile)
	}

	// Initiate SSO login
	log.Printf("[SSO] Profile '%s' requires authentication. Opening SSO login page...", profile)
	if err := a.initiateLogin(ctx, profile); err != nil {
		return fmt.Errorf("SSO login failed: %w", err)
	}

	// Verify authentication succeeded
	needsAuth, err = a.needsAuthentication(ctx, profile, region)
	if err != nil {
		return fmt.Errorf("failed to verify authentication: %w", err)
	}

	if needsAuth {
		return fmt.Errorf("SSO login completed but profile still not authenticated")
	}

	log.Printf("[SSO] Profile '%s' successfully authenticated", profile)
	return nil
}

// needsAuthentication checks if a profile needs authentication
func (a *AuthChecker) needsAuthentication(ctx context.Context, profile, region string) (bool, error) {
	args := []string{"sts", "get-caller-identity", "--output", "json"}

	if profile != "" {
		args = append([]string{"--profile", profile}, args...)
	}

	if region != "" {
		args = append([]string{"--region", region}, args...)
	}

	cmdCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, a.cliPath, args...)
	output, err := cmd.CombinedOutput()

	if err != nil {
		// Check if error is due to expired credentials
		errStr := string(output)
		if strings.Contains(errStr, "Token has expired") ||
			strings.Contains(errStr, "security token included in the request is expired") ||
			strings.Contains(errStr, "The SSO session associated with this profile has expired") ||
			strings.Contains(errStr, "Error loading SSO Token") {
			return true, nil // Needs authentication
		}

		// Check for missing credentials
		if strings.Contains(errStr, "Unable to locate credentials") ||
			strings.Contains(errStr, "No credentials found") {
			return true, nil // Needs authentication
		}

		// Other error
		return false, fmt.Errorf("failed to check credentials: %w (output: %s)", err, errStr)
	}

	// Successfully got caller identity - authenticated
	return false, nil
}

// isSSOProfile checks if a profile is configured for SSO
func (a *AuthChecker) isSSOProfile(ctx context.Context, profile string) (bool, error) {
	args := []string{"configure", "get", "sso_start_url"}

	if profile != "" {
		args = append(args, "--profile", profile)
	}

	cmdCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, a.cliPath, args...)
	output, err := cmd.CombinedOutput()

	if err != nil {
		// If we can't get sso_start_url, it's not an SSO profile
		return false, nil
	}

	// If we got a URL, it's an SSO profile
	url := strings.TrimSpace(string(output))
	return url != "", nil
}

// initiateLogin starts the AWS SSO login process
func (a *AuthChecker) initiateLogin(ctx context.Context, profile string) error {
	args := []string{"sso", "login"}

	if profile != "" {
		args = append(args, "--profile", profile)
	}

	log.Printf("[SSO] Running: aws %s", strings.Join(args, " "))

	cmd := exec.CommandContext(ctx, a.cliPath, args...)

	// Inherit stdin/stdout/stderr so user can see the SSO login process
	cmd.Stdin = nil
	cmd.Stdout = log.Writer()
	cmd.Stderr = log.Writer()

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("SSO login command failed: %w", err)
	}

	return nil
}

// CheckProfile is a convenience method to check and login for a specific profile
func CheckProfile(ctx context.Context, cliPath, profile, region string) error {
	// Skip check if no profile specified (will use default)
	if profile == "" {
		return nil
	}

	checker := NewAuthChecker(cliPath)
	return checker.CheckAndLogin(ctx, profile, region)
}
