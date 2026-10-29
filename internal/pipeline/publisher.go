package pipeline

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/andev0x/socks5-proxy-analytics/internal/metrics"
	"github.com/andev0x/socks5-proxy-analytics/internal/models"
	"github.com/andev0x/socks5-proxy-analytics/internal/storage"
	"go.uber.org/zap"
)

// errPublishAborted signals that the publisher was aborted during shutdown. The batch is
// counted as permanently dropped; it is never reported as published.
var errPublishAborted = errors.New("publisher aborted during shutdown")

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
//
// Abort stops further database attempts and drains the remaining queue, counting every
// record as dropped, so a shutdown deadline cannot leave the publisher retrying after the
// process has already begun closing its database pool.
type Publisher struct {
	in        <-chan *models.TrafficLog
	repo      storage.Repository
	cfg       PublisherConfig
	m         *metrics.Metrics
	log       *zap.Logger
	done      chan struct{}
	stop      chan struct{}
	stopOnce  sync.Once
	startOnce sync.Once
	ctx       context.Context
	cancel    context.CancelFunc
}

// NewPublisher creates a publisher. The input channel is closed by the normalizer.
func NewPublisher(
	in <-chan *models.TrafficLog, repo storage.Repository, cfg PublisherConfig, m *metrics.Metrics, log *zap.Logger,
) *Publisher {
	pubCtx, cancel := context.WithCancel(context.Background())

	return &Publisher{
		in:     in,
		repo:   repo,
		cfg:    cfg,
		m:      m,
		log:    log,
		done:   make(chan struct{}),
		stop:   make(chan struct{}),
		ctx:    pubCtx,
		cancel: cancel,
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

// Abort stops retrying and discards remaining records. It is safe to call more than once.
func (p *Publisher) Abort() {
	p.stopOnce.Do(func() {
		close(p.stop)
		p.cancel()
	})
}

func (p *Publisher) aborted() bool {
	select {
	case <-p.stop:
		return true
	default:
		return false
	}
}

func (p *Publisher) run() {
	defer close(p.done)

	ticker := time.NewTicker(p.cfg.FlushInterval)
	defer ticker.Stop()

	batch := make([]*models.TrafficLog, 0, p.cfg.BatchSize)

	for {
		if p.aborted() {
			p.flush(batch)
			p.drainAndDrop()

			return
		}

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
		case <-p.stop:
			p.flush(batch)
			p.drainAndDrop()

			return
		}
	}
}

// drainAndDrop consumes the remaining queue without database calls and accounts for every
// discarded record.
func (p *Publisher) drainAndDrop() {
	dropped := 0

	for range p.in {
		dropped++
	}

	if dropped > 0 {
		p.m.EventsPublishDropped.Add(float64(dropped))
		p.log.Warn("publisher aborted; remaining analytics records dropped",
			zap.Int("dropped_records", dropped))
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

		// An abort is a deliberate shutdown decision, not a database error.
		if !errors.Is(err, errPublishAborted) {
			p.m.DBErrors.Inc()
		}

		p.m.EventsPublishDropped.Add(float64(len(batch)))
		p.log.Error("failed to persist analytics batch; batch dropped",
			zap.Int("batch_size", len(batch)),
			zap.Bool("aborted", errors.Is(err, errPublishAborted)),
			zap.Error(err))

		return
	}

	p.m.EventsPublished.Add(float64(len(batch)))
	p.log.Debug("analytics batch persisted", zap.Int("batch_size", len(batch)))
}

func (p *Publisher) saveWithRetry(batch []*models.TrafficLog) error {
	var lastErr error

	for attempt := 0; attempt <= p.cfg.MaxRetries; attempt++ {
		if p.aborted() {
			return errPublishAborted
		}

		if attempt > 0 {
			p.m.PublishRetries.Inc()

			backoff := p.cfg.RetryBackoff * time.Duration(1<<(attempt-1))
			if backoff > 5*time.Second {
				backoff = 5 * time.Second
			}

			if !p.waitBackoff(backoff) {
				return errPublishAborted
			}
		}

		// Derive from the publisher context so Abort cancels an in-flight write instead of
		// leaving shutdown to wait for the full flush timeout.
		ctx, cancel := context.WithTimeout(p.ctx, p.cfg.FlushTimeout)
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

// waitBackoff sleeps for the retry backoff, returning false if the publisher was aborted.
func (p *Publisher) waitBackoff(backoff time.Duration) bool {
	if backoff <= 0 {
		return !p.aborted()
	}

	timer := time.NewTimer(backoff)
	defer timer.Stop()

	select {
	case <-p.stop:
		return false
	case <-timer.C:
		return true
	}
}
