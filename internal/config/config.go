// Package config provides configuration loading, validation and defaults for the application.
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/go-viper/mapstructure/v2"
	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

// PolicyRuleConfig defines one declarative egress policy rule.
type PolicyRuleConfig struct {
	ID         string `mapstructure:"id"`
	Workload   string `mapstructure:"workload"`
	Action     string `mapstructure:"action"`
	Host       string `mapstructure:"host"`
	HostSuffix string `mapstructure:"host_suffix"`
	IP         string `mapstructure:"ip"`
	Port       int    `mapstructure:"port"`
	PortStart  int    `mapstructure:"port_start"`
	PortEnd    int    `mapstructure:"port_end"`
	Priority   int    `mapstructure:"priority"`
}

// PolicyConfig holds Phase 2 egress policy settings.
type PolicyConfig struct {
	Enabled         bool               `mapstructure:"enabled"`
	ID              string             `mapstructure:"id"`
	Version         string             `mapstructure:"version"`
	DefaultAction   string             `mapstructure:"default_action"`
	RestrictedCIDRs []string           `mapstructure:"restricted_cidrs"`
	Rules           []PolicyRuleConfig `mapstructure:"rules"`
}

// Config holds all application configuration loaded from
// config files and environment variables.
type Config struct {
	Proxy     ProxyConfig     `mapstructure:"proxy"`
	API       APIConfig       `mapstructure:"api"`
	Metrics   MetricsConfig   `mapstructure:"metrics"`
	Database  DatabaseConfig  `mapstructure:"database"`
	Pipeline  PipelineConfig  `mapstructure:"pipeline"`
	Logging   LoggingConfig   `mapstructure:"logging"`
	RateLimit RateLimitConfig `mapstructure:"rate_limit"`
	Shutdown  ShutdownConfig  `mapstructure:"shutdown"`
	Policy    PolicyConfig    `mapstructure:"policy"`
}

// WorkloadConfig defines a workload identity and its SOCKS5 credentials.
type WorkloadConfig struct {
	ID       string `mapstructure:"id"`
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
}

// ProxyConfig holds SOCKS5 listener, security and timeout settings.
type ProxyConfig struct {
	Address            string           `mapstructure:"address"`
	Port               int              `mapstructure:"port"`
	Auth               AuthConfig       `mapstructure:"auth"`
	Workloads          []WorkloadConfig `mapstructure:"workloads"`
	MaxConnections     int              `mapstructure:"max_connections"`
	IPWhitelist        []string         `mapstructure:"ip_whitelist"`
	DialTimeoutMs      int              `mapstructure:"dial_timeout_ms"`
	HandshakeTimeoutMs int              `mapstructure:"handshake_timeout_ms"`
	ShutdownGraceMs    int              `mapstructure:"shutdown_grace_ms"`
	IdleTimeoutMs      int              `mapstructure:"idle_timeout_ms"`
}

// AuthConfig holds optional SOCKS5 username/password authentication settings.
type AuthConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
}

// APIConfig holds the analytics HTTP listener settings.
type APIConfig struct {
	Address        string `mapstructure:"address"`
	Port           int    `mapstructure:"port"`
	ReadTimeoutMs  int    `mapstructure:"read_timeout_ms"`
	WriteTimeoutMs int    `mapstructure:"write_timeout_ms"`
	IdleTimeoutMs  int    `mapstructure:"idle_timeout_ms"`
}

// MetricsConfig holds the Prometheus endpoint settings.
type MetricsConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Address string `mapstructure:"address"`
	Port    int    `mapstructure:"port"`
}

// DatabaseConfig holds PostgreSQL connection settings.
type DatabaseConfig struct {
	Host             string `mapstructure:"host"`
	Port             int    `mapstructure:"port"`
	User             string `mapstructure:"user"`
	Password         string `mapstructure:"password"`
	Database         string `mapstructure:"database"`
	SSLMode          string `mapstructure:"sslmode"`
	ConnectTimeoutMs int    `mapstructure:"connect_timeout_ms"`
	MaxOpenConns     int    `mapstructure:"max_open_conns"`
	MaxIdleConns     int    `mapstructure:"max_idle_conns"`
}

// PipelineConfig holds the analytics event pipeline settings.
type PipelineConfig struct {
	Workers        int `mapstructure:"workers"`
	BufferSize     int `mapstructure:"buffer_size"`
	BatchSize      int `mapstructure:"batch_size"`
	FlushInterval  int `mapstructure:"flush_interval_ms"`
	MaxRetries     int `mapstructure:"max_retries"`
	RetryBackoffMs int `mapstructure:"retry_backoff_ms"`
	FlushTimeoutMs int `mapstructure:"flush_timeout_ms"`
}

