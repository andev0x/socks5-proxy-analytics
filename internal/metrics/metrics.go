// Package metrics defines and exposes the Prometheus metrics of the proxy and analytics pipeline.
package metrics

import (
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	labelReason    = "reason"
	labelDirection = "direction"
	labelStage     = "stage"
	labelResult    = "result"
)

// Bounded label values used by the metrics. Keep this list small and enum-like.
const (
	RejectMaxConnections = "max_connections"
	RejectRateLimited    = "rate_limited"
	RejectNotAllowed     = "ip_not_allowed"
	RejectShuttingDown   = "shutting_down"

	HandshakeMalformedGreeting = "malformed_greeting"
	HandshakeNoAcceptableAuth  = "no_acceptable_auth"
	HandshakeAuthFailed        = "auth_failed"
	HandshakeMalformedRequest  = "malformed_request"
	HandshakeUnsupportedCmd    = "unsupported_command"
	HandshakeUnsupportedAddr   = "unsupported_address_type"
	HandshakeTimeout           = "timeout"

	DirectionClientToUpstream = "client_to_upstream"
	DirectionUpstreamToClient = "upstream_to_client"

	StageCollector  = "collector"
	StageNormalizer = "normalizer"

	ShutdownClean   = "clean"
	ShutdownTimeout = "timeout"
)

// Metrics holds all Prometheus collectors owned by one process.
type Metrics struct {
	registry *prometheus.Registry

	ActiveConnections   prometheus.Gauge
	AcceptedConnections prometheus.Counter
	RejectedConnections *prometheus.CounterVec
	AuthFailures        prometheus.Counter
	HandshakeFailures   *prometheus.CounterVec
	DialErrors          *prometheus.CounterVec
	DialLatency         prometheus.Histogram
	ConnectionDuration  prometheus.Histogram
	RelayBytes          *prometheus.CounterVec
	RelayErrors         prometheus.Counter

	EventsCollected      prometheus.Counter
	EventsDropped        *prometheus.CounterVec
	QueueEvents          *prometheus.GaugeVec
	EventsNormalized     prometheus.Counter
	EventsPublished      prometheus.Counter
	EventsPublishDropped prometheus.Counter
	PublishFailures      prometheus.Counter
	PublishRetries       prometheus.Counter
	PublishDuration      prometheus.Histogram
	BatchSize            prometheus.Histogram
	DBErrors             prometheus.Counter

	Shutdowns *prometheus.CounterVec
}

// NewMetrics creates a dedicated registry and registers all metrics on it.
func NewMetrics() (*Metrics, error) {
	m := &Metrics{registry: prometheus.NewRegistry()}

	m.initializeConnectionMetrics()
	m.initializeTrafficMetrics()
	m.initializePipelineMetrics()
	m.initializeDatabaseMetrics()
	m.initializeLabelSeries()

	if err := m.registerAllMetrics(); err != nil {
		return nil, err
	}

	return m, nil
}

func (m *Metrics) initializeConnectionMetrics() {
	m.ActiveConnections = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "socks5_proxy_active_connections",
		Help: "Current number of active SOCKS5 sessions admitted by connection and client rules.",
	})
	m.AcceptedConnections = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "socks5_proxy_connections_accepted_total",
		Help: "SOCKS5 sessions admitted by connection limits and client rules.",
	})
	m.RejectedConnections = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "socks5_proxy_connections_rejected_total",
		Help: "Connections refused before the SOCKS5 handshake, by bounded reason.",
	}, []string{labelReason})
	m.AuthFailures = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "socks5_proxy_auth_failures_total",
		Help: "SOCKS5 username/password authentication failures.",
	})
	m.HandshakeFailures = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "socks5_proxy_handshake_failures_total",
		Help: "SOCKS5 handshake and request failures, by bounded reason.",
	}, []string{labelReason})
}

func (m *Metrics) initializeTrafficMetrics() {
	m.DialErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "socks5_proxy_dial_errors_total",
		Help: "Upstream TCP dial failures, by bounded error class.",
	}, []string{labelReason})
	m.DialLatency = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "socks5_proxy_dial_latency_seconds",
		Help:    "Upstream TCP connect latency in seconds.",
		Buckets: prometheus.DefBuckets,
	})
	m.ConnectionDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "socks5_proxy_connection_duration_seconds",
		Help:    "SOCKS5 session duration in seconds (accept to relay completion).",
		Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 300},
	})
	m.RelayBytes = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "socks5_proxy_relay_bytes_total",
		Help: "Application payload bytes relayed, excluding SOCKS5 handshake bytes.",
	}, []string{labelDirection})
	m.RelayErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "socks5_proxy_relay_errors_total",
		Help: "Relay copy errors after a successful upstream connection.",
	})
}

