package pipeline

import (
	"sync"

	"github.com/andev0x/socks5-proxy-analytics/internal/metrics"
	"github.com/andev0x/socks5-proxy-analytics/internal/models"
	"go.uber.org/zap"
)

// Normalizer converts raw connection events into persisted traffic records.
//
// Ownership: the normalizer owns the output channel and closes it after all workers have
// drained the input channel. The input channel is owned and closed by the collector.
type Normalizer struct {
	in        <-chan ConnectionEvent
	out       chan *models.TrafficLog
	workers   int
	m         *metrics.Metrics
	log       *zap.Logger
	wg        sync.WaitGroup
	startOnce sync.Once
	closeOnce sync.Once
}

// NewNormalizer creates a normalizer. The output channel is created by the caller so its
// capacity is explicit in pipeline wiring.
func NewNormalizer(
	in <-chan ConnectionEvent, out chan *models.TrafficLog, workers int, m *metrics.Metrics, log *zap.Logger,
) *Normalizer {
	return &Normalizer{
		in:      in,
		out:     out,
		workers: workers,
		m:       m,
		log:     log,
	}
}

// Start begins processing events. Repeated calls are no-ops.
func (n *Normalizer) Start() {
	n.startOnce.Do(func() {
		n.log.Debug("analytics normalizer started", zap.Int("workers", n.workers))

		for i := 0; i < n.workers; i++ {
			n.wg.Add(1)

			go n.process()
		}
	})
}

func (n *Normalizer) process() {
	defer n.wg.Done()

	for event := range n.in {
		record := normalize(event)
		n.out <- record
		n.m.EventsNormalized.Inc()
		n.m.QueueEvents.WithLabelValues(metrics.StageNormalizer).Set(float64(len(n.out)))
	}
}

// Wait blocks until the input channel is closed and all records have been handed to the
// publisher, then closes the output channel.
func (n *Normalizer) Wait() {
	n.wg.Wait()
	n.closeOnce.Do(func() {
		close(n.out)
	})
}

//nolint:gocritic // event is an immutable snapshot copied out of the bounded queue
func normalize(event ConnectionEvent) *models.TrafficLog {
	return &models.TrafficLog{
		EventID:               event.EventID,
		ClientIP:              event.ClientIP,
		ClientPort:            event.ClientPort,
		RequestedHost:         event.RequestedHost,
		DestinationIP:         event.DestinationIP,
		DestinationPort:       event.DestinationPort,
		Protocol:              event.Protocol,
		Outcome:               event.Outcome,
		TerminationReason:     event.TerminationReason,
		Timestamp:             event.StartedAt,
		DurationMs:            event.DurationMs,
		DialLatencyMs:         event.DialLatencyMs,
		BytesClientToUpstream: event.BytesClientToUpstream,
		BytesUpstreamToClient: event.BytesUpstreamToClient,
		SchemaVersion:         models.EventSchemaVersion,
	}
}