// LoggingConfig holds structured logging settings.
type LoggingConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
}

// RateLimitConfig holds per-client TCP connection rate limiting settings.
type RateLimitConfig struct {
	Enabled           bool `mapstructure:"enabled"`
	RequestsPerSecond int  `mapstructure:"requests_per_second"`
	MaxTrackedClients int  `mapstructure:"max_tracked_clients"`
}

// ShutdownConfig holds the overall shutdown deadline.
type ShutdownConfig struct {
	TimeoutMs int `mapstructure:"timeout_ms"`
}

// Load loads application configuration from:
// 1. .env file (if present)
// 2. config file from CONFIG_FILE, or CONFIG_PATH (default ./configs/config.yml)
// 3. environment variables (highest priority).
//
// It validates required settings and returns actionable errors without echoing secrets.
func Load() (*Config, error) {
	_ = godotenv.Load()

	v := viper.New()
	setDefaults(v)
	configureFile(v)
	bindEnvs(v)

	if err := readConfig(v); err != nil {
		return nil, err
	}

	var cfg Config
	if err := v.Unmarshal(&cfg, viper.DecodeHook(mapstructure.StringToSliceHookFunc(","))); err != nil {
		return nil, fmt.Errorf("error unmarshaling config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func configureFile(v *viper.Viper) {
	if file := strings.TrimSpace(os.Getenv("CONFIG_FILE")); file != "" {
		v.SetConfigFile(file)

		return
	}

	v.SetConfigName("config")
	v.SetConfigType("yml")

	path := strings.TrimSpace(os.Getenv("CONFIG_PATH"))
	if path == "" {
		path = "./configs"
	}

	v.AddConfigPath(path)
}

func readConfig(v *viper.Viper) error {
	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) {
			return fmt.Errorf("error reading config file: %w", err)
		}
	}

	return nil
}

