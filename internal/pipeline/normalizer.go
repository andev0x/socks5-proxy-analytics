package pipeline

import (
	"github.com/andev0x/socks5-proxy-analytics/internal/models"
	"go.uber.org/zap"
)

type Normalizer struct {
	in  chan RawTrafficEvent
	out chan *models.TrafficLog
	log *zap.Logger
}

func NewNormalizer(in chan RawTrafficEvent, out chan *models.TrafficLog, log *zap.Logger) *Normalizer {
	return &Normalizer{
		in:  in,
		out: out,
		log: log,
	}
}

func (n *Normalizer) Start(numWorkers int) {
	for i := 0; i < numWorkers; i++ {
		go n.process()
	}
}

func (n *Normalizer) process() {
	for event := range n.in {
		trafficLog := &models.TrafficLog{
			SourceIP:      event.SourceIP,
			DestinationIP: event.DestinationIP,
			Domain:        event.Domain,
			Port:          event.Port,
			Timestamp:     event.Timestamp,
			LatencyMs:     event.LatencyMs,
			BytesIn:       event.BytesIn,
			BytesOut:      event.BytesOut,
			Protocol:      event.Protocol,
		}

		select {
		case n.out <- trafficLog:
		default:
			n.log.Warn("normalizer output channel full, dropping event")
		}
	}
}

func (n *Normalizer) Close() {
	close(n.out)
}
