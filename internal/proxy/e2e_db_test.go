package proxy

import (
	"context"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/andev0x/socks5-proxy-analytics/internal/config"
	"github.com/andev0x/socks5-proxy-analytics/internal/metrics"
	"github.com/andev0x/socks5-proxy-analytics/internal/pipeline"
	"github.com/andev0x/socks5-proxy-analytics/internal/storage"
	"go.uber.org/zap"
)

func TestProxyPipelinePostgresE2E(t *testing.T) {
	cfg := testProxyDatabaseConfig(t)
	if cfg == nil {
		t.Skip("TEST_DB_HOST is not set; proxy/database E2E test skipped")
	}

	cfg = isolatedDatabaseConfig(t, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db, err := storage.NewDatabase(ctx, cfg)
	if err != nil {
		t.Skipf("PostgreSQL is not reachable at %s:%d: %v", cfg.Database.Host, cfg.Database.Port, err)
	}

	repo := storage.NewPostgresRepository(db)
	if err := db.Exec("TRUNCATE TABLE traffic_logs RESTART IDENTITY").Error; err != nil {
		t.Fatalf("truncate traffic_logs: %v", err)
	}

	m, err := metrics.NewMetrics()
	if err != nil {
		t.Fatalf("NewMetrics() error = %v", err)
	}

	analytics := pipeline.NewPipeline(cfg, repo, m, zap.NewNop())
	analytics.Start()

	upstream := startTestUpstream(t, func(conn net.Conn) {
		payload := make([]byte, 4)
		if _, err := io.ReadFull(conn, payload); err != nil {
			return
		}

		_, _ = conn.Write([]byte("pong"))
		_, _ = io.Copy(io.Discard, conn)
	})

	server, err := NewServer(cfg, zap.NewNop(), analytics.Collector(), m, nil)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	if err := server.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()

		_ = server.Shutdown(shutdownCtx)
		_ = analytics.Shutdown(shutdownCtx)
		_ = repo.Close()
	})

	conn, code := openSOCKS(t, server.Addr().String(), "localhost", upstream.Port, nil)
	if code != replySucceeded {
		t.Fatalf("SOCKS reply = %d, want success", code)
	}

	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	response := make([]byte, len("pong"))
	if _, err := io.ReadFull(conn, response); err != nil {
		t.Fatalf("read response: %v", err)
	}

	_ = conn.Close()

	time.Sleep(time.Duration(cfg.Pipeline.FlushInterval)*time.Millisecond + 200*time.Millisecond)

	start := time.Now().UTC().Add(-5 * time.Minute)
	end := time.Now().UTC().Add(5 * time.Minute)
	logs, err := repo.GetTrafficByTimeRange(ctx, start, end, 10, 0)
	if err != nil {
		t.Fatalf("GetTrafficByTimeRange() error = %v", err)
	}

	if len(logs) != 1 {
		t.Fatalf("persisted logs = %d, want 1", len(logs))
	}

	record := logs[0]
	if record.ClientIP != "127.0.0.1" {
		t.Errorf("ClientIP = %q, want 127.0.0.1", record.ClientIP)
	}

	if record.RequestedHost != "localhost" {
		t.Errorf("RequestedHost = %q, want localhost", record.RequestedHost)
	}

	if record.DestinationIP != "127.0.0.1" {
		t.Errorf("DestinationIP = %q, want 127.0.0.1", record.DestinationIP)
	}

	if record.BytesClientToUpstream != int64(len("ping")) {
		t.Errorf("bytes client->upstream = %d, want %d", record.BytesClientToUpstream, len("ping"))
	}

	if record.BytesUpstreamToClient != int64(len("pong")) {
		t.Errorf("bytes upstream->client = %d, want %d", record.BytesUpstreamToClient, len("pong"))
	}

	if record.Outcome != "connected" {
		t.Errorf("Outcome = %q, want connected", record.Outcome)
	}
}

func testProxyDatabaseConfig(t *testing.T) *config.Config {
	t.Helper()

	host := os.Getenv("TEST_DB_HOST")
	if host == "" {
		return nil
	}

	cfg := testProxyConfig()
	cfg.Proxy.Address = "127.0.0.1"
	cfg.Proxy.Port = 0
	cfg.Proxy.MaxConnections = 100
	cfg.Proxy.DialTimeoutMs = 2000
	cfg.Proxy.HandshakeTimeoutMs = 2000
	cfg.Proxy.ShutdownGraceMs = 1000

	cfg.Database.Host = host
	cfg.Database.Port = envInt("TEST_DB_PORT", 5432)
	cfg.Database.User = envString("TEST_DB_USER", "postgres")
	cfg.Database.Password = os.Getenv("TEST_DB_PASSWORD")
	cfg.Database.Database = envString("TEST_DB_NAME", "socks5_analytics_test")
	cfg.Database.SSLMode = envString("TEST_DB_SSLMODE", "disable")
	cfg.Database.ConnectTimeoutMs = 5000
	cfg.Database.MaxOpenConns = 5
	cfg.Database.MaxIdleConns = 2

	cfg.Pipeline.Workers = 2
	cfg.Pipeline.BufferSize = 100
	cfg.Pipeline.BatchSize = 10
	cfg.Pipeline.FlushInterval = 25
	cfg.Pipeline.MaxRetries = 1
	cfg.Pipeline.RetryBackoffMs = 10
	cfg.Pipeline.FlushTimeoutMs = 2000

	cfg.Metrics.Enabled = false
	cfg.RateLimit.MaxTrackedClients = 100

	return cfg
}

func envString(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}

	return fallback
}

func envInt(name string, fallback int) int {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}

	result := 0
	for _, char := range value {
		if char < '0' || char > '9' {
			return fallback
		}

		result = result*10 + int(char-'0')
	}

	return result
}
