package pipeline

import (
	"time"

	"go.uber.org/zap"
)

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

type Collector struct {
	out chan RawTrafficEvent
	log *zap.Logger
}

func NewCollector(out chan RawTrafficEvent, log *zap.Logger) *Collector {
	return &Collector{
		out: out,
		log: log,
	}
}

func (c *Collector) Collect(event RawTrafficEvent) error {
	select {
	case c.out <- event:
		return nil
	default:
		c.log.Warn("collector channel full, dropping event")
		return nil
	}
}