func (m *Metrics) initializePipelineMetrics() {
	m.EventsCollected = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "pipeline_events_collected_total",
		Help: "Connection events accepted into the bounded analytics queue.",
	})
	m.EventsDropped = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "pipeline_events_dropped_total",
		Help: "Connection events dropped before persistence, by pipeline stage.",
	}, []string{labelStage})
	m.QueueEvents = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "pipeline_queue_events",
		Help: "Current number of queued events, by pipeline stage.",
	}, []string{labelStage})
	m.EventsNormalized = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "pipeline_events_normalized_total",
		Help: "Connection events converted to normalized traffic records.",
	})
	m.EventsPublished = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "pipeline_events_published_total",
		Help: "Traffic records successfully written to PostgreSQL.",
	})
	m.EventsPublishDropped = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "pipeline_events_publish_dropped_total",
		Help: "Traffic records lost after all publisher retries were exhausted.",
	})
	m.PublishFailures = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "pipeline_publish_failures_total",
		Help: "Publisher batches that failed after all retries.",
	})
	m.PublishRetries = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "pipeline_publish_retries_total",
		Help: "Publisher batch write retries.",
	})
	m.PublishDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "pipeline_publish_duration_seconds",
		Help:    "Publisher batch write duration in seconds, including retries.",
		Buckets: prometheus.DefBuckets,
	})
	m.BatchSize = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "pipeline_batch_size",
		Help:    "Number of traffic records per published batch.",
		Buckets: []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000},
	})
}

func (m *Metrics) initializeDatabaseMetrics() {
	m.DBErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "db_errors_total",
		Help: "Database write errors observed by the publisher.",
	})
	m.Shutdowns = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "process_shutdowns_total",
		Help: "Process shutdown outcomes, by bounded result.",
	}, []string{labelResult})
}

// initializeLabelSeries materializes bounded label combinations so every documented metric is
// scrapeable at zero before the first event occurs.
func (m *Metrics) initializeLabelSeries() {
	for _, reason := range []string{
		RejectMaxConnections, RejectRateLimited, RejectNotAllowed, RejectShuttingDown,
	} {
		m.RejectedConnections.WithLabelValues(reason)
	}

	for _, reason := range []string{
		HandshakeMalformedGreeting, HandshakeNoAcceptableAuth, HandshakeAuthFailed,
		HandshakeMalformedRequest, HandshakeUnsupportedCmd, HandshakeUnsupportedAddr, HandshakeTimeout,
	} {
		m.HandshakeFailures.WithLabelValues(reason)
	}

	for _, reason := range []string{"dns", "timeout", "canceled", "refused", "network_unreachable", "other"} {
		m.DialErrors.WithLabelValues(reason)
	}

	for _, direction := range []string{DirectionClientToUpstream, DirectionUpstreamToClient} {
		m.RelayBytes.WithLabelValues(direction)
	}

	for _, stage := range []string{StageCollector, StageNormalizer} {
		m.EventsDropped.WithLabelValues(stage)
		m.QueueEvents.WithLabelValues(stage)
	}

	for _, result := range []string{ShutdownClean, ShutdownTimeout} {
		m.Shutdowns.WithLabelValues(result)
	}
}

func (m *Metrics) registerAllMetrics() error {
	collectorsToRegister := []prometheus.Collector{
		m.ActiveConnections,
		m.AcceptedConnections,
		m.RejectedConnections,
		m.AuthFailures,
		m.HandshakeFailures,
		m.DialErrors,
		m.DialLatency,
		m.ConnectionDuration,
		m.RelayBytes,
		m.RelayErrors,
		m.EventsCollected,
		m.EventsDropped,
		m.QueueEvents,
		m.EventsNormalized,
		m.EventsPublished,
		m.EventsPublishDropped,
		m.PublishFailures,
		m.PublishRetries,
		m.PublishDuration,
		m.BatchSize,
		m.DBErrors,
		m.Shutdowns,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	}

	for _, collector := range collectorsToRegister {
		if err := m.registry.Register(collector); err != nil {
			return fmt.Errorf("failed to register metric: %w", err)
		}
	}

	return nil
}

// Registry exposes the registry for tests and custom HTTP handlers.
func (m *Metrics) Registry() *prometheus.Registry {
	return m.registry
}

// Handler returns the Prometheus scrape handler for this registry.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}
