// Package config provides configuration loading and management for the application.
package config

import (
	"errors"
	"fmt"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

// Config holds all application configuration loaded from
// config files and environment variables.
type Config struct {
	Proxy struct {
		Address string `mapstructure:"address"`
		Port    int    `mapstructure:"port"`
		Auth    struct {
			Enabled  bool   `mapstructure:"enabled"`
			Username string `mapstructure:"username"`
			Password string `mapstructure:"password"`
		} `mapstructure:"auth"`
		MaxConnections int      `mapstructure:"max_connections"`
		IPWhitelist    []string `mapstructure:"ip_whitelist"`
	} `mapstructure:"proxy"`

	API struct {
		Address string `mapstructure:"address"`
		Port    int    `mapstructure:"port"`
	} `mapstructure:"api"`

	Database struct {
		Host     string `mapstructure:"host"`
		Port     int    `mapstructure:"port"`
		User     string `mapstructure:"user"`
		Password string `mapstructure:"password"`
		Database string `mapstructure:"database"`
		SSLMode  string `mapstructure:"sslmode"`
	} `mapstructure:"database"`

	Pipeline struct {
		Workers       int `mapstructure:"workers"`
		BufferSize    int `mapstructure:"buffer_size"`
		BatchSize     int `mapstructure:"batch_size"`
		FlushInterval int `mapstructure:"flush_interval_ms"`
	} `mapstructure:"pipeline"`

	Logging struct {
		Level  string `mapstructure:"level"`
		Format string `mapstructure:"format"`
	} `mapstructure:"logging"`

	RateLimit struct {
		Enabled           bool `mapstructure:"enabled"`
		RequestsPerSecond int  `mapstructure:"requests_per_second"`
	} `mapstructure:"rate_limit"`
}

// Load loads application configuration from:
// 1. .env file (if present)
// 2. config.yml file
// 3. environment variables (highest priority)
//
// It validates that required database settings are provided.
func Load() (*Config, error) {
	// Load .env file if it exists (no error if missing).
	_ = godotenv.Load()

	viper.SetConfigName("config")
	viper.SetConfigType("yml")
	viper.AddConfigPath("./configs")

	setDefaults()

	// Read config file if present.
	if err := viper.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) {
			return nil, fmt.Errorf("error reading config file: %w", err)
		}
	}

	// Bind environment variables.
	if err := bindEnvs(); err != nil {
		return nil, err
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("error unmarshaling config: %w", err)
	}

	// Validate required database configuration.
	if cfg.Database.Host == "" {
		return nil, fmt.Errorf("critical: DB_HOST environment variable not set")
	}
	if cfg.Database.User == "" {
		return nil, fmt.Errorf("critical: DB_USER environment variable not set")
	}
	if cfg.Database.Password == "" {
		return nil, fmt.Errorf("critical: DB_PASSWORD environment variable not set")
	}
	if cfg.Database.Database == "" {
		return nil, fmt.Errorf("critical: DB_NAME environment variable not set")
	}

	return &cfg, nil
}

// bindEnvs binds all supported environment variables to viper keys.
func bindEnvs() error {
	bindings := map[string]string{
		"proxy.address":                  "PROXY_ADDRESS",
		"proxy.port":                     "PROXY_PORT",
		"proxy.auth.enabled":             "PROXY_AUTH_ENABLED",
		"proxy.auth.username":            "PROXY_AUTH_USERNAME",
		"proxy.auth.password":            "PROXY_AUTH_PASSWORD",
		"proxy.max_connections":          "PROXY_MAX_CONNECTIONS",
		"api.address":                    "API_ADDRESS",
		"api.port":                       "API_PORT",
		"database.host":                  "DB_HOST",
		"database.port":                  "DB_PORT",
		"database.user":                  "DB_USER",
		"database.password":              "DB_PASSWORD",
		"database.database":              "DB_NAME",
		"database.sslmode":               "DB_SSLMODE",
		"pipeline.workers":               "PIPELINE_WORKERS",
		"pipeline.buffer_size":           "PIPELINE_BUFFER_SIZE",
		"pipeline.batch_size":            "PIPELINE_BATCH_SIZE",
		"pipeline.flush_interval_ms":     "PIPELINE_FLUSH_INTERVAL_MS",
		"logging.level":                  "LOG_LEVEL",
		"logging.format":                 "LOG_FORMAT",
		"rate_limit.enabled":             "RATE_LIMIT_ENABLED",
		"rate_limit.requests_per_second": "RATE_LIMIT_RPS",
	}

	for key, env := range bindings {
		if err := viper.BindEnv(key, env); err != nil {
			return fmt.Errorf("failed to bind env %s: %w", env, err)
		}
	}

	return nil
}

// setDefaults sets safe default values for non-sensitive configuration.
func setDefaults() {
	viper.SetDefault("proxy.address", "0.0.0.0")
	viper.SetDefault("proxy.port", 1080)
	viper.SetDefault("proxy.max_connections", 10000)
	viper.SetDefault("proxy.auth.enabled", false)

	viper.SetDefault("api.address", "0.0.0.0")
	viper.SetDefault("api.port", 8080)

	// Database defaults (no credentials).
	viper.SetDefault("database.host", "")
	viper.SetDefault("database.port", 5432)
	viper.SetDefault("database.user", "")
	viper.SetDefault("database.password", "")
	viper.SetDefault("database.database", "")
	viper.SetDefault("database.sslmode", "disable")

	viper.SetDefault("pipeline.workers", 4)
	viper.SetDefault("pipeline.buffer_size", 10000)
	viper.SetDefault("pipeline.batch_size", 100)
	viper.SetDefault("pipeline.flush_interval_ms", 5000)

	viper.SetDefault("logging.level", "info")
	viper.SetDefault("logging.format", "json")

	viper.SetDefault("rate_limit.enabled", false)
	viper.SetDefault("rate_limit.requests_per_second", 100)
}
