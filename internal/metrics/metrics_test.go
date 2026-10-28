package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestNewMetricsRegistersDocumentedCollectors(t *testing.T) {
	m, err := NewMetrics()
	if err != nil {
		t.Fatalf("NewMetrics() error = %v", err)
	}

	families, err := m.Registry().Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}

	names := make(map[string]struct{}, len(families))
	for _, family := range families {
		names[family.GetName()] = struct{}{}
	}

	required := []string{
		"socks5_proxy_active_connections",
		"socks5_proxy_connections_accepted_total",
		"socks5_proxy_connections_rejected_total",
		"socks5_proxy_dial_errors_total",
		"socks5_proxy_relay_bytes_total",
		"pipeline_events_collected_total",
		"pipeline_events_dropped_total",
		"pipeline_events_published_total",
		"pipeline_publish_retries_total",
		"pipeline_queue_events",
		"process_shutdowns_total",
		"go_goroutines",
	}

	for _, name := range required {
		if _, ok := names[name]; !ok {
			t.Errorf("metric %q is not registered", name)
		}
	}
}

func TestHandlerServesLiveValues(t *testing.T) {
	m, err := NewMetrics()
	if err != nil {
		t.Fatalf("NewMetrics() error = %v", err)
	}

	m.AcceptedConnections.Add(2)
	m.ActiveConnections.Set(1)
	m.RejectedConnections.WithLabelValues(RejectRateLimited).Inc()
	m.RelayBytes.WithLabelValues(DirectionClientToUpstream).Add(42)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/metrics", http.NoBody)
	m.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}

	body := response.Body.String()
	for _, expected := range []string{
		"socks5_proxy_connections_accepted_total 2",
		"socks5_proxy_active_connections 1",
		`socks5_proxy_connections_rejected_total{reason="rate_limited"} 1`,
		`socks5_proxy_relay_bytes_total{direction="client_to_upstream"} 42`,
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("metrics body missing %q", expected)
		}
	}
}

func TestMetricsInstancesAreIndependent(t *testing.T) {
	first, err := NewMetrics()
	if err != nil {
		t.Fatalf("first NewMetrics() error = %v", err)
	}

	second, err := NewMetrics()
	if err != nil {
		t.Fatalf("second NewMetrics() error = %v", err)
	}

	first.AcceptedConnections.Inc()

	if got := testutil.ToFloat64(second.AcceptedConnections); got != 0 {
		t.Errorf("second registry accepted = %v, want 0", got)
	}
}
