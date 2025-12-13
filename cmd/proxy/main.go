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
	defer log.Sync()

	zapLog := log.GetZapLogger()

	// Initialize database
	db, err := storage.NewDatabase(cfg)
	if err != nil {
		zapLog.Fatal("Failed to initialize database", zap.Error(err))
	}

	repo := storage.NewPostgresRepository(db)
	defer repo.Close()

	// Initialize pipeline
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

	// Initialize proxy server
	proxyServer := proxy.NewServer(cfg, zapLog, collector)
	if err := proxyServer.Start(); err != nil {
		zapLog.Fatal("Failed to start proxy server", zap.Error(err))
	}

	zapLog.Info("SOCKS5 Proxy Analytics started successfully")

	// Setup graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	<-sigChan
	zapLog.Info("Shutting down gracefully...")

	if err := proxyServer.Stop(); err != nil {
		zapLog.Error("Error stopping proxy server", zap.Error(err))
	}

	publisher.Stop()
	normalizer.Close()
	close(collectorChan)

	zapLog.Info("Shutdown complete")
}
