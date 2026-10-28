package pipeline

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/andev0x/socks5-proxy-analytics/internal/config"
	"github.com/andev0x/socks5-proxy-analytics/internal/metrics"
	"github.com/andev0x/socks5-proxy-analytics/internal/models"
	"github.com/andev0x/socks5-proxy-analytics/internal/storage"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.uber.org/zap"
)

func TestCollectorDropsWhenQueueFull(t *testing.T) {
	m := mustMetrics(t)
	queue := make(chan ConnectionEvent, 1)
	collector := NewCollector(queue, m, zap.NewNop())

	if !collector.Collect(sampleEvent()) {
		t.Fatal("first event should be accepted")
	}

	if collector.Collect(sampleEvent()) {
		t.Fatal("second event should be dropped when the queue is full")
	}

	if got := testutil.ToFloat64(m.EventsDropped.WithLabelValues(metrics.StageCollector)); got != 1 {
		t.Fatalf("dropped metric = %v, want 1", got)
	}

	if collector.Dropped() != 1 {
		t.Fatalf("Dropped() = %d, want 1", collector.Dropped())
	}

	collector.Close()

	if collector.Collect(sampleEvent()) {
		t.Fatal("collector must refuse events after close")
	}
}

func TestNormalizerMapsEventAndClosesOutput(t *testing.T) {
	m := mustMetrics(t)
	in := make(chan ConnectionEvent, 1)
	out := make(chan *models.TrafficLog, 1)
	normalizer := NewNormalizer(in, out, 2, m, zap.NewNop())
	normalizer.Start()

	event := sampleEvent()
	in <- event
	close(in)

	normalizer.Wait()

	record, ok := <-out
	if !ok {
		t.Fatal("expected one normalized record")
	}

	if record.ClientIP != event.ClientIP || record.RequestedHost != event.RequestedHost {
		t.Errorf("attribution mismatch: %+v", record)
	}

	if record.BytesClientToUpstream != event.BytesClientToUpstream ||
		record.BytesUpstreamToClient != event.BytesUpstreamToClient {
		t.Errorf("byte direction mismatch: %+v", record)
	}

	if record.SchemaVersion != models.EventSchemaVersion {
		t.Errorf("schema version = %d", record.SchemaVersion)
	}

	if _, ok := <-out; ok {
		t.Fatal("normalizer must close its output channel")
	}
}

func TestPublisherFlushesOnBatchAndClose(t *testing.T) {
	m := mustMetrics(t)
	repo := &fakeRepo{}
	in := make(chan *models.TrafficLog, 10)
	publisher := NewPublisher(in, repo, PublisherConfig{
		BatchSize:     2,
		FlushInterval: time.Hour,
		MaxRetries:    0,
		RetryBackoff:  time.Millisecond,
		FlushTimeout:  time.Second,
	}, m, zap.NewNop())
	publisher.Start()

	in <- trafficRecord()
	in <- trafficRecord()
	in <- trafficRecord()
	close(in)
	<-publisher.Done()

	if got := repo.count(); got != 3 {
		t.Fatalf("saved records = %d, want 3", got)
	}

	if got := testutil.ToFloat64(m.EventsPublished); got != 3 {
		t.Fatalf("published metric = %v, want 3", got)
	}
}

func TestPublisherRetriesAndSucceeds(t *testing.T) {
	m := mustMetrics(t)
	repo := &fakeRepo{failuresRemaining: 2}
	in := make(chan *models.TrafficLog, 1)
	publisher := NewPublisher(in, repo, PublisherConfig{
		BatchSize:     1,
		FlushInterval: time.Hour,
		MaxRetries:    2,
		RetryBackoff:  time.Millisecond,
		FlushTimeout:  time.Second,
	}, m, zap.NewNop())
	publisher.Start()

	in <- trafficRecord()
	close(in)
	<-publisher.Done()

	if got := repo.count(); got != 1 {
		t.Fatalf("saved records = %d, want 1 after retry", got)
	}

	if got := testutil.ToFloat64(m.PublishRetries); got != 2 {
		t.Fatalf("retry metric = %v, want 2", got)
	}

	if got := testutil.ToFloat64(m.EventsPublishDropped); got != 0 {
		t.Fatalf("dropped metric = %v, want 0", got)
	}
}

func TestPublisherReportsExhaustedRetriesAsLoss(t *testing.T) {
	m := mustMetrics(t)
	repo := &fakeRepo{failuresRemaining: 1000}
	in := make(chan *models.TrafficLog, 1)
	publisher := NewPublisher(in, repo, PublisherConfig{
		BatchSize:     1,
		FlushInterval: time.Hour,
		MaxRetries:    1,
		RetryBackoff:  time.Millisecond,
		FlushTimeout:  time.Second,
	}, m, zap.NewNop())
	publisher.Start()

	in <- trafficRecord()
	close(in)
	<-publisher.Done()

	if got := repo.count(); got != 0 {
		t.Fatalf("saved records = %d, want 0", got)
	}

	if got := testutil.ToFloat64(m.PublishFailures); got != 1 {
		t.Fatalf("failure metric = %v, want 1", got)
	}

	if got := testutil.ToFloat64(m.EventsPublishDropped); got != 1 {
		t.Fatalf("dropped metric = %v, want 1", got)
	}

	if got := testutil.ToFloat64(m.EventsPublished); got != 0 {
		t.Fatalf("published metric = %v, want 0 (failure must not look successful)", got)
	}
}

