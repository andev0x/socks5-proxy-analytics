package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaultsAndEnvOverrides(t *testing.T) {
	setRequiredDBEnv(t)
	t.Setenv("PROXY_PORT", "1081")
	t.Setenv("PROXY_AUTH_ENABLED", "false")
	t.Setenv("PROXY_IP_WHITELIST", "10.0.0.0/8, 192.168.1.10")
	t.Setenv("PIPELINE_BUFFER_SIZE", "250")
	t.Setenv("RATE_LIMIT_ENABLED", "true")
	t.Setenv("RATE_LIMIT_RPS", "5")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Proxy.Port != 1081 {
		t.Errorf("proxy port = %d, want 1081", cfg.Proxy.Port)
	}

	if cfg.Proxy.Address != "127.0.0.1" {
		t.Errorf("default proxy address = %q, want loopback", cfg.Proxy.Address)
	}

	if len(cfg.Proxy.IPWhitelist) != 2 || cfg.Proxy.IPWhitelist[0] != "10.0.0.0/8" {
		t.Errorf("ip whitelist = %#v, want two parsed entries", cfg.Proxy.IPWhitelist)
	}

	if cfg.Pipeline.BufferSize != 250 {
		t.Errorf("buffer size = %d, want 250", cfg.Pipeline.BufferSize)
	}

	if !cfg.RateLimit.Enabled || cfg.RateLimit.RequestsPerSecond != 5 {
		t.Errorf("rate limit config = %+v", cfg.RateLimit)
	}

	if cfg.Metrics.Port != 9090 || !cfg.Metrics.Enabled {
		t.Errorf("metrics config = %+v", cfg.Metrics)
	}
}

func TestLoadConfigFileAndEnvPrecedence(t *testing.T) {
	setRequiredDBEnv(t)

	dir := t.TempDir()
	file := filepath.Join(dir, "config.yml")

	content := []byte(`
proxy:
  port: 2080
  ip_whitelist:
    - 172.16.0.0/12
pipeline:
  batch_size: 7
`)
	if err := os.WriteFile(file, content, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("CONFIG_FILE", file)
	t.Setenv("PROXY_PORT", "3080")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Proxy.Port != 3080 {
		t.Errorf("env must override file: port = %d, want 3080", cfg.Proxy.Port)
	}

	if cfg.Pipeline.BatchSize != 7 {
		t.Errorf("file batch size = %d, want 7", cfg.Pipeline.BatchSize)
	}

	if len(cfg.Proxy.IPWhitelist) != 1 || cfg.Proxy.IPWhitelist[0] != "172.16.0.0/12" {
		t.Errorf("file whitelist = %#v", cfg.Proxy.IPWhitelist)
	}
}

func TestValidateRejectsAuthWithoutCredentials(t *testing.T) {
	setRequiredDBEnv(t)
	t.Setenv("PROXY_AUTH_ENABLED", "true")
	t.Setenv("PROXY_AUTH_USERNAME", "")
	t.Setenv("PROXY_AUTH_PASSWORD", "")

	if _, err := Load(); err == nil {
		t.Fatal("expected auth without credentials to fail validation")
	}
}

func TestValidateRejectsMalformedWhitelist(t *testing.T) {
	setRequiredDBEnv(t)
	t.Setenv("PROXY_IP_WHITELIST", "10.0.0.0/8,not-an-address")

	if _, err := Load(); err == nil {
		t.Fatal("expected malformed whitelist entry to fail validation")
	}
}

func TestValidateRejectsMissingDatabase(t *testing.T) {
	t.Setenv("CONFIG_FILE", "")
	t.Setenv("CONFIG_PATH", t.TempDir())
	t.Setenv("DB_HOST", "")
	t.Setenv("DB_USER", "")
	t.Setenv("DB_PASSWORD", "")
	t.Setenv("DB_NAME", "")

	if _, err := Load(); err == nil {
		t.Fatal("expected missing database configuration to fail")
	}
}

func TestValidateRejectsUnsafeValues(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{"port", "PROXY_PORT", "0"},
		{"workers", "PIPELINE_WORKERS", "0"},
		{"buffer", "PIPELINE_BUFFER_SIZE", "0"},
		{"batch", "PIPELINE_BATCH_SIZE", "0"},
		{"rate limit without rps", "RATE_LIMIT_RPS", "0"},
		{"ssl mode", "DB_SSLMODE", "bogus"},
		{"log level", "LOG_LEVEL", "verbose"},
		{"shutdown budget", "SHUTDOWN_TIMEOUT_MS", "1000"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setRequiredDBEnv(t)
			t.Setenv(test.key, test.value)
			if test.name == "rate limit without rps" {
				t.Setenv("RATE_LIMIT_ENABLED", "true")
			}

			if _, err := Load(); err == nil {
				t.Fatalf("expected %s=%s to fail validation", test.key, test.value)
			}
		})
	}
}

func setRequiredDBEnv(t *testing.T) {
	t.Helper()

	t.Setenv("CONFIG_FILE", "")
	t.Setenv("CONFIG_PATH", t.TempDir())
	t.Setenv("DB_HOST", "localhost")
	t.Setenv("DB_PORT", "5432")
	t.Setenv("DB_USER", "test_user")
	t.Setenv("DB_PASSWORD", "test_password")
	t.Setenv("DB_NAME", "test_db")
}
