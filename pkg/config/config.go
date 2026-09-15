package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/viper"
)

// Config represents the complete server configuration.
//
// This structure defines all configurable aspects of the gRPC-Mesh server,
// including network addresses, TLS certificates, logging levels, and
// authentication settings.
//
// Configuration can be loaded from:
//   - YAML files (config.yaml by default)
//   - Environment variables (automatically mapped)
//   - Programmatic defaults
//
// Example YAML:
//
//	server:
//	  grpc_address: ":50051"
//	  metrics_address: ":9090"
//	listener:
//	  address: ":8443"
//	  cert_file: "certs/server.crt"
//	  key_file: "certs/server.key"
//	logging:
//	  level: "info"
//	auth:
//	  enabled: true
//	  allowed_tokens: ["secret-token-1", "secret-token-2"]
type Config struct {
	Server   ServerConfig   `mapstructure:"server"`
	Listener ListenerConfig `mapstructure:"listener"`
	Logging  LoggingConfig  `mapstructure:"logging"`
	Auth     AuthConfig     `mapstructure:"auth"`
}

// ServerConfig holds gRPC server and metrics endpoint settings.
//
// These settings control the addresses where the server's gRPC API and
// Prometheus metrics endpoints listen.
type ServerConfig struct {
	// GRPCAddress is the TCP address for the gRPC InvokePlane service.
	// Format: ":port" or "host:port" (e.g., ":50051" or "0.0.0.0:50051")
	GRPCAddress string `mapstructure:"grpc_address"`

	// MetricsAddress is the TCP address for the Prometheus metrics HTTP endpoint.
	// Format: ":port" or "host:port" (e.g., ":9090")
	MetricsAddress string `mapstructure:"metrics_address"`
}

// ListenerConfig holds TLS tunnel listener settings.
//
// These settings configure the listener that accepts TLS+Yamux connections
// from mesh nodes. All fields are required for production use.
type ListenerConfig struct {
	// Address is the TCP address to listen for incoming mesh node connections.
	// Format: ":port" or "host:port" (e.g., ":8443")
	Address string `mapstructure:"address"`

	// CertFile is the path to the TLS certificate file (PEM format).
	// The certificate will be automatically reloaded when it changes on disk.
	CertFile string `mapstructure:"cert_file"`

	// KeyFile is the path to the TLS private key file (PEM format).
	KeyFile string `mapstructure:"key_file"`

	// CAFile is the optional path to the CA certificate for client verification.
	// Used for mutual TLS (mTLS) authentication.
	CAFile string `mapstructure:"ca_file"`
}

// LoggingConfig holds logging settings for structured logging.
//
// The server uses Zap for structured logging with configurable levels.
type LoggingConfig struct {
	// Level is the minimum log level to output.
	// Valid values: "debug", "info", "warn", "error", "fatal"
	// Default: "info"
	Level string `mapstructure:"level"`
}

// AuthConfig holds authentication and authorization settings.
//
// When authentication is enabled, mesh nodes must provide a valid token
// in their handshake message.
type AuthConfig struct {
	// Enabled controls whether authentication is required.
	// If false, all connections are accepted without token validation.
	// Default: false
	Enabled bool `mapstructure:"enabled"`

	// AllowedTokens is the list of valid authentication tokens.
	// Nodes must present one of these tokens during handshake.
	// Only used when Enabled is true.
	//
	// Tokens in this list are NOT bound to node identities: any holder of a
	// token may claim any node_id and replace that node's session. Prefer
	// NodeTokens for per-node credentials.
	AllowedTokens []string `mapstructure:"allowed_tokens"`

	// NodeTokens maps node_id to the token issued to that node. When non-
	// empty it takes precedence over AllowedTokens, and a handshake is only
	// accepted when the claimed node_id matches the token's binding.
	//
	// Example:
	//
	//	auth:
	//	  enabled: true
	//	  node_tokens:
	//	    node-a: token-for-node-a
	//	    node-b: token-for-node-b
	NodeTokens map[string]string `mapstructure:"node_tokens"`
}

// Load loads server configuration from a file or uses defaults.
//
// Configuration loading follows this precedence (highest to lowest):
//  1. Values from the configuration file (if provided)
//  2. Environment variables (e.g., SERVER_GRPC_ADDRESS)
//  3. Default values
//
// If path is empty, the function searches for "config.yaml" in:
//   - ./config/
//   - ./
//
// If no config file is found and path is empty, default values are used.
//
// Parameters:
//   - path: Path to YAML configuration file, or empty string for auto-detection
//
// Returns:
//   - Loaded and validated configuration
//   - Error if:
//   - File parsing fails
//   - Required certificate files are missing or invalid
//   - Configuration validation fails
//
// Example:
//
//	// Load from specific file
//	cfg, err := config.Load("./config/production.yaml")
//
//	// Auto-detect config file or use defaults
//	cfg, err := config.Load("")
//
//	// Use environment variables (SERVER_GRPC_ADDRESS=:50051)
//	os.Setenv("SERVER_GRPC_ADDRESS", ":50051")
//	cfg, err := config.Load("")
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

	// Environment overrides: viper looks up env keys verbatim, so nested keys
	// like server.grpc_address must be mapped to SERVER_GRPC_ADDRESS. BindEnv
	// registers each key explicitly because Unmarshal skips env-only values.
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	for _, key := range []string{
		"server.grpc_address",
		"server.metrics_address",
		"listener.address",
		"listener.cert_file",
		"listener.key_file",
		"listener.ca_file",
		"logging.level",
		"auth.enabled",
	} {
		_ = v.BindEnv(key)
	}

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

	// A half-configured pair passes file checks but can never complete a TLS
	// handshake; reject it at load time with an actionable message.
	if (cfg.Listener.CertFile == "") != (cfg.Listener.KeyFile == "") {
		return nil, fmt.Errorf(
			"listener.cert_file and listener.key_file must be configured together (got cert=%q key=%q)",
			cfg.Listener.CertFile, cfg.Listener.KeyFile)
	}

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
