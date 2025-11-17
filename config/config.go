package config

import (
	"fmt"
	"time"

	"github.com/spf13/viper"
)

// Config represents the complete application configuration
type Config struct {
	App      AppConfig      `mapstructure:"app"`
	AWS      AWSConfig      `mapstructure:"aws"`
	Timeouts TimeoutConfig  `mapstructure:"timeouts"`
	Network  NetworkConfig  `mapstructure:"network"`
	Tunnels  []TunnelConfig `mapstructure:"tunnels"`
}

// AppConfig holds application-level settings
type AppConfig struct {
	LogLevel string `mapstructure:"log_level"`
}

// AWSConfig holds AWS-specific configuration
type AWSConfig struct {
	Profile string `mapstructure:"profile"`
	Region  string `mapstructure:"region"`
	CLIPath string `mapstructure:"cli_path"`
}

// TimeoutConfig holds timeout-related settings
type TimeoutConfig struct {
	TunnelReady     time.Duration `mapstructure:"tunnel_ready"`
	TunnelReadyPoll time.Duration `mapstructure:"tunnel_ready_poll"`
	IdleCheck       time.Duration `mapstructure:"idle_check"`
	IdleTimeout     time.Duration `mapstructure:"idle_timeout"`
	Shutdown        time.Duration `mapstructure:"shutdown"`
	ShutdownPoll    time.Duration `mapstructure:"shutdown_poll"`
	Connection      time.Duration `mapstructure:"connection"`
}

// NetworkConfig holds network-related settings
type NetworkConfig struct {
	ListenAddress    string `mapstructure:"listen_address"`
	TunnelPortOffset int    `mapstructure:"tunnel_port_offset"`
}

// TunnelConfig holds configuration for each tunnel
type TunnelConfig struct {
	Name            string `mapstructure:"name"`
	LocalPort       int    `mapstructure:"local_port"`
	RDSEndpoint     string `mapstructure:"rds_endpoint"`
	RDSPort         int    `mapstructure:"rds_port"`
	InstanceID      string `mapstructure:"instance_id"`       // Explicit instance ID (e.g., i-xxxxx)
	InstancePattern string `mapstructure:"instance_pattern"`  // Wildcard pattern (e.g., app-server-*)
	Description     string `mapstructure:"description"`
	AWSProfile      string `mapstructure:"aws_profile"`       // Optional: per-tunnel AWS profile
	AWSRegion       string `mapstructure:"aws_region"`        // Optional: per-tunnel AWS region
}

// Load loads configuration from file and environment variables
func Load(configPath string, profile, region string) (*Config, error) {
	// Set defaults first
	setDefaults()

	// Configure viper
	viper.SetConfigType("yaml")

	if configPath != "" {
		viper.SetConfigFile(configPath)
	} else {
		viper.SetConfigName("config")
		viper.AddConfigPath(".")
		viper.AddConfigPath("$HOME/.lazy-ssm")
	}

	// Read config file (optional - will use defaults if not found)
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("error reading config file: %w", err)
		}
		// Config file not found - using defaults
	}

	// Unmarshal into config struct
	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("error unmarshaling config: %w", err)
	}

	// Override with CLI flags if provided
	if profile != "" {
		cfg.AWS.Profile = profile
	}
	if region != "" {
		cfg.AWS.Region = region
	}

	// Validate configuration
	if err := Validate(&cfg); err != nil {
		return nil, fmt.Errorf("configuration validation failed: %w", err)
	}

	return &cfg, nil
}

// setDefaults sets default values for configuration
func setDefaults() {
	// App defaults
	viper.SetDefault("app.log_level", "info")

	// AWS defaults
	viper.SetDefault("aws.profile", "")
	viper.SetDefault("aws.region", "")
	viper.SetDefault("aws.cli_path", "aws")

	// Timeout defaults
	viper.SetDefault("timeouts.tunnel_ready", "15s")
	viper.SetDefault("timeouts.tunnel_ready_poll", "500ms")
	viper.SetDefault("timeouts.idle_check", "30s")
	viper.SetDefault("timeouts.idle_timeout", "5m")
	viper.SetDefault("timeouts.shutdown", "10s")
	viper.SetDefault("timeouts.shutdown_poll", "100ms")
	viper.SetDefault("timeouts.connection", "1s")

	// Network defaults
	viper.SetDefault("network.listen_address", "localhost")
	viper.SetDefault("network.tunnel_port_offset", 10000)

	// Empty tunnels array by default
	viper.SetDefault("tunnels", []TunnelConfig{})
}
