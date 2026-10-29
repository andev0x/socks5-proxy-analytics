package proxy

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andev0x/socks5-proxy-analytics/internal/metrics"
	"github.com/andev0x/socks5-proxy-analytics/internal/pipeline"
	"github.com/andev0x/socks5-proxy-analytics/internal/security"
)

// BenchmarkSOCKS5RelayEcho measures a persistent SOCKS5 session relaying 64 KiB
// request/response round trips to a local echo upstream. It includes relay copies and
// per-write byte accounting, but excludes one-time handshake and dial setup.
func BenchmarkSOCKS5RelayEcho(b *testing.B) {
	upstream := startTestUpstream(b, func(conn net.Conn) {
		_, _ = io.Copy(conn, conn)
	})

	sink := newTestSink()
	server, _ := startTestProxy(b, testProxyConfig(), sink)

	conn, code := openSOCKS(b, server.Addr().String(), "127.0.0.1", upstream.Port, nil)
	if code != replySucceeded {
		b.Fatalf("SOCKS reply = %d", code)
	}

	defer func() { _ = conn.Close() }()

	payload := bytes.Repeat([]byte("x"), 64*1024)
	response := make([]byte, len(payload))

	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if _, err := conn.Write(payload); err != nil {
			b.Fatalf("write payload: %v", err)
		}

		if _, err := io.ReadFull(conn, response); err != nil {
			b.Fatalf("read response: %v", err)
		}
	}
}

// BenchmarkSOCKS5RelayUpload measures one-way client-to-upstream streaming through the
// proxy. The upstream discards all bytes, so the result is dominated by the proxy relay
// path rather than by an echo round trip.
func BenchmarkSOCKS5RelayUpload(b *testing.B) {
	upstream := startTestUpstream(b, func(conn net.Conn) {
		_, _ = io.Copy(io.Discard, conn)
	})

	sink := newTestSink()
	server, _ := startTestProxy(b, testProxyConfig(), sink)

	conn, code := openSOCKS(b, server.Addr().String(), "127.0.0.1", upstream.Port, nil)
	if code != replySucceeded {
		b.Fatalf("SOCKS reply = %d", code)
	}

	defer func() { _ = conn.Close() }()

	payload := bytes.Repeat([]byte("x"), 1024*1024)

	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if _, err := conn.Write(payload); err != nil {
			b.Fatalf("write payload: %v", err)
		}
	}
}

// BenchmarkSOCKS5RelayDownload measures one-way upstream-to-client streaming through the
// proxy. The client discards all bytes.
func BenchmarkSOCKS5RelayDownload(b *testing.B) {
	payload := bytes.Repeat([]byte("x"), 1024*1024)

	upstream := startTestUpstream(b, func(conn net.Conn) {
		for {
			if _, err := conn.Write(payload); err != nil {
				return
			}
		}
	})

	sink := newTestSink()
	server, _ := startTestProxy(b, testProxyConfig(), sink)

	conn, code := openSOCKS(b, server.Addr().String(), "127.0.0.1", upstream.Port, nil)
	if code != replySucceeded {
		b.Fatalf("SOCKS reply = %d", code)
	}

	defer func() { _ = conn.Close() }()

	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()

	response := make([]byte, len(payload))

	for i := 0; i < b.N; i++ {
		if _, err := io.ReadFull(conn, response); err != nil {
			b.Fatalf("read payload: %v", err)
		}
	}
}

