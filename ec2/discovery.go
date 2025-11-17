package ec2

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// InstanceDiscovery handles EC2 instance discovery
type InstanceDiscovery struct {
	cliPath string
	profile string
	region  string
}

// NewInstanceDiscovery creates a new instance discovery helper
func NewInstanceDiscovery(cliPath, profile, region string) *InstanceDiscovery {
	return &InstanceDiscovery{
		cliPath: cliPath,
		profile: profile,
		region:  region,
	}
}

// FindInstance finds an EC2 instance ID matching the given pattern
// Pattern can include wildcards (e.g., "app-server-*")
func (d *InstanceDiscovery) FindInstance(ctx context.Context, pattern string) (string, error) {
	// Build AWS CLI command to describe instances
	args := []string{"ec2", "describe-instances"}

	// Add filters for running instances matching the name pattern
	args = append(args,
		"--filters",
		fmt.Sprintf("Name=tag:Name,Values=%s", pattern),
		"Name=instance-state-name,Values=running",
		"--query", "Reservations[0].Instances[0].InstanceId",
		"--output", "text",
	)

	// Add profile if specified
	if d.profile != "" {
		args = append([]string{"--profile", d.profile}, args...)
	}

	// Add region if specified
	if d.region != "" {
		args = append([]string{"--region", d.region}, args...)
	}

	// Create command with timeout
	cmdCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, d.cliPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to query EC2 instances: %w (output: %s)", err, string(output))
	}

	instanceID := strings.TrimSpace(string(output))

	// Check if we got a valid instance ID
	if instanceID == "" || instanceID == "None" {
		return "", fmt.Errorf("no running instance found matching pattern: %s", pattern)
	}

	if !strings.HasPrefix(instanceID, "i-") {
		return "", fmt.Errorf("invalid instance ID returned: %s", instanceID)
	}

	return instanceID, nil
}

// FindInstanceWithSSM finds an EC2 instance that is also SSM-managed
func (d *InstanceDiscovery) FindInstanceWithSSM(ctx context.Context, pattern string) (string, error) {
	// Build AWS CLI command to describe instances with SSM filter
	args := []string{"ec2", "describe-instances"}

	// Add filters for running instances matching the name pattern with SSM agent
	args = append(args,
		"--filters",
		fmt.Sprintf("Name=tag:Name,Values=%s", pattern),
		"Name=instance-state-name,Values=running",
		"--query", "Reservations[].Instances[?not_null(IamInstanceProfile)].[InstanceId]",
		"--output", "text",
	)

	// Add profile if specified
	if d.profile != "" {
		args = append([]string{"--profile", d.profile}, args...)
	}

	// Add region if specified
	if d.region != "" {
		args = append([]string{"--region", d.region}, args...)
	}

	// Create command with timeout
	cmdCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, d.cliPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to query EC2 instances: %w (output: %s)", err, string(output))
	}

	instanceID := strings.TrimSpace(string(output))

	// Check if we got a valid instance ID
	if instanceID == "" || instanceID == "None" {
		return "", fmt.Errorf("no running SSM-managed instance found matching pattern: %s", pattern)
	}

	// If multiple instances, take the first one
	if strings.Contains(instanceID, "\n") {
		instanceID = strings.Split(instanceID, "\n")[0]
		instanceID = strings.TrimSpace(instanceID)
	}

	if !strings.HasPrefix(instanceID, "i-") {
		return "", fmt.Errorf("invalid instance ID returned: %s", instanceID)
	}

	return instanceID, nil
}
