package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadEnvOverrides verifies the documented precedence where environment
// variables such as SERVER_GRPC_ADDRESS override file values and defaults.
func TestLoadEnvOverrides(t *testing.T) {
	t.Setenv("SERVER_GRPC_ADDRESS", ":7777")
	t.Setenv("LOGGING_LEVEL", "debug")
	t.Setenv("LISTENER_ADDRESS", ":9443")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Server.GRPCAddress != ":7777" {
		t.Errorf("SERVER_GRPC_ADDRESS not applied: got %q", cfg.Server.GRPCAddress)
	}
	if cfg.Logging.Level != "debug" {
		t.Errorf("LOGGING_LEVEL not applied: got %q", cfg.Logging.Level)
	}
	if cfg.Listener.Address != ":9443" {
		t.Errorf("LISTENER_ADDRESS not applied: got %q", cfg.Listener.Address)
	}
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// TestLoadCertKeyMustBePaired: configuring only one of cert_file/key_file
// must fail at load time instead of producing a TLS setup that can never
// complete a handshake.
func TestLoadCertKeyMustBePaired(t *testing.T) {
	path := writeConfig(t, `
listener:
  address: ":8443"
  cert_file: server.crt
`)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected pairing error when only cert_file is set")
	}
	if !strings.Contains(err.Error(), "together") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoadCertKeyPairedSucceeds(t *testing.T) {
	dir := t.TempDir()
	cert := filepath.Join(dir, "server.crt")
	key := filepath.Join(dir, "server.key")
	for _, p := range []string{cert, key} {
		if err := os.WriteFile(p, []byte("placeholder"), 0o600); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	path := writeConfig(t, "listener:\n  cert_file: "+cert+"\n  key_file: "+key+"\n")

	if _, err := Load(path); err != nil {
		t.Fatalf("paired cert/key should load: %v", err)
	}
}
