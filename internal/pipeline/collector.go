// Package pipeline provides bounded, best-effort connection-event collection, normalization and publishing.
package pipeline

import (
	"sync"
	"sync/atomic"

	"github.com/andev0x/socks5-proxy-analytics/internal/metrics"
	"go.uber.org/zap"
)

// Collector accepts connection events from the proxy without blocking the relay path.
//
// Delivery contract: best-effort. If the bounded queue is full the event is dropped,
// counted in pipeline_events_dropped_total{stage="collector"} and logged at a bounded rate.
type Collector struct {
	out     chan ConnectionEvent
	m       *metrics.Metrics
	log     *zap.Logger
	mu      sync.RWMutex
	closed  bool
	dropped atomic.Uint64
}

// NewCollector creates a bounded event collector.
func NewCollector(out chan ConnectionEvent, m *metrics.Metrics, log *zap.Logger) *Collector {
	return &Collector{
		out: out,
		m:   m,
		log: log,
	}
}

// Collect enqueues an event without blocking. It returns false when the event was dropped
// because the collector is closed or the queue is full.
//
//nolint:gocritic // event is an immutable snapshot; value semantics prevent producer/consumer aliasing
func (c *Collector) Collect(event ConnectionEvent) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.closed {
		c.m.EventsDropped.WithLabelValues(metrics.StageCollector).Inc()

		return false
	}

	select {
	case c.out <- event:
		c.m.EventsCollected.Inc()
		c.m.QueueEvents.WithLabelValues(metrics.StageCollector).Set(float64(len(c.out)))

		return true
	default:
		c.m.EventsDropped.WithLabelValues(metrics.StageCollector).Inc()
		c.m.QueueEvents.WithLabelValues(metrics.StageCollector).Set(float64(len(c.out)))
		c.logDrop()

		return false
	}
}

func (c *Collector) logDrop() {
	dropped := c.dropped.Add(1)
	if dropped == 1 || dropped%1000 == 0 {
		c.log.Warn("analytics queue full; connection event dropped",
			zap.Uint64("dropped_total", dropped),
			zap.Int("queue_capacity", cap(c.out)))
	}
}

// Close stops accepting events and closes the output channel. It is safe to call more than
// once, and it must be called only after all event producers have stopped.
func (c *Collector) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return
	}

	c.closed = true
	close(c.out)
}

// Out exposes the bounded queue for pipeline ownership wiring.
func (c *Collector) Out() <-chan ConnectionEvent {
	return c.out
}

// Dropped returns the number of events dropped by this collector.
func (c *Collector) Dropped() uint64 {
	return c.dropped.Load()
}
