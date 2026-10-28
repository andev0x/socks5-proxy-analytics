package pipeline

import (
	"testing"
	"time"

	"github.com/andev0x/socks5-proxy-analytics/internal/metrics"
	"github.com/andev0x/socks5-proxy-analytics/internal/models"
	"go.uber.org/zap"
)

func metricsForBenchmark() (*metrics.Metrics, error) {
	return metrics.NewMetrics()
}

func BenchmarkCollectorEnqueue(b *testing.B) {
	m, err := metricsForBenchmark()
	if err != nil {
		b.Fatalf("metrics: %v", err)
	}

	queue := make(chan ConnectionEvent, 1024)
	collector := NewCollector(queue, m, zap.NewNop())
	event := sampleEvent()

	defer collector.Close()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// Drain in the benchmark loop so the accepted path is measured without relying on
		// goroutine scheduling; production drops on saturation and is covered by unit tests.
		if len(queue) == cap(queue) {
			<-queue
		}

		if !collector.Collect(event) {
			b.Fatal("unexpected drop while the benchmark drains the queue")
		}
	}
}

func BenchmarkEventNormalization(b *testing.B) {
	event := sampleEvent()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = normalize(event)
	}
}

func BenchmarkPublisherBatchPersist(b *testing.B) {
	m, err := metricsForBenchmark()
	if err != nil {
		b.Fatalf("metrics: %v", err)
	}

	const batchSize = 100

	publisher := NewPublisher(
		make(chan *models.TrafficLog, 1),
		&fakeRepo{discard: true},
		PublisherConfig{
			BatchSize:     batchSize,
			FlushInterval: time.Hour,
			MaxRetries:    0,
			RetryBackoff:  0,
			FlushTimeout:  time.Second,
		},
		m,
		zap.NewNop(),
	)

	batch := make([]*models.TrafficLog, 0, batchSize)
	for i := 0; i < batchSize; i++ {
		batch = append(batch, trafficRecord())
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		publisher.flush(batch)
	}

	b.ReportMetric(batchSize, "records/op")
}
