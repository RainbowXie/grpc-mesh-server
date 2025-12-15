package config

import (
	"fmt"
	"os"

	"github.com/spf13/viper"
)

// Config represents the server configuration
type Config struct {
	Server   ServerConfig   `mapstructure:"server"`
	Listener ListenerConfig `mapstructure:"listener"`
	Logging  LoggingConfig  `mapstructure:"logging"`
	Auth     AuthConfig     `mapstructure:"auth"`
}

// ServerConfig holds gRPC server settings
type ServerConfig struct {
	GRPCAddress    string `mapstructure:"grpc_address"`
	MetricsAddress string `mapstructure:"metrics_address"`
}

// ListenerConfig holds tunnel listener settings
type ListenerConfig struct {
	Address  string `mapstructure:"address"`
	CertFile string `mapstructure:"cert_file"`
	KeyFile  string `mapstructure:"key_file"`
	CAFile   string `mapstructure:"ca_file"`
}

// LoggingConfig holds logging settings
type LoggingConfig struct {
	Level string `mapstructure:"level"`
}

// AuthConfig holds authentication settings
type AuthConfig struct {
	Enabled       bool     `mapstructure:"enabled"`
	AllowedTokens []string `mapstructure:"allowed_tokens"`
}

// Load loads configuration from file
func Load(path string) (*Config, error) {
	v := viper.New()

	// Set defaults
	v.SetDefault("server.grpc_address", ":50051")
	v.SetDefault("server.metrics_address", ":9090")
	v.SetDefault("listener.address", ":8443")
	v.SetDefault("logging.level", "info")
	v.SetDefault("auth.enabled", false)

	if path != "" {
		v.SetConfigFile(path)
	} else {
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath("./config")
		v.AddConfigPath(".")
	}

	// Read environment variables
	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok && path == "" {
			// Config file not found but not required
		} else {
			return nil, fmt.Errorf("failed to read config: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Validate
	if cfg.Listener.CertFile != "" {
		if _, err := os.Stat(cfg.Listener.CertFile); err != nil {
			return nil, fmt.Errorf("cert file not found: %w", err)
		}
	}
	if cfg.Listener.KeyFile != "" {
		if _, err := os.Stat(cfg.Listener.KeyFile); err != nil {
			return nil, fmt.Errorf("key file not found: %w", err)
		}
	}

	return &cfg, nil
}
