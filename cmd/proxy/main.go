// Package main provides the SOCKS5 proxy server entry point.
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/andev0x/socks5-proxy-analytics/internal/config"
	"github.com/andev0x/socks5-proxy-analytics/internal/logger"
	"github.com/andev0x/socks5-proxy-analytics/internal/metrics"
	"github.com/andev0x/socks5-proxy-analytics/internal/pipeline"
	"github.com/andev0x/socks5-proxy-analytics/internal/policy"
	"github.com/andev0x/socks5-proxy-analytics/internal/proxy"
	"github.com/andev0x/socks5-proxy-analytics/internal/storage"
	"go.uber.org/zap"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to run application: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, log, err := initializeApp()
	if err != nil {
		return err
	}
	defer func() {
		_ = log.Sync()
	}()

	app, err := buildApp(cfg, log)
	if err != nil {
		log.Error("failed to start application", zap.Error(err))

		return err
	}

	waitForShutdown(cfg, log, app)

	return nil
}

type application struct {
	metricsServer *http.Server
	metrics       *metrics.Metrics
	repo          storage.Repository
	pipeline      *pipeline.Pipeline
	proxy         *proxy.Server
}

func initializeApp() (*config.Config, *zap.Logger, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load config: %w", err)
	}

	log, err := logger.New(cfg.Logging.Level, cfg.Logging.Format)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create logger: %w", err)
	}

	return cfg, log.GetZapLogger(), nil
}

func buildApp(cfg *config.Config, log *zap.Logger) (*application, error) {
	logStartupConfig(cfg, log)

	appMetrics, err := metrics.NewMetrics()
	if err != nil {
		return nil, fmt.Errorf("failed to create metrics: %w", err)
	}

	metricsServer, err := startMetricsServer(cfg, appMetrics, log)
	if err != nil {
		return nil, err
	}

	db, err := storage.NewDatabase(context.Background(), cfg)
	if err != nil {
		shutdownMetricsServer(metricsServer, log)

		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	repo := storage.NewPostgresRepository(db)
	analytics := pipeline.NewPipeline(cfg, repo, appMetrics, log)
	analytics.Start()

	policyEngine, err := policy.NewEngineFromConfig(cfg)
	if err != nil {
		shutdownPipelineAndRepo(cfg, analytics, repo, log)
		shutdownMetricsServer(metricsServer, log)

		return nil, fmt.Errorf("failed to load egress policy: %w", err)
	}

	authenticatorSetup := cfg.Proxy.Auth.Enabled
	if authenticatorSetup && len(cfg.Proxy.Workloads) > 0 {
		log.Info("workload identities configured", zap.Int("count", len(cfg.Proxy.Workloads)))
	}

	proxyServer, err := proxy.NewServer(cfg, log, analytics.Collector(), appMetrics, policyEngine)
	if err != nil {
		shutdownPipelineAndRepo(cfg, analytics, repo, log)
		shutdownMetricsServer(metricsServer, log)

		return nil, fmt.Errorf("failed to create proxy server: %w", err)
	}

	if err := proxyServer.Start(); err != nil {
		shutdownPipelineAndRepo(cfg, analytics, repo, log)
		shutdownMetricsServer(metricsServer, log)

		return nil, fmt.Errorf("failed to start proxy server: %w", err)
	}

	log.Info("SOCKS5 Proxy Analytics started",
		zap.String("proxy_address", proxyServer.Addr().String()),
		zap.String("metrics_address", metricsAddress(cfg)))

	return &application{
		metricsServer: metricsServer,
		metrics:       appMetrics,
		repo:          repo,
		pipeline:      analytics,
		proxy:         proxyServer,
	}, nil
}

func waitForShutdown(cfg *config.Config, log *zap.Logger, app *application) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	sig := <-sigChan
	log.Info("shutdown signal received", zap.String("signal", sig.String()))

	start := time.Now()
	clean := shutdownApp(cfg, log, app)
	result := metrics.ShutdownClean
	if !clean {
		result = metrics.ShutdownTimeout
	}

	app.metrics.Shutdowns.WithLabelValues(result).Inc()
	log.Info("shutdown complete",
		zap.Bool("clean", clean),
		zap.Duration("duration", time.Since(start)))
}

