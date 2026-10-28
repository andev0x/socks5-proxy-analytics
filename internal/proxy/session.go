package proxy

import (
	"bufio"
	"io"
	"net"
	"sync/atomic"
	"time"

	"github.com/andev0x/socks5-proxy-analytics/internal/metrics"
	"github.com/andev0x/socks5-proxy-analytics/internal/pipeline"
)

type session struct {
	startedAt time.Time
	endedAt   time.Time

	clientIP   string
	clientPort int

	requestedHost   string
	destinationIP   string
	destinationPort int

	dialLatencyMs int64
	outcome       string
	reason        string
	clientReader  *bufio.Reader

	bytesClientToUpstream atomic.Int64
	bytesUpstreamToClient atomic.Int64
}

func newSession(remote net.Addr, startedAt time.Time) *session {
	sess := &session{startedAt: startedAt}

	if tcpAddr, ok := remote.(*net.TCPAddr); ok {
		sess.clientIP = tcpAddr.IP.String()
		sess.clientPort = tcpAddr.Port
	} else if remote != nil {
		host, port, err := net.SplitHostPort(remote.String())
		if err == nil {
			sess.clientIP = host
			sess.clientPort = portFromString(port)
		}
	}

	return sess
}

func portFromString(port string) int {
	var value int

	for _, char := range port {
		if char < '0' || char > '9' {
			return 0
		}

		value = value*10 + int(char-'0')
	}

	return value
}

func (s *session) finish(endedAt time.Time) {
	s.endedAt = endedAt
}

func (s *session) durationMs() int64 {
	if s.endedAt.IsZero() {
		return 0
	}

	return s.endedAt.Sub(s.startedAt).Milliseconds()
}

func (s *session) event() pipeline.ConnectionEvent {
	return pipeline.ConnectionEvent{
		EventID:               pipeline.NewEventID(),
		StartedAt:             s.startedAt,
		EndedAt:               s.endedAt,
		ClientIP:              s.clientIP,
		ClientPort:            s.clientPort,
		RequestedHost:         s.requestedHost,
		DestinationIP:         s.destinationIP,
		DestinationPort:       s.destinationPort,
		Protocol:              "tcp",
		Outcome:               s.outcome,
		TerminationReason:     s.reason,
		DialLatencyMs:         s.dialLatencyMs,
		DurationMs:            s.durationMs(),
		BytesClientToUpstream: s.bytesClientToUpstream.Load(),
		BytesUpstreamToClient: s.bytesUpstreamToClient.Load(),
	}
}

type relayResult struct {
	direction string
	err       error
}

func (s *Server) relay(client, upstream net.Conn, sess *session) {
	results := make(chan relayResult, 2)
	clientSource := io.Reader(client)
	if sess.clientReader != nil {
		clientSource = sess.clientReader
	}

	go relayCopy(upstream, clientSource, &sess.bytesClientToUpstream, s.m, metrics.DirectionClientToUpstream, results)
	go relayCopy(client, upstream, &sess.bytesUpstreamToClient, s.m, metrics.DirectionUpstreamToClient, results)

	first := <-results
	if first.err != nil {
		_ = client.Close()
		_ = upstream.Close()
	}

	second := <-results

	if sess.reason == "" {
		sess.reason = relayTerminationReason(s, first, second)
	}

	if !s.forced.Load() && (first.err != nil || second.err != nil) {
		s.m.RelayErrors.Inc()
	}
}

func relayCopy(
	dst net.Conn,
	src io.Reader,
	counter *atomic.Int64,
	m *metrics.Metrics,
	direction string,
	results chan<- relayResult,
) {
	_, err := io.Copy(countingWriter{dst: dst, counter: counter, m: m, direction: direction}, readerOnly{reader: src})

	if closeWriter, ok := dst.(interface{ CloseWrite() error }); ok {
		_ = closeWriter.CloseWrite()
	}

	results <- relayResult{direction: direction, err: err}
}

func relayTerminationReason(s *Server, first, second relayResult) string {
	if s.forced.Load() {
		return pipeline.ReasonServerShutdown
	}

	if first.err != nil || second.err != nil {
		return pipeline.ReasonRelayError
	}

	if first.direction == metrics.DirectionClientToUpstream {
		return pipeline.ReasonClientClose
	}

	return pipeline.ReasonUpstreamClose
}

type countingWriter struct {
	dst       io.Writer
	counter   *atomic.Int64
	m         *metrics.Metrics
	direction string
}

func (w countingWriter) Write(p []byte) (int, error) {
	n, err := w.dst.Write(p)
	if n > 0 {
		w.counter.Add(int64(n))
		w.m.RelayBytes.WithLabelValues(w.direction).Add(float64(n))
	}

	return n, err
}

type readerOnly struct {
	reader io.Reader
}

func (r readerOnly) Read(p []byte) (int, error) {
	return r.reader.Read(p)
}
