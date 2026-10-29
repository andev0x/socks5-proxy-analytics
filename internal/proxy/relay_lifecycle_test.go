package proxy

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/andev0x/socks5-proxy-analytics/internal/metrics"
	"github.com/andev0x/socks5-proxy-analytics/internal/pipeline"
	"go.uber.org/zap"
)

// TestUpstreamCloseFinalizesSessionWithoutClientClose verifies that a session is finalized
// as soon as the upstream half of the relay ends, even if the client keeps its socket open.
// Without this, a client that never closes leaks a goroutine and two file descriptors for
// the lifetime of the process when idle_timeout_ms is 0 (the default).
func TestUpstreamCloseFinalizesSessionWithoutClientClose(t *testing.T) {
	upstream := startTestUpstream(t, func(conn net.Conn) {
		if _, err := io.ReadFull(conn, make([]byte, 1)); err != nil {
			return
		}

		_, _ = conn.Write([]byte("bye"))
		// Close the upstream side immediately after writing the response.
	})

	sink := newTestSink()
	server, _ := startTestProxy(t, testProxyConfig(), sink)

	conn, code := openSOCKS(t, server.Addr().String(), "127.0.0.1", upstream.Port, nil)
	if code != replySucceeded {
		t.Fatalf("SOCKS reply = %d, want success", code)
	}

	defer func() { _ = conn.Close() }()

	if _, err := conn.Write([]byte("x")); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	response := make([]byte, 3)
	if _, err := io.ReadFull(conn, response); err != nil {
		t.Fatalf("read response: %v", err)
	}

	// Deliberately keep the client socket open. The proxy must still finish the session
	// within the bounded relay linger window.
	event := sink.waitFor(t, relayLingerTimeout+3*time.Second)

	if event.Outcome != pipeline.OutcomeConnected {
		t.Errorf("Outcome = %q, want connected", event.Outcome)
	}

	if event.BytesClientToUpstream != 1 {
		t.Errorf("bytes client->upstream = %d, want 1", event.BytesClientToUpstream)
	}

	if event.BytesUpstreamToClient != 3 {
		t.Errorf("bytes upstream->client = %d, want 3", event.BytesUpstreamToClient)
	}

	if event.TerminationReason != pipeline.ReasonUpstreamClose {
		t.Errorf("TerminationReason = %q, want upstream_close (not a relay error)",
			event.TerminationReason)
	}
}

// TestClientHalfCloseStillDeliversUpstreamResponse verifies the other half-close direction:
// a client that shuts down its write side must still receive the upstream response.
func TestClientHalfCloseStillDeliversUpstreamResponse(t *testing.T) {
	upstream := startTestUpstream(t, func(conn net.Conn) {
		body, err := io.ReadAll(conn)
		if err != nil {
			return
		}

		_, _ = conn.Write(append([]byte("echo:"), body...))
	})

	sink := newTestSink()
	server, _ := startTestProxy(t, testProxyConfig(), sink)

	conn, code := openSOCKS(t, server.Addr().String(), "127.0.0.1", upstream.Port, nil)
	if code != replySucceeded {
		t.Fatalf("SOCKS reply = %d, want success", code)
	}

	defer func() { _ = conn.Close() }()

	if _, err := conn.Write([]byte("hello")); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		t.Fatal("expected *net.TCPConn")
	}

	if err := tcpConn.CloseWrite(); err != nil {
		t.Fatalf("close write: %v", err)
	}

	response, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	if string(response) != "echo:hello" {
		t.Errorf("response = %q, want %q", response, "echo:hello")
	}

	event := sink.wait(t)
	if event.BytesClientToUpstream != 5 {
		t.Errorf("bytes client->upstream = %d, want 5", event.BytesClientToUpstream)
	}

	if event.BytesUpstreamToClient != int64(len("echo:hello")) {
		t.Errorf("bytes upstream->client = %d", event.BytesUpstreamToClient)
	}
}

// TestPolicyEnabledWithoutEngineIsRejected ensures a configuration that requests policy
// enforcement can never silently construct a server with no policy engine (open egress).
func TestPolicyEnabledWithoutEngineIsRejected(t *testing.T) {
	cfg := testProxyConfig()
	cfg.Policy.Enabled = true

	m, err := metrics.NewMetrics()
	if err != nil {
		t.Fatalf("NewMetrics() error = %v", err)
	}

	if _, err := NewServer(cfg, zap.NewNop(), newTestSink(), m, nil); err == nil {
		t.Fatal("NewServer must reject policy.enabled without a policy engine")
	}
}

func TestIdleTimeoutClosesSilentSession(t *testing.T) {
	upstream := startTestUpstream(t, func(conn net.Conn) {
		_, _ = io.Copy(io.Discard, conn)
	})

	cfg := testProxyConfig()
	cfg.Proxy.IdleTimeoutMs = 100

	sink := newTestSink()
	server, _ := startTestProxy(t, cfg, sink)

	conn, code := openSOCKS(t, server.Addr().String(), "127.0.0.1", upstream.Port, nil)
	if code != replySucceeded {
		t.Fatalf("SOCKS reply = %d, want success", code)
	}

	defer func() { _ = conn.Close() }()

	event := sink.wait(t)

	if event.TerminationReason != pipeline.ReasonRelayError && event.TerminationReason != pipeline.ReasonTimeout {
		t.Errorf("TerminationReason = %q, want relay_error or timeout", event.TerminationReason)
	}
}

func TestShutdownWithActiveSessionReturnsPromptly(t *testing.T) {
	upstream := startTestUpstream(t, func(conn net.Conn) {
		_, _ = io.Copy(io.Discard, conn)
	})

	sink := newTestSink()
	server, _ := startTestProxy(t, testProxyConfig(), sink)

	conn, code := openSOCKS(t, server.Addr().String(), "127.0.0.1", upstream.Port, nil)
	if code != replySucceeded {
		t.Fatalf("SOCKS reply = %d, want success", code)
	}

	defer func() { _ = conn.Close() }()

	start := time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_ = server.Shutdown(ctx)

	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("shutdown took %s, want a bounded duration", elapsed)
	}

	_ = sink.wait(t)
}
