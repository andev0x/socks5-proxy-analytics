package tui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

const sampleMetrics = `# HELP socks5_proxy_active_connections x
# TYPE socks5_proxy_active_connections gauge
socks5_proxy_active_connections 3
# TYPE socks5_proxy_connections_accepted_total counter
socks5_proxy_connections_accepted_total 10
# TYPE socks5_proxy_connections_rejected_total counter
socks5_proxy_connections_rejected_total{reason="max_connections"} 2
# TYPE socks5_proxy_relay_bytes_total counter
socks5_proxy_relay_bytes_total{direction="client_to_upstream"} 1024
socks5_proxy_relay_bytes_total{direction="upstream_to_client"} 2048
`

func TestScrapeMetricsSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(sampleMetrics))
	}))
	defer srv.Close()

	client := &http.Client{}
	snap := ScrapeMetrics(context.Background(), client, srv.URL)
	if !snap.OK {
		t.Fatalf("scrape failed: %s", snap.Err)
	}
	v, ok := snap.Scalar("socks5_proxy_active_connections")
	if !ok || v != 3 {
		t.Fatalf("active=%v ok=%v", v, ok)
	}
	if got := snap.LabelValue("socks5_proxy_relay_bytes_total", "client_to_upstream"); got != 1024 {
		t.Fatalf("got %v", got)
	}
	sum, ok := snap.VectorSum("socks5_proxy_connections_rejected_total")
	if !ok || sum != 2 {
		t.Fatalf("sum=%v ok=%v", sum, ok)
	}
}

func TestScrapeMetricsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	snap := ScrapeMetrics(context.Background(), &http.Client{}, srv.URL)
	if snap.OK {
		t.Fatal("expected failure")
	}
	if snap.Err == "" {
		t.Fatal("expected error")
	}
}
