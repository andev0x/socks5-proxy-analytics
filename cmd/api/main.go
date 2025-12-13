package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/andev0x/socks5-proxy-analytics/internal/api"
	"github.com/andev0x/socks5-proxy-analytics/internal/config"
	"github.com/andev0x/socks5-proxy-analytics/internal/logger"
	"github.com/andev0x/socks5-proxy-analytics/internal/storage"
	"github.com/gin-gonic/gin"
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

	// Setup Gin router
	if cfg.Logging.Level == "info" || cfg.Logging.Level == "warn" {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.Default()

	// Initialize handler
	handler := api.NewHandler(repo, zapLog)

	// Register routes
	router.GET("/health", handler.Health)
	router.GET("/stats/top-domains", handler.GetTopDomains)
	router.GET("/stats/source-ips", handler.GetTopSourceIPs)
	router.GET("/stats/traffic", handler.GetTrafficStats)
	router.GET("/logs/traffic", handler.GetTrafficLogs)

	zapLog.Info("API server starting", zap.String("address", fmt.Sprintf("%s:%d", cfg.API.Address, cfg.API.Port)))

	// Run server in a goroutine
	go func() {
		addr := fmt.Sprintf("%s:%d", cfg.API.Address, cfg.API.Port)
		if err := router.Run(addr); err != nil {
			zapLog.Error("failed to run API server", zap.Error(err))
			os.Exit(1)
		}
	}()

	// Setup graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	<-sigChan
	zapLog.Info("API server shutting down gracefully...")
}
