// Package metrics provides Prometheus metrics collection and export.
package metrics

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net/http"
)

// Metrics holds all Prometheus metrics.
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

// NewMetrics creates and registers all metrics.
func NewMetrics() (*Metrics, error) {
	m := &Metrics{}
	m.initializeConnectionMetrics()
	m.initializeTrafficMetrics()
	m.initializePipelineMetrics()
	m.initializeDatabaseMetrics()
	m.registerAllMetrics()

	return m, nil
}

func (m *Metrics) initializeConnectionMetrics() {
	m.ActiveConnections = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "socks5_proxy_active_connections",
		Help: "Current number of active proxy connections",
	})
	m.TotalConnections = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "socks5_proxy_total_connections",
		Help: "Total number of proxy connections since start",
	})
	m.ClosedConnections = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "socks5_proxy_closed_connections",
		Help: "Total number of closed proxy connections",
	})
}

func (m *Metrics) initializeTrafficMetrics() {
	m.BytesIn = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "socks5_proxy_bytes_in_total",
		Help: "Total bytes received by proxy",
	})
	m.BytesOut = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "socks5_proxy_bytes_out_total",
		Help: "Total bytes sent by proxy",
	})
	m.LatencyHistogram = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "socks5_proxy_latency_ms",
		Help:    "Distribution of connection latencies in milliseconds",
		Buckets: []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000},
	})
}

func (m *Metrics) initializePipelineMetrics() {
	m.EventsCollected = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "pipeline_events_collected_total",
		Help: "Total events collected by the pipeline",
	})
	m.EventsProcessed = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "pipeline_events_processed_total",
		Help: "Total events processed by the normalizer",
	})
	m.EventsPublished = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "pipeline_events_published_total",
		Help: "Total events published to the database",
	})
	m.ProcessingLatency = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "pipeline_processing_latency_ms",
		Help:    "Pipeline event processing latency in milliseconds",
		Buckets: []float64{1, 5, 10, 25, 50, 100, 250, 500},
	})
}

func (m *Metrics) initializeDatabaseMetrics() {
	m.DBQueryDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "db_query_duration_ms",
		Help:    "Database query duration in milliseconds",
		Buckets: []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000},
	})
	m.DBErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "db_errors_total",
		Help: "Total database errors",
	})
}

func (m *Metrics) registerAllMetrics() {
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
}

// StartMetricsServer starts the Prometheus metrics HTTP server.
func StartMetricsServer(port int) error {
	http.Handle("/metrics", promhttp.Handler())
	addr := fmt.Sprintf("0.0.0.0:%d", port)

	return http.ListenAndServe(addr, nil)
}
