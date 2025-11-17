package config

import (
	"fmt"
	"strings"
)

// Validate validates the configuration
func Validate(cfg *Config) error {
	// Validate app config
	if err := validateApp(&cfg.App); err != nil {
		return fmt.Errorf("app config: %w", err)
	}

	// Validate AWS config
	if err := validateAWS(&cfg.AWS); err != nil {
		return fmt.Errorf("aws config: %w", err)
	}

	// Validate network config
	if err := validateNetwork(&cfg.Network); err != nil {
		return fmt.Errorf("network config: %w", err)
	}

	// Validate tunnels
	if len(cfg.Tunnels) == 0 {
		return fmt.Errorf("at least one tunnel must be configured")
	}

	for i, tunnel := range cfg.Tunnels {
		if err := validateTunnel(&tunnel); err != nil {
			return fmt.Errorf("tunnel %d (%s): %w", i, tunnel.Name, err)
		}
	}

	// Check for duplicate ports
	if err := validateNoDuplicatePorts(cfg.Tunnels); err != nil {
		return err
	}

	return nil
}

// validateApp validates application configuration
func validateApp(app *AppConfig) error {
	validLogLevels := []string{"debug", "info", "warn", "error"}
	isValid := false
	for _, level := range validLogLevels {
		if strings.ToLower(app.LogLevel) == level {
			isValid = true
			break
		}
	}
	if !isValid {
		return fmt.Errorf("invalid log_level '%s', must be one of: %v", app.LogLevel, validLogLevels)
	}
	return nil
}

// validateAWS validates AWS configuration
func validateAWS(aws *AWSConfig) error {
	if aws.CLIPath == "" {
		return fmt.Errorf("cli_path cannot be empty")
	}
	// Note: profile and region are optional - AWS CLI will use defaults if not set
	return nil
}

// validateNetwork validates network configuration
func validateNetwork(net *NetworkConfig) error {
	if net.ListenAddress == "" {
		return fmt.Errorf("listen_address cannot be empty")
	}
	if net.TunnelPortOffset <= 0 {
		return fmt.Errorf("tunnel_port_offset must be positive")
	}
	if net.TunnelPortOffset > 50000 {
		return fmt.Errorf("tunnel_port_offset too large (max 50000)")
	}
	return nil
}

// validateTunnel validates a single tunnel configuration
func validateTunnel(tunnel *TunnelConfig) error {
	if tunnel.Name == "" {
		return fmt.Errorf("name is required")
	}

	if tunnel.LocalPort <= 0 || tunnel.LocalPort > 65535 {
		return fmt.Errorf("local_port must be between 1 and 65535")
	}

	if tunnel.LocalPort < 1024 {
		return fmt.Errorf("local_port should be >= 1024 (ports below 1024 require root)")
	}

	if tunnel.RDSEndpoint == "" {
		return fmt.Errorf("rds_endpoint is required")
	}

	if tunnel.RDSPort <= 0 || tunnel.RDSPort > 65535 {
		return fmt.Errorf("rds_port must be between 1 and 65535")
	}

	// Must have either instance_id or instance_pattern, but not both
	hasInstanceID := tunnel.InstanceID != ""
	hasInstancePattern := tunnel.InstancePattern != ""

	if !hasInstanceID && !hasInstancePattern {
		return fmt.Errorf("either instance_id or instance_pattern is required")
	}

	if hasInstanceID && hasInstancePattern {
		return fmt.Errorf("cannot specify both instance_id and instance_pattern")
	}

	// Validate instance_id format if provided
	if hasInstanceID && !strings.HasPrefix(tunnel.InstanceID, "i-") {
		return fmt.Errorf("instance_id must start with 'i-'")
	}

	// Validate instance_pattern if provided
	if hasInstancePattern && tunnel.InstancePattern == "" {
		return fmt.Errorf("instance_pattern cannot be empty")
	}

	if tunnel.Description == "" {
		return fmt.Errorf("description is required")
	}

	return nil
}

// validateNoDuplicatePorts ensures no two tunnels use the same local port
func validateNoDuplicatePorts(tunnels []TunnelConfig) error {
	portMap := make(map[int]string)
	for _, tunnel := range tunnels {
		if existingTunnel, exists := portMap[tunnel.LocalPort]; exists {
			return fmt.Errorf("duplicate local_port %d used by tunnels '%s' and '%s'",
				tunnel.LocalPort, existingTunnel, tunnel.Name)
		}
		portMap[tunnel.LocalPort] = tunnel.Name
	}
	return nil
}
