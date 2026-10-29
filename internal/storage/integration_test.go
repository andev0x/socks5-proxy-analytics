package storage

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/andev0x/socks5-proxy-analytics/internal/config"
	"github.com/andev0x/socks5-proxy-analytics/internal/models"
)

// TestPostgresRepositoryIntegration verifies migrations, writes and analytics queries against
// a real PostgreSQL instance. It is skipped unless TEST_DB_HOST is set, and must never be
// treated as passed when skipped.
func TestPostgresRepositoryIntegration(t *testing.T) {
	cfg := testDatabaseConfig()
	if cfg == nil {
		t.Skip("TEST_DB_HOST is not set; PostgreSQL integration test skipped")
	}

	cfg = isolatedDatabaseConfig(t, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db, err := NewDatabase(ctx, cfg)
	if err != nil {
		t.Skipf("PostgreSQL is not reachable at %s:%d: %v", cfg.Database.Host, cfg.Database.Port, err)
	}

	repo := NewPostgresRepository(db)
	t.Cleanup(func() {
		_ = repo.Close()
	})

	if err := db.Exec("TRUNCATE TABLE traffic_logs RESTART IDENTITY").Error; err != nil {
		t.Fatalf("truncate traffic_logs: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	records := []*models.TrafficLog{
		newRecord("event-1", now, "example.test", "203.0.113.5", 443, 11, 7),
		newRecord("event-2", now.Add(time.Second), "example.test", "203.0.113.5", 443, 5, 3),
	}

	if err := repo.SaveTrafficLogs(ctx, records); err != nil {
		t.Fatalf("SaveTrafficLogs() error = %v", err)
	}

	// Re-inserting the same event IDs must be idempotent, not duplicate or fail.
	if err := repo.SaveTrafficLogs(ctx, records); err != nil {
		t.Fatalf("idempotent SaveTrafficLogs() error = %v", err)
	}

	destinations, err := repo.GetTopDestinations(ctx, 5)
	if err != nil {
		t.Fatalf("GetTopDestinations() error = %v", err)
	}

	if len(destinations) != 1 || destinations[0].Destination != "example.test" || destinations[0].Count != 2 {
		t.Fatalf("destinations = %+v", destinations)
	}

	if destinations[0].TotalBytesClientToUpstream != 16 || destinations[0].TotalBytesUpstreamToClient != 10 {
		t.Errorf("destination byte sums = %+v", destinations[0])
	}

	clients, err := repo.GetTopClientIPs(ctx, 5)
	if err != nil {
		t.Fatalf("GetTopClientIPs() error = %v", err)
	}

	if len(clients) != 1 || clients[0].ClientIP != "192.0.2.10" || clients[0].Count != 2 {
		t.Fatalf("clients = %+v", clients)
	}

	stats, err := repo.GetTrafficStats(ctx, now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("GetTrafficStats() error = %v", err)
	}

	if stats.TotalConnections != 2 || stats.TotalBytesClientToUpstream != 16 ||
		stats.TotalBytesUpstreamToClient != 10 {
		t.Errorf("stats = %+v", stats)
	}

	logs, err := repo.GetTrafficByTimeRange(ctx, now.Add(-time.Minute), now.Add(time.Minute), 10, 0)
	if err != nil {
		t.Fatalf("GetTrafficByTimeRange() error = %v", err)
	}

	if len(logs) != 2 {
		t.Fatalf("logs = %d, want 2", len(logs))
	}

	if logs[0].RequestedHost == "" || logs[0].ClientIP == "" || logs[0].DestinationIP == "" {
		t.Errorf("stored attribution incomplete: %+v", logs[0])
	}

	if err := repo.Ping(ctx); err != nil {
		t.Errorf("Ping() error = %v", err)
	}

	// A closed pool simulates a database outage: persistence must return an error rather
	// than silently looking successful. Publisher-level retry/drop accounting is tested in
	// internal/pipeline.
	if err := repo.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if err := repo.SaveTrafficLogs(ctx, records[:1]); err == nil {
		t.Error("expected SaveTrafficLogs to fail after the database pool was closed")
	}
}

func newRecord(
	eventID string,
	timestamp time.Time,
	requestedHost string,
	destinationIP string,
	port int,
	clientToUpstream int64,
	upstreamToClient int64,
) *models.TrafficLog {
	return &models.TrafficLog{
		EventID:               eventID,
		ClientIP:              "192.0.2.10",
		ClientPort:            54321,
		RequestedHost:         requestedHost,
		DestinationIP:         destinationIP,
		DestinationPort:       port,
		Protocol:              "tcp",
		Outcome:               "connected",
		TerminationReason:     "client_close",
		Timestamp:             timestamp,
		DurationMs:            100,
		DialLatencyMs:         3,
		BytesClientToUpstream: clientToUpstream,
		BytesUpstreamToClient: upstreamToClient,
		SchemaVersion:         models.EventSchemaVersion,
	}
}

func testDatabaseConfig() *config.Config {
	host := os.Getenv("TEST_DB_HOST")
	if host == "" {
		return nil
	}

	cfg := &config.Config{}
	cfg.Database.Host = host
	cfg.Database.Port = envInt("TEST_DB_PORT", 5432)
	cfg.Database.User = envString("TEST_DB_USER", "postgres")
	cfg.Database.Password = os.Getenv("TEST_DB_PASSWORD")
	cfg.Database.Database = envString("TEST_DB_NAME", "socks5_analytics_test")
	cfg.Database.SSLMode = envString("TEST_DB_SSLMODE", "disable")
	cfg.Database.ConnectTimeoutMs = 5000
	cfg.Database.MaxOpenConns = 5
	cfg.Database.MaxIdleConns = 2

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
