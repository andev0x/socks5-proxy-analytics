// Package main provides the SOCKS5 proxy server entry point.
package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/andev0x/socks5-proxy-analytics/internal/config"
	"github.com/andev0x/socks5-proxy-analytics/internal/logger"
	"github.com/andev0x/socks5-proxy-analytics/internal/models"
	"github.com/andev0x/socks5-proxy-analytics/internal/pipeline"
	"github.com/andev0x/socks5-proxy-analytics/internal/proxy"
	"github.com/andev0x/socks5-proxy-analytics/internal/storage"
	"go.uber.org/zap"
)

func main() {
	cfg, zapLog := initializeApp()
	repo := initializeDatabase(cfg, zapLog)
	defer closeRepository(repo, zapLog)

	collector, normalizer, publisher := initializePipeline(cfg, repo, zapLog)
	proxyServer := initializeProxy(cfg, zapLog, collector)

	waitForShutdown(zapLog, proxyServer, publisher, normalizer)
}

func initializeApp() (*config.Config, *zap.Logger) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	log, err := logger.New(cfg.Logging.Level)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create logger: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		_ = log.Sync()
	}()

	return cfg, log.GetZapLogger()
}

func initializeDatabase(cfg *config.Config, zapLog *zap.Logger) storage.Repository {
	db, err := storage.NewDatabase(cfg)
	if err != nil {
		zapLog.Fatal("Failed to initialize database", zap.Error(err))
	}

	return storage.NewPostgresRepository(db)
}

func closeRepository(repo storage.Repository, zapLog *zap.Logger) {
	if err := repo.Close(); err != nil {
		zapLog.Error("failed to close repository", zap.Error(err))
	}
}

func initializePipeline(
	cfg *config.Config, repo storage.Repository, zapLog *zap.Logger,
) (*pipeline.Collector, *pipeline.Normalizer, *pipeline.Publisher) {
	collectorChan := make(chan pipeline.RawTrafficEvent, cfg.Pipeline.BufferSize)
	normalizerOutputChan := make(chan *models.TrafficLog, cfg.Pipeline.BufferSize)

	collector := pipeline.NewCollector(collectorChan, zapLog)
	normalizer := pipeline.NewNormalizer(collectorChan, normalizerOutputChan, zapLog)
	normalizer.Start(cfg.Pipeline.Workers)

	publisher := pipeline.NewPublisher(
		normalizerOutputChan,
		repo,
		cfg.Pipeline.BatchSize,
		cfg.Pipeline.FlushInterval,
		zapLog,
	)
	publisher.Start()

	return collector, normalizer, publisher
}

func initializeProxy(
	cfg *config.Config, zapLog *zap.Logger, collector *pipeline.Collector,
) *proxy.Server {
	proxyServer := proxy.NewServer(cfg, zapLog, collector)
	if err := proxyServer.Start(); err != nil {
		zapLog.Fatal("Failed to start proxy server", zap.Error(err))
	}

	zapLog.Info("SOCKS5 Proxy Analytics started successfully")

	return proxyServer
}

func waitForShutdown(
	zapLog *zap.Logger, proxyServer *proxy.Server,
	publisher *pipeline.Publisher, normalizer *pipeline.Normalizer,
) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	<-sigChan
	zapLog.Info("Shutting down gracefully...")

	if err := proxyServer.Stop(); err != nil {
		zapLog.Error("Error stopping proxy server", zap.Error(err))
	}

	publisher.Stop()
	normalizer.Close()

	zapLog.Info("Shutdown complete")
}
