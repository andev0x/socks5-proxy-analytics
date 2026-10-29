package proxy

import (
	"bufio"
	"io"
	"net"
	"sync/atomic"
	"time"

	"github.com/andev0x/socks5-proxy-analytics/internal/metrics"
	"github.com/andev0x/socks5-proxy-analytics/internal/pipeline"
	"github.com/prometheus/client_golang/prometheus"
)

type session struct {
	startedAt time.Time
	endedAt   time.Time

	clientIP   string
	clientPort int

	workloadID      string
	requestedHost   string
	destinationIP   string
	destinationPort int

	policyDecision string
	policyReason   string
	policyID       string
	policyVersion  string

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
		WorkloadID:            s.workloadID,
		PolicyDecision:        s.policyDecision,
		PolicyReason:          s.policyReason,
		PolicyID:              s.policyID,
		PolicyVersion:         s.policyVersion,
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

// relayBufferSize returns the configured per-direction copy buffer size in bytes.
func (s *Server) relayBufferSize() int {
	if s.cfg == nil || s.cfg.Proxy.RelayBufferKB <= 0 {
		return 32 * 1024
	}

	return s.cfg.Proxy.RelayBufferKB * 1024
}

func (s *Server) relay(client, upstream net.Conn, sess *session, idleTimeout time.Duration) {
	results := make(chan relayResult, 2)
	clientSource := io.Reader(client)
	if sess.clientReader != nil {
		clientSource = sess.clientReader
	}

	if idleTimeout > 0 {
		deadline := time.Now().Add(idleTimeout)
		_ = client.SetDeadline(deadline)
		_ = upstream.SetDeadline(deadline)
	}

	go relayCopy(
		upstream, clientSource, &sess.bytesClientToUpstream, s.relayBytesClientToUpstream,
		s.relayBufferSize(), metrics.DirectionClientToUpstream, results, client, upstream, idleTimeout,
	)
	go relayCopy(
		client, upstream, &sess.bytesUpstreamToClient, s.relayBytesUpstreamToClient,
		s.relayBufferSize(), metrics.DirectionUpstreamToClient, results, client, upstream, idleTimeout,
	)

	first := <-results

	var (
		second       relayResult
		lingerClosed bool
	)

	switch {
	case first.err != nil:
		// A copy error invalidates both directions.
		_ = client.Close()
		_ = upstream.Close()

		second = <-results
	case first.direction == metrics.DirectionUpstreamToClient:
		// The upstream side is finished: no further data can arrive. CloseWrite already sent
		// FIN to the client; wait a bounded time for the client to close so the reverse copy
		// cannot pin a goroutine and descriptors indefinitely.
		second, lingerClosed = waitForRelayCompletion(results, client, relayLingerTimeout)
	default:
		second = <-results
	}

	if sess.reason == "" {
		if lingerClosed {
			// The upstream closed normally; the reverse copy failed only because we closed
			// the lingering client ourselves. This is not a relay error.
			sess.reason = pipeline.ReasonUpstreamClose
		} else {
			sess.reason = relayTerminationReason(s, first, second)
		}
	}

	if !s.forced.Load() && !lingerClosed && (first.err != nil || second.err != nil) {
		s.m.RelayErrors.Inc()
	}
}

// waitForRelayCompletion waits for the remaining relay copy, closing the client connection
// after linger so a client that never closes cannot hold the session open forever.
func waitForRelayCompletion(
	results <-chan relayResult, client net.Conn, linger time.Duration,
) (relayResult, bool) {
	timer := time.NewTimer(linger)
	defer timer.Stop()

	select {
	case result := <-results:
		return result, false
	case <-timer.C:
		_ = client.Close()

		return <-results, true
	}
}

func relayCopy(
	dst net.Conn,
	src io.Reader,
	counter *atomic.Int64,
	relayBytes prometheus.Counter,
	bufferSize int,
	direction string,
	results chan<- relayResult,
	client, upstream net.Conn,
	idleTimeout time.Duration,
) {
	touch := func() {
		if idleTimeout <= 0 {
			return
		}
		deadline := time.Now().Add(idleTimeout)
		_ = client.SetDeadline(deadline)
		_ = upstream.SetDeadline(deadline)
	}

	touch()

	buffer := make([]byte, bufferSize)

	_, err := io.CopyBuffer(
		countingWriter{dst: dst, counter: counter, relayBytes: relayBytes, touch: touch},
		readerOnly{reader: src, touch: touch},
		buffer,
	)

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
	dst        io.Writer
	counter    *atomic.Int64
	relayBytes prometheus.Counter
	touch      func()
}

func (w countingWriter) Write(p []byte) (int, error) {
	if w.touch != nil {
		w.touch()
	}

	n, err := w.dst.Write(p)
	if n > 0 {
		w.counter.Add(int64(n))
		w.relayBytes.Add(float64(n))
	}

	return n, err
}

type readerOnly struct {
	reader io.Reader
	touch  func()
}

func (r readerOnly) Read(p []byte) (int, error) {
	if r.touch != nil {
		r.touch()
	}

	return r.reader.Read(p)
}
