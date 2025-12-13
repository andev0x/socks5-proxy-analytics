package metrics

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net/http"
)

// Metrics holds all Prometheus metrics
type Metrics struct {
	// Connection metrics
	ActiveConnections prometheus.Gauge
	TotalConnections  prometheus.Counter
	ClosedConnections prometheus.Counter

	// Traffic metrics
	BytesIn  prometheus.Counter
	BytesOut prometheus.Counter

	// Latency metrics
	LatencyHistogram prometheus.Histogram

	// Pipeline metrics
	EventsCollected   prometheus.Counter
	EventsProcessed   prometheus.Counter
	EventsPublished   prometheus.Counter
	ProcessingLatency prometheus.Histogram

	// Database metrics
	DBQueryDuration prometheus.Histogram
	DBErrors        prometheus.Counter
}

// NewMetrics creates and registers all metrics
func NewMetrics() (*Metrics, error) {
	m := &Metrics{
		ActiveConnections: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "socks5_proxy_active_connections",
			Help: "Current number of active proxy connections",
		}),
		TotalConnections: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "socks5_proxy_total_connections",
			Help: "Total number of proxy connections since start",
		}),
		ClosedConnections: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "socks5_proxy_closed_connections",
			Help: "Total number of closed proxy connections",
		}),
		BytesIn: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "socks5_proxy_bytes_in_total",
			Help: "Total bytes received by proxy",
		}),
		BytesOut: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "socks5_proxy_bytes_out_total",
			Help: "Total bytes sent by proxy",
		}),
		LatencyHistogram: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "socks5_proxy_latency_ms",
			Help:    "Distribution of connection latencies in milliseconds",
			Buckets: []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000},
		}),
		EventsCollected: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "pipeline_events_collected_total",
			Help: "Total events collected by the pipeline",
		}),
		EventsProcessed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "pipeline_events_processed_total",
			Help: "Total events processed by the normalizer",
		}),
		EventsPublished: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "pipeline_events_published_total",
			Help: "Total events published to the database",
		}),
		ProcessingLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "pipeline_processing_latency_ms",
			Help:    "Pipeline event processing latency in milliseconds",
			Buckets: []float64{1, 5, 10, 25, 50, 100, 250, 500},
		}),
		DBQueryDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "db_query_duration_ms",
			Help:    "Database query duration in milliseconds",
			Buckets: []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000},
		}),
		DBErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "db_errors_total",
			Help: "Total database errors",
		}),
	}

	// Register all metrics
	prometheus.MustRegister(
		m.ActiveConnections,
		m.TotalConnections,
		m.ClosedConnections,
		m.BytesIn,
		m.BytesOut,
		m.LatencyHistogram,
		m.EventsCollected,
		m.EventsProcessed,
		m.EventsPublished,
		m.ProcessingLatency,
		m.DBQueryDuration,
		m.DBErrors,
	)

	return m, nil
}

// StartMetricsServer starts the Prometheus metrics HTTP server
func StartMetricsServer(port int) error {
	http.Handle("/metrics", promhttp.Handler())
	addr := fmt.Sprintf("0.0.0.0:%d", port)
	return http.ListenAndServe(addr, nil)
}