// BenchmarkSOCKS5Handshake measures a complete SOCKS5 negotiation (greeting, optional
// RFC 1929 authentication and CONNECT request parsing) against an in-memory connection.
// It isolates protocol handling from kernel socket cost; end-to-end connection setup is
// measured by the load harness in benchmarks/.
func BenchmarkSOCKS5Handshake(b *testing.B) {
	b.Run("no_auth", func(b *testing.B) {
		cfg := testProxyConfig()
		server := &Server{cfg: cfg}

		clientBytes := append([]byte{socksVersion, 1, methodNoAuth}, buildRequest("example.test", 443)...)

		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			conn := &memoryConn{reader: bytes.NewReader(clientBytes)}
			reader := bufio.NewReaderSize(conn, handshakeBufferSize)

			if _, err := server.negotiateAuth(conn, reader); err != nil {
				b.Fatalf("negotiateAuth: %v", err)
			}

			if _, err := readRequest(reader); err != nil {
				b.Fatalf("readRequest: %v", err)
			}
		}
	})

	b.Run("user_password", func(b *testing.B) {
		cfg := testProxyConfig()
		server := &Server{cfg: cfg, auth: security.NewAuthenticator("client", "secret")}

		authBytes := []byte{authVersion, 6, 'c', 'l', 'i', 'e', 'n', 't', 6, 's', 'e', 'c', 'r', 'e', 't'}
		clientBytes := append([]byte{socksVersion, 1, methodUserPassword}, authBytes...)
		clientBytes = append(clientBytes, buildRequest("example.test", 443)...)

		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			conn := &memoryConn{reader: bytes.NewReader(clientBytes)}
			reader := bufio.NewReaderSize(conn, handshakeBufferSize)

			if _, err := server.negotiateAuth(conn, reader); err != nil {
				b.Fatalf("negotiateAuth: %v", err)
			}

			if _, err := readRequest(reader); err != nil {
				b.Fatalf("readRequest: %v", err)
			}
		}
	})
}

// memoryConn is a net.Conn backed by memory. Writes are discarded.
type memoryConn struct {
	reader *bytes.Reader
}

func (c *memoryConn) Read(p []byte) (int, error)       { return c.reader.Read(p) }
func (c *memoryConn) Write(p []byte) (int, error)      { return len(p), nil }
func (c *memoryConn) Close() error                     { return nil }
func (c *memoryConn) LocalAddr() net.Addr              { return &net.TCPAddr{IP: net.IPv4zero} }
func (c *memoryConn) RemoteAddr() net.Addr             { return &net.TCPAddr{IP: net.IPv4zero} }
func (c *memoryConn) SetDeadline(time.Time) error      { return nil }
func (c *memoryConn) SetReadDeadline(time.Time) error  { return nil }
func (c *memoryConn) SetWriteDeadline(time.Time) error { return nil }

// BenchmarkRequestParsing isolates SOCKS5 request parsing from socket I/O.
func BenchmarkRequestParsing(b *testing.B) {
	request := buildRequest("example.test", 443)
	reader := bufio.NewReader(bytes.NewReader(request))

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		reader.Reset(bytes.NewReader(request))

		parsed, err := readRequest(reader)
		if err != nil {
			b.Fatalf("readRequest: %v", err)
		}

		if parsed.port != 443 {
			b.Fatal("unexpected port")
		}
	}
}

// BenchmarkEventConstruction isolates building the analytics event from a finished session.
func BenchmarkEventConstruction(b *testing.B) {
	sess := newSession(&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 4242}, time.Now())
	sess.requestedHost = "example.test"
	sess.destinationIP = "203.0.113.5"
	sess.destinationPort = 443
	sess.outcome = pipeline.OutcomeConnected
	sess.reason = pipeline.ReasonClientClose
	sess.bytesClientToUpstream.Store(1024)
	sess.bytesUpstreamToClient.Store(2048)
	sess.finish(time.Now())

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = sess.event()
	}
}

// BenchmarkRelayByteAccounting isolates the per-write accounting wrapper.
func BenchmarkRelayByteAccounting(b *testing.B) {
	m, err := metrics.NewMetrics()
	if err != nil {
		b.Fatalf("metrics: %v", err)
	}

	var counter atomic.Int64

	writer := countingWriter{
		dst:        io.Discard,
		counter:    &counter,
		relayBytes: m.RelayBytes.WithLabelValues(metrics.DirectionClientToUpstream),
	}

	payload := make([]byte, 32*1024)

	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if _, err := writer.Write(payload); err != nil {
			b.Fatalf("write: %v", err)
		}
	}
}
