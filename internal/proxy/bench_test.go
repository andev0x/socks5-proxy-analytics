package proxy

import (
	"bytes"
	"io"
	"net"
	"testing"
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
