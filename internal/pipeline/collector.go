// Package pipeline provides traffic event collection, normalization, and publishing.
package pipeline

import (
	"time"

	"go.uber.org/zap"
)

// RawTrafficEvent represents an unprocessed traffic event from the proxy.
type RawTrafficEvent struct {
	SourceIP      string
	DestinationIP string
	Domain        string
	Port          int
	Timestamp     time.Time
	LatencyMs     int64
	BytesIn       int64
	BytesOut      int64
	Protocol      string
}

// Collector collects raw traffic events from the proxy.
type Collector struct {
	out chan RawTrafficEvent
	log *zap.Logger
}

// NewCollector creates a new traffic event collector.
func NewCollector(out chan RawTrafficEvent, log *zap.Logger) *Collector {
	return &Collector{
		out: out,
		log: log,
	}
}

// Collect adds a raw traffic event to the collection channel.
func (c *Collector) Collect(event RawTrafficEvent) error {
	select {
	case c.out <- event:
		return nil
	default:
		c.log.Warn("collector channel full, dropping event")

		return nil
	}
}
