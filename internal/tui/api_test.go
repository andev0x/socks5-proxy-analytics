package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/andev0x/socks5-proxy-analytics/internal/models"
)

func TestAPIClientHealthAndStats(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/logs/traffic", func(w http.ResponseWriter, r *http.Request) {
		logs := []models.TrafficLog{{EventID: "abc", ClientIP: "127.0.0.1", Outcome: "connected"}}
		_ = json.NewEncoder(w).Encode(logs)
	})
	mux.HandleFunc("/stats/traffic", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(models.TrafficStats{TotalConnections: 5})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewAPIClient(srv.URL, &http.Client{})
	if err := c.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	stats, err := c.TrafficStats(context.Background(), now.Add(-time.Hour), now)
	if err != nil || stats.TotalConnections != 5 {
		t.Fatalf("stats err=%v", err)
	}
	logs, err := c.TrafficLogs(context.Background(), 10, 0, now.Add(-time.Hour), now)
	if err != nil || len(logs) != 1 {
		t.Fatalf("logs err=%v len=%d", err, len(logs))
	}
}