func bindEnvs(v *viper.Viper) {
	// #nosec G101 -- these are environment variable names, never credential values.
	bindings := map[string]string{
		"proxy.address":                  "PROXY_ADDRESS",
		"proxy.port":                     "PROXY_PORT",
		"proxy.auth.enabled":             "PROXY_AUTH_ENABLED",
		"proxy.auth.username":            "PROXY_AUTH_USERNAME",
		"proxy.auth.password":            "PROXY_AUTH_PASSWORD",
		"proxy.max_connections":          "PROXY_MAX_CONNECTIONS",
		"proxy.ip_whitelist":             "PROXY_IP_WHITELIST",
		"proxy.dial_timeout_ms":          "PROXY_DIAL_TIMEOUT_MS",
		"proxy.handshake_timeout_ms":     "PROXY_HANDSHAKE_TIMEOUT_MS",
		"proxy.shutdown_grace_ms":        "PROXY_SHUTDOWN_GRACE_MS",
		"proxy.idle_timeout_ms":          "PROXY_IDLE_TIMEOUT_MS",
		"policy.enabled":                 "POLICY_ENABLED",
		"policy.id":                      "POLICY_ID",
		"policy.version":                 "POLICY_VERSION",
		"policy.default_action":          "POLICY_DEFAULT_ACTION",
		"api.address":                    "API_ADDRESS",
		"api.port":                       "API_PORT",
		"api.read_timeout_ms":            "API_READ_TIMEOUT_MS",
		"api.write_timeout_ms":           "API_WRITE_TIMEOUT_MS",
		"api.idle_timeout_ms":            "API_IDLE_TIMEOUT_MS",
		"metrics.enabled":                "METRICS_ENABLED",
		"metrics.address":                "METRICS_ADDRESS",
		"metrics.port":                   "METRICS_PORT",
		"database.host":                  "DB_HOST",
		"database.port":                  "DB_PORT",
		"database.user":                  "DB_USER",
		"database.password":              "DB_PASSWORD",
		"database.database":              "DB_NAME",
		"database.sslmode":               "DB_SSLMODE",
		"database.connect_timeout_ms":    "DB_CONNECT_TIMEOUT_MS",
		"database.max_open_conns":        "DB_MAX_OPEN_CONNS",
		"database.max_idle_conns":        "DB_MAX_IDLE_CONNS",
		"pipeline.workers":               "PIPELINE_WORKERS",
		"pipeline.buffer_size":           "PIPELINE_BUFFER_SIZE",
		"pipeline.batch_size":            "PIPELINE_BATCH_SIZE",
		"pipeline.flush_interval_ms":     "PIPELINE_FLUSH_INTERVAL_MS",
		"pipeline.max_retries":           "PIPELINE_MAX_RETRIES",
		"pipeline.retry_backoff_ms":      "PIPELINE_RETRY_BACKOFF_MS",
		"pipeline.flush_timeout_ms":      "PIPELINE_FLUSH_TIMEOUT_MS",
		"logging.level":                  "LOG_LEVEL",
		"logging.format":                 "LOG_FORMAT",
		"rate_limit.enabled":             "RATE_LIMIT_ENABLED",
		"rate_limit.requests_per_second": "RATE_LIMIT_RPS",
		"rate_limit.max_tracked_clients": "RATE_LIMIT_MAX_TRACKED_CLIENTS",
		"shutdown.timeout_ms":            "SHUTDOWN_TIMEOUT_MS",
	}

	for key, env := range bindings {
		// BindEnv only fails for an empty key, which is a programming error covered by tests.
		_ = v.BindEnv(key, env)
	}
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("proxy.address", "127.0.0.1")
	v.SetDefault("proxy.port", 1080)
	v.SetDefault("proxy.max_connections", 10000)
	v.SetDefault("proxy.auth.enabled", false)
	v.SetDefault("proxy.dial_timeout_ms", 10000)
	v.SetDefault("proxy.handshake_timeout_ms", 10000)
	v.SetDefault("proxy.shutdown_grace_ms", 5000)
	v.SetDefault("proxy.idle_timeout_ms", 0)

	v.SetDefault("policy.enabled", false)
	v.SetDefault("policy.id", "default")
	v.SetDefault("policy.version", "1")
	v.SetDefault("policy.default_action", "deny")
	v.SetDefault("api.address", "127.0.0.1")
	v.SetDefault("api.port", 8080)
	v.SetDefault("api.read_timeout_ms", 10000)
	v.SetDefault("api.write_timeout_ms", 10000)
	v.SetDefault("api.idle_timeout_ms", 60000)

	v.SetDefault("metrics.enabled", true)
	v.SetDefault("metrics.address", "127.0.0.1")
	v.SetDefault("metrics.port", 9090)

	v.SetDefault("database.host", "")
	v.SetDefault("database.port", 5432)
	v.SetDefault("database.user", "")
	v.SetDefault("database.password", "")
	v.SetDefault("database.database", "")
	v.SetDefault("database.sslmode", "disable")
	v.SetDefault("database.connect_timeout_ms", 5000)
	v.SetDefault("database.max_open_conns", 20)
	v.SetDefault("database.max_idle_conns", 10)

	v.SetDefault("pipeline.workers", 4)
	v.SetDefault("pipeline.buffer_size", 10000)
	v.SetDefault("pipeline.batch_size", 100)
	v.SetDefault("pipeline.flush_interval_ms", 5000)
	v.SetDefault("pipeline.max_retries", 3)
	v.SetDefault("pipeline.retry_backoff_ms", 200)
	v.SetDefault("pipeline.flush_timeout_ms", 5000)

	v.SetDefault("logging.level", "info")
	v.SetDefault("logging.format", "json")

	v.SetDefault("rate_limit.enabled", false)
	v.SetDefault("rate_limit.requests_per_second", 100)
	v.SetDefault("rate_limit.max_tracked_clients", 100000)

	v.SetDefault("shutdown.timeout_ms", 15000)
}

// Validate checks configuration invariants and fails safely on ambiguous security settings.
func (c *Config) Validate() error {
	if err := c.validateProxy(); err != nil {
		return err
	}

	if err := c.validateAPI(); err != nil {
		return err
	}

	if err := c.validateMetrics(); err != nil {
		return err
	}

	if err := c.validateDatabase(); err != nil {
		return err
	}

	if err := c.validatePipeline(); err != nil {
		return err
	}

	if err := c.validateRateLimit(); err != nil {
		return err
	}

	if err := c.validatePolicy(); err != nil {
		return err
	}

	if err := c.validateLogging(); err != nil {
		return err
	}

	if c.Shutdown.TimeoutMs <= 0 {
		return errors.New("shutdown.timeout_ms must be greater than zero")
	}

	if c.Shutdown.TimeoutMs <= c.Proxy.ShutdownGraceMs {
		return errors.New("shutdown.timeout_ms must be greater than proxy.shutdown_grace_ms")
	}

	return nil
}

