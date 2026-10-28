package pipeline

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/andev0x/socks5-proxy-analytics/internal/config"
	"github.com/andev0x/socks5-proxy-analytics/internal/metrics"
	"github.com/andev0x/socks5-proxy-analytics/internal/models"
	"github.com/andev0x/socks5-proxy-analytics/internal/storage"
	"go.uber.org/zap"
)

// Pipeline owns the bounded analytics channels and their producer/consumer goroutines.
type Pipeline struct {
	collector  *Collector
	normalizer *Normalizer
	publisher  *Publisher
	startOnce  sync.Once
}

// NewPipeline wires the collector, normalizer and publisher using validated configuration.
func NewPipeline(
	cfg *config.Config, repo storage.Repository, m *metrics.Metrics, log *zap.Logger,
) *Pipeline {
	collectorIn := make(chan ConnectionEvent, cfg.Pipeline.BufferSize)
	normalizerOut := make(chan *models.TrafficLog, cfg.Pipeline.BufferSize)

	collector := NewCollector(collectorIn, m, log)
	normalizer := NewNormalizer(collectorIn, normalizerOut, cfg.Pipeline.Workers, m, log)
	publisher := NewPublisher(
		normalizerOut,
		repo,
		PublisherConfig{
			BatchSize:     cfg.Pipeline.BatchSize,
			FlushInterval: time.Duration(cfg.Pipeline.FlushInterval) * time.Millisecond,
			MaxRetries:    cfg.Pipeline.MaxRetries,
			RetryBackoff:  time.Duration(cfg.Pipeline.RetryBackoffMs) * time.Millisecond,
			FlushTimeout:  time.Duration(cfg.Pipeline.FlushTimeoutMs) * time.Millisecond,
		},
		m,
		log,
	)

	return &Pipeline{
		collector:  collector,
		normalizer: normalizer,
		publisher:  publisher,
	}
}

// Start launches normalizer workers and the publisher. Repeated calls are no-ops.
func (p *Pipeline) Start() {
	p.startOnce.Do(func() {
		p.normalizer.Start()
		p.publisher.Start()
	})
}

// Emit submits an event without blocking the forwarding path.
//
//nolint:gocritic // event is an immutable snapshot; value semantics prevent producer/consumer aliasing
func (p *Pipeline) Emit(event ConnectionEvent) bool {
	return p.collector.Collect(event)
}

// Collector exposes the event sink for the proxy.
func (p *Pipeline) Collector() *Collector {
	return p.collector
}

// Shutdown stops event production, drains the normalizer and flushes pending batches,
// bounded by ctx. It must be called only after the proxy stopped emitting events.
func (p *Pipeline) Shutdown(ctx context.Context) error {
	p.collector.Close()

	done := make(chan struct{})
	go func() {
		p.normalizer.Wait()
		<-p.publisher.Done()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return errors.New("analytics pipeline shutdown deadline exceeded: pending events may be lost")
	}
}
