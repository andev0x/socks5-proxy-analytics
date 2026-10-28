// Package main provides the analytics API server entry point.
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
	"github.com/andev0x/socks5-proxy-analytics/internal/handlers"
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

	log, err := logger.New(cfg.Logging.Level, cfg.Logging.Format)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create logger: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		_ = log.Sync()
	}()

	zapLog := log.GetZapLogger()

	db, err := storage.NewDatabase(context.Background(), cfg)
	if err != nil {
		zapLog.Fatal("Failed to initialize database", zap.Error(err))
	}

	repo := storage.NewPostgresRepository(db)
	server := buildHTTPServer(cfg, repo, zapLog)

	address := net.JoinHostPort(cfg.API.Address, fmt.Sprintf("%d", cfg.API.Port))
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", address)
	if err != nil {
		zapLog.Fatal("Failed to listen on API address", zap.String("address", address), zap.Error(err))
	}

	go func() {
		zapLog.Info("API server started", zap.String("address", address))
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			zapLog.Error("API server stopped unexpectedly", zap.Error(serveErr))
		}
	}()

	waitForSignal(zapLog, server, repo)
}

func buildHTTPServer(cfg *config.Config, repo storage.Repository, log *zap.Logger) *http.Server {
	if cfg.Logging.Level != "debug" {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.Default()
	handler := handlers.NewHandler(repo, log)

	router.GET("/health", handler.Health)
	router.GET("/stats/top-destinations", handler.GetTopDestinations)
	router.GET("/stats/top-domains", handler.GetTopDestinations)
	router.GET("/stats/top-client-ips", handler.GetTopClientIPs)
	router.GET("/stats/source-ips", handler.GetTopClientIPs)
	router.GET("/stats/traffic", handler.GetTrafficStats)
	router.GET("/logs/traffic", handler.GetTrafficLogs)

	return &http.Server{
		Handler:           router,
		ReadHeaderTimeout: time.Duration(cfg.API.ReadTimeoutMs) * time.Millisecond,
		ReadTimeout:       time.Duration(cfg.API.ReadTimeoutMs) * time.Millisecond,
		WriteTimeout:      time.Duration(cfg.API.WriteTimeoutMs) * time.Millisecond,
		IdleTimeout:       time.Duration(cfg.API.IdleTimeoutMs) * time.Millisecond,
	}
}

func waitForSignal(log *zap.Logger, server *http.Server, repo storage.Repository) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	sig := <-sigChan
	log.Info("API shutdown signal received", zap.String("signal", sig.String()))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Error("API server shutdown did not complete cleanly", zap.Error(err))
	}

	if err := repo.Close(); err != nil {
		log.Error("failed to close repository", zap.Error(err))
	}

	log.Info("API shutdown complete")
}