func (c *Config) validateProxy() error {
	if err := validateAddress("proxy.address", c.Proxy.Address); err != nil {
		return err
	}

	if err := validatePort("proxy.port", c.Proxy.Port); err != nil {
		return err
	}

	if c.Proxy.MaxConnections <= 0 {
		return errors.New("proxy.max_connections must be greater than zero")
	}

	if c.Proxy.DialTimeoutMs <= 0 {
		return errors.New("proxy.dial_timeout_ms must be greater than zero")
	}

	if c.Proxy.HandshakeTimeoutMs <= 0 {
		return errors.New("proxy.handshake_timeout_ms must be greater than zero")
	}

	if c.Proxy.ShutdownGraceMs < 0 {
		return errors.New("proxy.shutdown_grace_ms must not be negative")
	}

	if c.Proxy.IdleTimeoutMs < 0 {
		return errors.New("proxy.idle_timeout_ms must not be negative")
	}

	if err := validateProxyAuth(c.Proxy.Auth); err != nil {
		return err
	}

	if err := validateWorkloads(c.Proxy.Workloads); err != nil {
		return err
	}

	return validateIPWhitelist(c.Proxy.IPWhitelist)
}

func validateProxyAuth(auth AuthConfig) error {
	if !auth.Enabled {
		return nil
	}

	if auth.Username == "" {
		return errors.New("proxy.auth.username is required when proxy.auth.enabled is true")
	}

	if auth.Password == "" {
		return errors.New("proxy.auth.password is required when proxy.auth.enabled is true")
	}

	if len(auth.Username) > 255 || len(auth.Password) > 255 {
		return errors.New("proxy.auth username and password must be at most 255 bytes (RFC 1929)")
	}

	if strings.ContainsRune(auth.Username, 0) || strings.ContainsRune(auth.Password, 0) {
		return errors.New("proxy.auth username and password must not contain NUL bytes")
	}

	return nil
}

func validateWorkloads(workloads []WorkloadConfig) error {
	ids := make(map[string]struct{})
	usernames := make(map[string]struct{})

	for _, workload := range workloads {
		if strings.TrimSpace(workload.ID) == "" {
			return errors.New("proxy.workloads[].id is required")
		}

		if _, exists := ids[workload.ID]; exists {
			return fmt.Errorf("duplicate workload id %q", workload.ID)
		}

		ids[workload.ID] = struct{}{}

		if strings.TrimSpace(workload.Username) == "" || strings.TrimSpace(workload.Password) == "" {
			return fmt.Errorf("workload %q requires username and password", workload.ID)
		}

		if _, exists := usernames[workload.Username]; exists {
			return fmt.Errorf("duplicate workload username %q", workload.Username)
		}

		usernames[workload.Username] = struct{}{}
	}

	return nil
}

func validateIPWhitelist(entries []string) error {
	for _, entry := range entries {
		if err := validateIPOrCIDR(entry); err != nil {
			return fmt.Errorf("proxy.ip_whitelist entry %q: %w", entry, err)
		}
	}

	return nil
}

func (c *Config) validateAPI() error {
	if err := validateAddress("api.address", c.API.Address); err != nil {
		return err
	}

	if err := validatePort("api.port", c.API.Port); err != nil {
		return err
	}

	if c.API.ReadTimeoutMs <= 0 || c.API.WriteTimeoutMs <= 0 || c.API.IdleTimeoutMs <= 0 {
		return errors.New("api read, write and idle timeouts must be greater than zero")
	}

	return nil
}

func (c *Config) validateMetrics() error {
	if err := validateAddress("metrics.address", c.Metrics.Address); err != nil {
		return err
	}

	if err := validatePort("metrics.port", c.Metrics.Port); err != nil {
		return err
	}

	return nil
}

func (c *Config) validateDatabase() error {
	if c.Database.Host == "" {
		return errors.New("critical: DB_HOST environment variable not set")
	}

	if c.Database.User == "" {
		return errors.New("critical: DB_USER environment variable not set")
	}

	if c.Database.Password == "" {
		return errors.New("critical: DB_PASSWORD environment variable not set")
	}

	if c.Database.Database == "" {
		return errors.New("critical: DB_NAME environment variable not set")
	}

	if err := validatePort("database.port", c.Database.Port); err != nil {
		return err
	}

	if err := validateSSLMode(c.Database.SSLMode); err != nil {
		return err
	}

	if c.Database.ConnectTimeoutMs <= 0 {
		return errors.New("database.connect_timeout_ms must be greater than zero")
	}

	if c.Database.MaxOpenConns <= 0 || c.Database.MaxIdleConns <= 0 {
		return errors.New("database connection pool sizes must be greater than zero")
	}

	return nil
}

