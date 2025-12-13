package config

import (
	"fmt"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

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

func Load() (*Config, error) {
	// Load .env file if it exists
	_ = godotenv.Load()

	viper.SetConfigName("config")
	viper.SetConfigType("yml")
	viper.AddConfigPath("./configs")

	// Set defaults
	setDefaults()

	// Try to read config file, but don't fail if it doesn't exist
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("error reading config file: %w", err)
		}
	}

	// Override with environment variables
	viper.BindEnv("proxy.address", "PROXY_ADDRESS")
	viper.BindEnv("proxy.port", "PROXY_PORT")
	viper.BindEnv("proxy.auth.enabled", "PROXY_AUTH_ENABLED")
	viper.BindEnv("proxy.auth.username", "PROXY_AUTH_USERNAME")
	viper.BindEnv("proxy.auth.password", "PROXY_AUTH_PASSWORD")
	viper.BindEnv("proxy.max_connections", "PROXY_MAX_CONNECTIONS")

	viper.BindEnv("api.address", "API_ADDRESS")
	viper.BindEnv("api.port", "API_PORT")

	// Database - credentials from environment
	viper.BindEnv("database.host", "DB_HOST")
	viper.BindEnv("database.port", "DB_PORT")
	viper.BindEnv("database.user", "DB_USER")
	viper.BindEnv("database.password", "DB_PASSWORD")
	viper.BindEnv("database.database", "DB_NAME")
	viper.BindEnv("database.sslmode", "DB_SSLMODE")

	viper.BindEnv("pipeline.workers", "PIPELINE_WORKERS")
	viper.BindEnv("pipeline.buffer_size", "PIPELINE_BUFFER_SIZE")
	viper.BindEnv("pipeline.batch_size", "PIPELINE_BATCH_SIZE")
	viper.BindEnv("pipeline.flush_interval_ms", "PIPELINE_FLUSH_INTERVAL_MS")

	viper.BindEnv("logging.level", "LOG_LEVEL")
	viper.BindEnv("logging.format", "LOG_FORMAT")

	viper.BindEnv("rate_limit.enabled", "RATE_LIMIT_ENABLED")
	viper.BindEnv("rate_limit.requests_per_second", "RATE_LIMIT_RPS")

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("error unmarshaling config: %w", err)
	}

	// Validate required credentials
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

func setDefaults() {
	// Safe defaults only - NO credentials
	viper.SetDefault("proxy.address", "0.0.0.0")
	viper.SetDefault("proxy.port", 1080)
	viper.SetDefault("proxy.max_connections", 10000)
	viper.SetDefault("proxy.auth.enabled", false)

	viper.SetDefault("api.address", "0.0.0.0")
	viper.SetDefault("api.port", 8080)

	// Database - no defaults for sensitive data
	// These MUST be provided via environment variables or config file
	viper.SetDefault("database.host", "")
	viper.SetDefault("database.port", 5432) // Safe default for port
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
