package pipeline

import (
	"context"
	"sync"
	"time"

	"github.com/andev0x/socks5-proxy-analytics/internal/models"
	"github.com/andev0x/socks5-proxy-analytics/internal/storage"
	"go.uber.org/zap"
)

type Publisher struct {
	in          chan *models.TrafficLog
	repo        storage.Repository
	batchSize   int
	flushTicker *time.Ticker
	log         *zap.Logger
	wg          sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc
}

func NewPublisher(
	in chan *models.TrafficLog,
	repo storage.Repository,
	batchSize int,
	flushIntervalMs int,
	log *zap.Logger,
) *Publisher {
	ctx, cancel := context.WithCancel(context.Background())

	return &Publisher{
		in:          in,
		repo:        repo,
		batchSize:   batchSize,
		flushTicker: time.NewTicker(time.Duration(flushIntervalMs) * time.Millisecond),
		log:         log,
		ctx:         ctx,
		cancel:      cancel,
	}
}

func (p *Publisher) Start() {
	p.wg.Add(1)
	go p.processBatch()
}

func (p *Publisher) processBatch() {
	defer p.wg.Done()

	batch := make([]*models.TrafficLog, 0, p.batchSize)
	defer func() {
		if len(batch) > 0 {
			p.flushBatch(batch)
		}
		p.flushTicker.Stop()
	}()

	for {
		select {
		case <-p.ctx.Done():
			return
		case log := <-p.in:
			if log == nil {
				return
			}
			batch = append(batch, log)
			if len(batch) >= p.batchSize {
				p.flushBatch(batch)
				batch = make([]*models.TrafficLog, 0, p.batchSize)
			}
		case <-p.flushTicker.C:
			if len(batch) > 0 {
				p.flushBatch(batch)
				batch = make([]*models.TrafficLog, 0, p.batchSize)
			}
		}
	}
}

func (p *Publisher) flushBatch(batch []*models.TrafficLog) {
	ctx, cancel := context.WithTimeout(p.ctx, 30*time.Second)
	defer cancel()

	if err := p.repo.SaveTrafficLogs(ctx, batch); err != nil {
		p.log.Error("failed to save traffic logs", zap.Error(err), zap.Int("batch_size", len(batch)))
	} else {
		p.log.Debug("batch saved successfully", zap.Int("batch_size", len(batch)))
	}
}

func (p *Publisher) Stop() {
	p.cancel()
	p.wg.Wait()
}

func (p *Publisher) Close() {
	close(p.in)
	p.Stop()
}