func (c *Config) validatePipeline() error {
	if err := validatePipelineBounds(c.Pipeline); err != nil {
		return err
	}

	if c.Pipeline.FlushInterval <= 0 {
		return errors.New("pipeline.flush_interval_ms must be greater than zero")
	}

	if c.Pipeline.MaxRetries < 0 || c.Pipeline.MaxRetries > 10 {
		return errors.New("pipeline.max_retries must be between 0 and 10")
	}

	if c.Pipeline.RetryBackoffMs < 0 {
		return errors.New("pipeline.retry_backoff_ms must not be negative")
	}

	if c.Pipeline.FlushTimeoutMs <= 0 {
		return errors.New("pipeline.flush_timeout_ms must be greater than zero")
	}

	return nil
}

func validatePipelineBounds(pipeline PipelineConfig) error {
	if pipeline.Workers <= 0 || pipeline.Workers > 128 {
		return errors.New("pipeline.workers must be between 1 and 128")
	}

	if pipeline.BufferSize <= 0 || pipeline.BufferSize > 1000000 {
		return errors.New("pipeline.buffer_size must be between 1 and 1000000")
	}

	if pipeline.BatchSize <= 0 || pipeline.BatchSize > 10000 {
		return errors.New("pipeline.batch_size must be between 1 and 10000")
	}

	return nil
}

func validateSSLMode(sslMode string) error {
	switch sslMode {
	case "disable", "allow", "prefer", "require", "verify-ca", "verify-full":
		return nil
	default:
		return fmt.Errorf("database.sslmode %q is not supported", sslMode)
	}
}

func (c *Config) validatePolicy() error {
	if !c.Policy.Enabled {
		return nil
	}

	if !c.Proxy.Auth.Enabled && len(c.Proxy.Workloads) == 0 {
		return errors.New("policy.enabled requires proxy.auth.enabled with at least one workload")
	}

	switch c.Policy.DefaultAction {
	case "", "deny":
	default:
		return errors.New("policy.default_action must be deny")
	}

	for i := range c.Policy.Rules {
		if err := validatePolicyRule(&c.Policy.Rules[i]); err != nil {
			return err
		}
	}

	for _, cidr := range c.Policy.RestrictedCIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			if net.ParseIP(cidr) == nil {
				return fmt.Errorf("policy restricted_cidr %q is invalid", cidr)
			}
		}
	}

	return nil
}

func validatePolicyRule(rule *PolicyRuleConfig) error {
	if strings.TrimSpace(rule.Action) == "" {
		return errors.New("policy rule action is required")
	}

	switch rule.Action {
	case "allow", "deny":
	default:
		return fmt.Errorf("policy rule action %q must be allow or deny", rule.Action)
	}

	if rule.Port != 0 && (rule.Port < 1 || rule.Port > 65535) {
		return fmt.Errorf("policy rule port %d is invalid", rule.Port)
	}

	if rule.PortStart != 0 || rule.PortEnd != 0 {
		if rule.PortStart < 1 || rule.PortEnd < rule.PortStart || rule.PortEnd > 65535 {
			return fmt.Errorf("policy rule port range %d-%d is invalid", rule.PortStart, rule.PortEnd)
		}
	}

	return nil
}

func (c *Config) validateRateLimit() error {
	if c.RateLimit.Enabled && c.RateLimit.RequestsPerSecond <= 0 {
		return errors.New("rate_limit.requests_per_second must be greater than zero when rate limiting is enabled")
	}

	if c.RateLimit.MaxTrackedClients <= 0 {
		return errors.New("rate_limit.max_tracked_clients must be greater than zero")
	}

	return nil
}

func (c *Config) validateLogging() error {
	switch c.Logging.Level {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("logging.level %q must be one of debug, info, warn, error", c.Logging.Level)
	}

	switch c.Logging.Format {
	case "json", "text":
	default:
		return fmt.Errorf("logging.format %q must be json or text", c.Logging.Format)
	}

	return nil
}

func validateAddress(field, address string) error {
	if strings.TrimSpace(address) == "" {
		return fmt.Errorf("%s must not be empty", field)
	}

	if ip := net.ParseIP(address); ip == nil && address != "localhost" {
		return fmt.Errorf("%s must be an IP address or localhost, got %q", field, address)
	}

	return nil
}

func validatePort(field string, port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("%s must be between 1 and 65535, got %d", field, port)
	}

	return nil
}

func validateIPOrCIDR(entry string) error {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return errors.New("entry is empty")
	}

	if net.ParseIP(entry) != nil {
		return nil
	}

	if _, _, err := net.ParseCIDR(entry); err != nil {
		return errors.New("entry must be an IP address or CIDR")
	}

	return nil
}