func TestPipelineShutdownFlushesPendingEvents(t *testing.T) {
	m := mustMetrics(t)
	repo := &fakeRepo{}
	cfg := testConfig()
	analytics := NewPipeline(cfg, repo, m, zap.NewNop())
	analytics.Start()

	if !analytics.Emit(sampleEvent()) {
		t.Fatal("event should be accepted")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := analytics.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	if got := repo.count(); got != 1 {
		t.Fatalf("saved records after shutdown = %d, want 1", got)
	}
}

func TestPipelineConcurrentEvents(t *testing.T) {
	m := mustMetrics(t)
	repo := &fakeRepo{}
	cfg := testConfig()
	cfg.Pipeline.BufferSize = 2000
	cfg.Pipeline.BatchSize = 25
	cfg.Pipeline.FlushInterval = 10

	analytics := NewPipeline(cfg, repo, m, zap.NewNop())
	analytics.Start()

	const (
		producers       = 10
		eventsPerWriter = 100
	)

	var wg sync.WaitGroup
	dropped := make(chan struct{}, producers*eventsPerWriter)

	for i := 0; i < producers; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for j := 0; j < eventsPerWriter; j++ {
				if !analytics.Emit(sampleEvent()) {
					dropped <- struct{}{}
				}
			}
		}()
	}

	wg.Wait()
	close(dropped)

	if len(dropped) != 0 {
		t.Fatalf("dropped %d events with a sufficiently large queue", len(dropped))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := analytics.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	if got := repo.count(); got != producers*eventsPerWriter {
		t.Fatalf("persisted records = %d, want %d", got, producers*eventsPerWriter)
	}
}

func TestPipelineShutdownHonorsDeadline(t *testing.T) {
	m := mustMetrics(t)
	repo := &fakeRepo{block: make(chan struct{})}
	defer close(repo.block)

	cfg := testConfig()
	cfg.Pipeline.MaxRetries = 0
	cfg.Pipeline.FlushTimeoutMs = 5000
	analytics := NewPipeline(cfg, repo, m, zap.NewNop())
	analytics.Start()

	analytics.Emit(sampleEvent())

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if err := analytics.Shutdown(ctx); err == nil {
		t.Fatal("expected shutdown deadline error when persistence is blocked")
	}
}

func sampleEvent() ConnectionEvent {
	now := time.Now().UTC()

	return ConnectionEvent{
		EventID:               NewEventID(),
		StartedAt:             now,
		EndedAt:               now.Add(100 * time.Millisecond),
		ClientIP:              "192.0.2.10",
		ClientPort:            12345,
		RequestedHost:         "example.test",
		DestinationIP:         "203.0.113.5",
		DestinationPort:       443,
		Protocol:              "tcp",
		Outcome:               OutcomeConnected,
		TerminationReason:     ReasonClientClose,
		DialLatencyMs:         5,
		DurationMs:            100,
		BytesClientToUpstream: 11,
		BytesUpstreamToClient: 7,
	}
}

func trafficRecord() *models.TrafficLog {
	event := sampleEvent()

	return normalize(event)
}

func testConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Pipeline.Workers = 2
	cfg.Pipeline.BufferSize = 10
	cfg.Pipeline.BatchSize = 2
	cfg.Pipeline.FlushInterval = 1000
	cfg.Pipeline.MaxRetries = 0
	cfg.Pipeline.RetryBackoffMs = 1
	cfg.Pipeline.FlushTimeoutMs = 1000

	return cfg
}

func mustMetrics(t *testing.T) *metrics.Metrics {
	t.Helper()

	m, err := metrics.NewMetrics()
	if err != nil {
		t.Fatalf("NewMetrics() error = %v", err)
	}

	return m
}

type fakeRepo struct {
	mu                sync.Mutex
	saved             []*models.TrafficLog
	failuresRemaining int
	block             chan struct{}
	discard           bool
}

func (r *fakeRepo) SaveTrafficLog(_ context.Context, log *models.TrafficLog) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.saved = append(r.saved, log)

	return nil
}

func (r *fakeRepo) SaveTrafficLogs(ctx context.Context, logs []*models.TrafficLog) error {
	if r.block != nil {
		select {
		case <-r.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.failuresRemaining > 0 {
		r.failuresRemaining--

		return errors.New("injected persistence failure")
	}

	if !r.discard {
		r.saved = append(r.saved, logs...)
	}

	return nil
}

func (r *fakeRepo) GetTopDestinations(context.Context, int) ([]models.DestinationStats, error) {
	return nil, nil
}

func (r *fakeRepo) GetTopClientIPs(context.Context, int) ([]models.ClientIPStats, error) {
	return nil, nil
}

func (r *fakeRepo) GetTrafficStats(
	context.Context, time.Time, time.Time,
) (*models.TrafficStats, error) {
	return &models.TrafficStats{}, nil
}

func (r *fakeRepo) GetTrafficByTimeRange(
	context.Context, time.Time, time.Time, int, int,
) ([]models.TrafficLog, error) {
	return nil, nil
}

func (r *fakeRepo) Ping(context.Context) error {
	return nil
}

func (r *fakeRepo) Close() error {
	return nil
}

func (r *fakeRepo) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.saved)
}

var _ storage.Repository = (*fakeRepo)(nil)
