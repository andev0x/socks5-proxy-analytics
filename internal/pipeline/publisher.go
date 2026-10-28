package pipeline

import (
	"context"
	"sync"
	"time"

	"github.com/andev0x/socks5-proxy-analytics/internal/metrics"
	"github.com/andev0x/socks5-proxy-analytics/internal/models"
	"github.com/andev0x/socks5-proxy-analytics/internal/storage"
	"go.uber.org/zap"
)

// PublisherConfig holds bounded batching and retry behavior for analytics writes.
type PublisherConfig struct {
	BatchSize     int
	FlushInterval time.Duration
	MaxRetries    int
	RetryBackoff  time.Duration
	FlushTimeout  time.Duration
}

// Publisher batches normalized records and writes them to storage.
//
// Persistence is best-effort: batches are retried a bounded number of times with
// exponential backoff. After exhaustion the batch is dropped and counted; it is never
// reported as published. The final batch is flushed before the publisher exits.
type Publisher struct {
	in        <-chan *models.TrafficLog
	repo      storage.Repository
	cfg       PublisherConfig
	m         *metrics.Metrics
	log       *zap.Logger
	done      chan struct{}
	startOnce sync.Once
}

// NewPublisher creates a publisher. The input channel is closed by the normalizer.
func NewPublisher(
	in <-chan *models.TrafficLog, repo storage.Repository, cfg PublisherConfig, m *metrics.Metrics, log *zap.Logger,
) *Publisher {
	return &Publisher{
		in:   in,
		repo: repo,
		cfg:  cfg,
		m:    m,
		log:  log,
		done: make(chan struct{}),
	}
}

// Start begins processing batches. Repeated calls are no-ops.
func (p *Publisher) Start() {
	p.startOnce.Do(func() {
		go p.run()
	})
}

// Done is closed after the input channel is drained and the final batch is flushed.
func (p *Publisher) Done() <-chan struct{} {
	return p.done
}

func (p *Publisher) run() {
	defer close(p.done)

	ticker := time.NewTicker(p.cfg.FlushInterval)
	defer ticker.Stop()

	batch := make([]*models.TrafficLog, 0, p.cfg.BatchSize)

	for {
		select {
		case record, ok := <-p.in:
			if !ok {
				p.flush(batch)

				return
			}

			batch = append(batch, record)
			p.m.QueueEvents.WithLabelValues(metrics.StageNormalizer).Set(float64(len(p.in)))

			if len(batch) >= p.cfg.BatchSize {
				p.flush(batch)
				batch = make([]*models.TrafficLog, 0, p.cfg.BatchSize)
			}
		case <-ticker.C:
			if len(batch) > 0 {
				p.flush(batch)
				batch = make([]*models.TrafficLog, 0, p.cfg.BatchSize)
			}
		}
	}
}

func (p *Publisher) flush(batch []*models.TrafficLog) {
	if len(batch) == 0 {
		return
	}

	start := time.Now()
	err := p.saveWithRetry(batch)

	p.m.BatchSize.Observe(float64(len(batch)))
	p.m.PublishDuration.Observe(time.Since(start).Seconds())

	if err != nil {
		p.m.PublishFailures.Inc()
		p.m.DBErrors.Inc()
		p.m.EventsPublishDropped.Add(float64(len(batch)))
		p.log.Error("failed to persist analytics batch after retries; batch dropped",
			zap.Int("batch_size", len(batch)),
			zap.Error(err))

		return
	}

	p.m.EventsPublished.Add(float64(len(batch)))
	p.log.Debug("analytics batch persisted", zap.Int("batch_size", len(batch)))
}

func (p *Publisher) saveWithRetry(batch []*models.TrafficLog) error {
	var lastErr error

	for attempt := 0; attempt <= p.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			p.m.PublishRetries.Inc()

			backoff := p.cfg.RetryBackoff * time.Duration(1<<(attempt-1))
			if backoff > 5*time.Second {
				backoff = 5 * time.Second
			}

			time.Sleep(backoff)
		}

		ctx, cancel := context.WithTimeout(context.Background(), p.cfg.FlushTimeout)
		lastErr = p.repo.SaveTrafficLogs(ctx, batch)
		cancel()

		if lastErr == nil {
			return nil
		}

		p.log.Warn("analytics batch write failed",
			zap.Int("attempt", attempt+1),
			zap.Int("max_attempts", p.cfg.MaxRetries+1),
			zap.Int("batch_size", len(batch)),
			zap.Error(lastErr))
	}

	return lastErr
}