// shutdownApp stops accepting connections first, waits for sessions, then drains and
// flushes analytics, and finally closes network/database resources.
func shutdownApp(cfg *config.Config, log *zap.Logger, app *application) bool {
	deadline := time.Duration(cfg.Shutdown.TimeoutMs) * time.Millisecond
	shutdownCtx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()

	clean := true

	graceDuration := time.Duration(cfg.Proxy.ShutdownGraceMs) * time.Millisecond
	graceCtx, graceCancel := context.WithTimeout(shutdownCtx, graceDuration)
	defer graceCancel()

	if err := app.proxy.Shutdown(graceCtx); err != nil {
		clean = false
		log.Warn("proxy sessions did not close cleanly", zap.Error(err))
	}

	if err := app.pipeline.Shutdown(shutdownCtx); err != nil {
		clean = false
		log.Error("analytics pipeline did not flush cleanly", zap.Error(err))
	}

	shutdownMetricsServer(app.metricsServer, log)

	if err := app.repo.Close(); err != nil {
		clean = false
		log.Error("failed to close database repository", zap.Error(err))
	}

	return clean
}

func shutdownPipelineAndRepo(
	cfg *config.Config, analytics *pipeline.Pipeline, repo storage.Repository, log *zap.Logger,
) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Shutdown.TimeoutMs)*time.Millisecond)
	defer cancel()

	if err := analytics.Shutdown(ctx); err != nil {
		log.Warn("analytics pipeline shutdown during startup failure was incomplete", zap.Error(err))
	}

	if err := repo.Close(); err != nil {
		log.Warn("failed to close repository during startup failure", zap.Error(err))
	}
}

func startMetricsServer(cfg *config.Config, m *metrics.Metrics, log *zap.Logger) (*http.Server, error) {
	if !cfg.Metrics.Enabled {
		log.Warn("metrics endpoint disabled by configuration")

		return nil, nil
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", m.Handler())
	mux.HandleFunc("/healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("ok\n"))
	})

	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	address := net.JoinHostPort(cfg.Metrics.Address, fmt.Sprintf("%d", cfg.Metrics.Port))
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on metrics address %s: %w", address, err)
	}

	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			log.Error("metrics server stopped unexpectedly", zap.Error(serveErr))
		}
	}()

	log.Info("metrics endpoint started", zap.String("address", address))

	return server, nil
}

func shutdownMetricsServer(server *http.Server, log *zap.Logger) {
	if server == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Warn("metrics server shutdown did not complete cleanly", zap.Error(err))
	}
}

func metricsAddress(cfg *config.Config) string {
	if !cfg.Metrics.Enabled {
		return "disabled"
	}

	return net.JoinHostPort(cfg.Metrics.Address, fmt.Sprintf("%d", cfg.Metrics.Port))
}

func logStartupConfig(cfg *config.Config, log *zap.Logger) {
	log.Info("configuration loaded",
		zap.String("proxy_address", cfg.Proxy.Address),
		zap.Int("proxy_port", cfg.Proxy.Port),
		zap.Bool("proxy_auth_enabled", cfg.Proxy.Auth.Enabled),
		zap.Int("ip_whitelist_entries", len(cfg.Proxy.IPWhitelist)),
		zap.Bool("rate_limit_enabled", cfg.RateLimit.Enabled),
		zap.Int("rate_limit_rps", cfg.RateLimit.RequestsPerSecond),
		zap.Int("max_connections", cfg.Proxy.MaxConnections),
		zap.Bool("metrics_enabled", cfg.Metrics.Enabled),
		zap.String("database_host", cfg.Database.Host),
		zap.Int("database_port", cfg.Database.Port),
		zap.String("database_name", cfg.Database.Database),
		zap.String("database_sslmode", cfg.Database.SSLMode),
		zap.Int("pipeline_buffer_size", cfg.Pipeline.BufferSize),
		zap.Int("pipeline_batch_size", cfg.Pipeline.BatchSize),
		zap.String("delivery_guarantee", "best-effort telemetry"))
}
