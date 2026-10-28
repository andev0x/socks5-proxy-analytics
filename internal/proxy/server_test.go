package proxy

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/andev0x/socks5-proxy-analytics/internal/config"
	"github.com/andev0x/socks5-proxy-analytics/internal/metrics"
	"github.com/andev0x/socks5-proxy-analytics/internal/pipeline"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.uber.org/zap"
)

func TestCONNECTPreservesAttributionAndByteDirections(t *testing.T) {
	upstream := startTestUpstream(t, func(conn net.Conn) {
		payload := make([]byte, 13)
		if _, err := io.ReadFull(conn, payload); err != nil {
			return
		}

		_, _ = conn.Write([]byte("response!"))
		_, _ = io.Copy(io.Discard, conn)
	})

	sink := newTestSink()
	server, m := startTestProxy(t, testProxyConfig(), sink)

	conn, code := openSOCKS(t, server.Addr().String(), "localhost", upstream.Port, nil)
	if code != replySucceeded {
		t.Fatalf("SOCKS reply = %d, want success", code)
	}

	if _, err := conn.Write([]byte("hello, world!")); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	response := make([]byte, len("response!"))
	if _, err := io.ReadFull(conn, response); err != nil {
		t.Fatalf("read response: %v", err)
	}

	if string(response) != "response!" {
		t.Fatalf("response = %q", response)
	}

	_ = conn.Close()
	event := sink.wait(t)

	if event.ClientIP != "127.0.0.1" {
		t.Errorf("ClientIP = %q, want 127.0.0.1 (SOCKS client)", event.ClientIP)
	}

	if event.ClientPort <= 0 {
		t.Errorf("ClientPort = %d, want > 0", event.ClientPort)
	}

	if event.RequestedHost != "localhost" {
		t.Errorf("RequestedHost = %q, want original name localhost", event.RequestedHost)
	}

	if event.DestinationIP != "127.0.0.1" {
		t.Errorf("DestinationIP = %q, want resolved 127.0.0.1", event.DestinationIP)
	}

	if event.DestinationPort != upstream.Port {
		t.Errorf("DestinationPort = %d, want %d", event.DestinationPort, upstream.Port)
	}

	if event.BytesClientToUpstream != int64(len("hello, world!")) {
		t.Errorf("bytes client->upstream = %d, want %d", event.BytesClientToUpstream, len("hello, world!"))
	}

	if event.BytesUpstreamToClient != int64(len("response!")) {
		t.Errorf("bytes upstream->client = %d, want %d", event.BytesUpstreamToClient, len("response!"))
	}

	if event.Outcome != pipeline.OutcomeConnected {
		t.Errorf("Outcome = %q", event.Outcome)
	}

	if event.TerminationReason != pipeline.ReasonClientClose && event.TerminationReason != pipeline.ReasonUpstreamClose {
		t.Errorf("TerminationReason = %q", event.TerminationReason)
	}

	if event.EventID == "" || event.DurationMs < 0 || event.StartedAt.IsZero() {
		t.Errorf("incomplete event metadata: %+v", event)
	}

	if got := testutil.ToFloat64(m.AcceptedConnections); got != 1 {
		t.Errorf("accepted metric = %v, want 1", got)
	}

	if got := testutil.ToFloat64(m.ActiveConnections); got != 0 {
		t.Errorf("active gauge = %v, want 0", got)
	}
}

func TestCONNECTIPLiteralHasEmptyRequestedHost(t *testing.T) {
	upstream := startTestUpstream(t, func(conn net.Conn) {
		_, _ = io.Copy(conn, conn)
	})

	sink := newTestSink()
	server, _ := startTestProxy(t, testProxyConfig(), sink)

	conn, code := openSOCKS(t, server.Addr().String(), "127.0.0.1", upstream.Port, nil)
	if code != replySucceeded {
		t.Fatalf("SOCKS reply = %d, want success", code)
	}

	_ = conn.Close()
	event := sink.wait(t)

	if event.RequestedHost != "" {
		t.Errorf("RequestedHost = %q, want empty for an IP literal", event.RequestedHost)
	}

	if event.DestinationIP != "127.0.0.1" {
		t.Errorf("DestinationIP = %q, want 127.0.0.1", event.DestinationIP)
	}
}

func TestDialFailureEmitsEventAndReply(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen probe: %v", err)
	}

	probeAddr, ok := probe.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatal("probe listener is not TCP")
	}

	closedPort := probeAddr.Port
	_ = probe.Close()

	sink := newTestSink()
	server, m := startTestProxy(t, testProxyConfig(), sink)

	conn, code := openSOCKS(t, server.Addr().String(), "localhost", closedPort, nil)
	defer func() { _ = conn.Close() }()

	if code != replyConnectionRefused && code != replyGeneralFailure {
		t.Fatalf("SOCKS reply = %d, want refusal or general failure", code)
	}

	event := sink.wait(t)
	if event.Outcome != pipeline.OutcomeDialFailed {
		t.Errorf("Outcome = %q, want dial_failed", event.Outcome)
	}

	if event.TerminationReason != pipeline.ReasonDialError {
		t.Errorf("TerminationReason = %q, want dial_error", event.TerminationReason)
	}

	if event.RequestedHost != "localhost" {
		t.Errorf("RequestedHost = %q, want localhost preserved", event.RequestedHost)
	}

	if event.DestinationIP != "" {
		t.Errorf("DestinationIP = %q, want empty when no socket was established", event.DestinationIP)
	}

	if got := testutil.ToFloat64(m.DialErrors.WithLabelValues("refused")); got == 0 {
		t.Error("expected refused dial error metric")
	}
}

func TestAuthenticationIsEnforced(t *testing.T) {
	upstream := startTestUpstream(t, func(conn net.Conn) {
		_, _ = io.Copy(conn, conn)
	})

	cfg := testProxyConfig()
	cfg.Proxy.Auth.Enabled = true
	cfg.Proxy.Auth.Username = "client"
	cfg.Proxy.Auth.Password = "secret"

	sink := newTestSink()
	server, m := startTestProxy(t, cfg, sink)

	conn, code := openSOCKS(t, server.Addr().String(), "127.0.0.1", upstream.Port, &proxyCredentials{
		username: "client",
		password: "wrong",
	})
	_ = conn.Close()

	if code != 0x01 {
		t.Fatalf("auth reply = %d, want failure 0x01", code)
	}

	event := sink.wait(t)
	if event.Outcome != pipeline.OutcomeAuthFailed {
		t.Errorf("Outcome = %q, want auth_failed", event.Outcome)
	}

	if got := testutil.ToFloat64(m.AuthFailures); got != 1 {
		t.Errorf("auth failure metric = %v, want 1", got)
	}

	conn, code = openSOCKS(t, server.Addr().String(), "127.0.0.1", upstream.Port, &proxyCredentials{
		username: "client",
		password: "secret",
	})
	if code != replySucceeded {
		t.Fatalf("authenticated SOCKS reply = %d, want success", code)
	}

	_ = conn.Close()
	sink.wait(t)
}

func TestAuthenticationRejectsClientWithoutUserPassMethod(t *testing.T) {
	cfg := testProxyConfig()
	cfg.Proxy.Auth.Enabled = true
	cfg.Proxy.Auth.Username = "client"
	cfg.Proxy.Auth.Password = "secret"

	sink := newTestSink()
	server, m := startTestProxy(t, cfg, sink)

	conn, err := net.DialTimeout("tcp", server.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
		t.Fatalf("write greeting: %v", err)
	}

	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("read greeting reply: %v", err)
	}

	if reply[1] != 0xFF {
		t.Fatalf("method reply = %d, want 0xFF", reply[1])
	}

	event := sink.wait(t)
	if event.Outcome != pipeline.OutcomeHandshakeFailed || event.TerminationReason != pipeline.ReasonNoAcceptableAuth {
		t.Errorf("event = %+v, want handshake_failed/no_acceptable_auth", event)
	}

	if got := testutil.ToFloat64(m.HandshakeFailures.WithLabelValues(metrics.HandshakeNoAcceptableAuth)); got != 1 {
		t.Errorf("handshake failure metric = %v, want 1", got)
	}
}

func TestIPWhitelistRejectsBeforeHandshake(t *testing.T) {
	cfg := testProxyConfig()
	cfg.Proxy.IPWhitelist = []string{"192.0.2.1"}

	sink := newTestSink()
	server, m := startTestProxy(t, cfg, sink)

	conn, err := net.DialTimeout("tcp", server.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}

	_, writeErr := conn.Write([]byte{5, 1, 0})
	if writeErr == nil {
		buffer := make([]byte, 1)
		_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		_, _ = conn.Read(buffer)
	}

	_ = conn.Close()

	if got := testutil.ToFloat64(m.RejectedConnections.WithLabelValues(metrics.RejectNotAllowed)); got != 1 {
		t.Errorf("whitelist rejection metric = %v, want 1", got)
	}

	sink.expectNone(t, 100*time.Millisecond)
}

func TestMaxConnectionsEnforced(t *testing.T) {
	upstream := startTestUpstream(t, func(conn net.Conn) {
		_, _ = io.Copy(conn, conn)
	})

	cfg := testProxyConfig()
	cfg.Proxy.MaxConnections = 1

	sink := newTestSink()
	server, m := startTestProxy(t, cfg, sink)

	first, code := openSOCKS(t, server.Addr().String(), "127.0.0.1", upstream.Port, nil)
	if code != replySucceeded {
		t.Fatalf("first SOCKS reply = %d", code)
	}

	second, err := net.DialTimeout("tcp", server.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("dial second: %v", err)
	}

	_ = second.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, readErr := second.Read(make([]byte, 1))
	if readErr == nil {
		t.Fatal("expected second connection to be closed by the connection limit")
	}

	_ = second.Close()

	if got := testutil.ToFloat64(m.RejectedConnections.WithLabelValues(metrics.RejectMaxConnections)); got != 1 {
		t.Errorf("max connection rejection metric = %v, want 1", got)
	}

	_ = first.Close()
	sink.wait(t)
}

func TestRateLimitEnforcedPerClient(t *testing.T) {
	upstream := startTestUpstream(t, func(conn net.Conn) {
		_, _ = io.Copy(conn, conn)
	})

	cfg := testProxyConfig()
	cfg.RateLimit.Enabled = true
	cfg.RateLimit.RequestsPerSecond = 1

	sink := newTestSink()
	server, m := startTestProxy(t, cfg, sink)

	first, code := openSOCKS(t, server.Addr().String(), "127.0.0.1", upstream.Port, nil)
	if code != replySucceeded {
		t.Fatalf("first SOCKS reply = %d", code)
	}

	second, err := net.DialTimeout("tcp", server.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("dial second: %v", err)
	}

	_ = second.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, readErr := second.Read(make([]byte, 1))
	if readErr == nil {
		t.Fatal("expected second connection to be rate limited")
	}

	_ = second.Close()

	if got := testutil.ToFloat64(m.RejectedConnections.WithLabelValues(metrics.RejectRateLimited)); got != 1 {
		t.Errorf("rate limit rejection metric = %v, want 1", got)
	}

	_ = first.Close()
	sink.wait(t)
}

func TestUnsupportedCommandAndAddressType(t *testing.T) {
	sink := newTestSink()
	server, _ := startTestProxy(t, testProxyConfig(), sink)

	conn := greetNoAuth(t, server.Addr().String())
	request := []byte{5, 0x02, 0, addressIPv4, 127, 0, 0, 1, 0x00, 0x50}
	if _, err := conn.Write(request); err != nil {
		t.Fatalf("write BIND request: %v", err)
	}

	reply := readReply(t, conn)
	if reply != replyCommandNotSupported {
		t.Errorf("BIND reply = %d, want %d", reply, replyCommandNotSupported)
	}

	_ = conn.Close()
	event := sink.wait(t)
	if event.TerminationReason != pipeline.ReasonUnsupportedCommand {
		t.Errorf("reason = %q, want unsupported_command", event.TerminationReason)
	}

	conn = greetNoAuth(t, server.Addr().String())
	request = []byte{5, 0x01, 0, 0x05, 0x00, 0x50}
	if _, err := conn.Write(request); err != nil {
		t.Fatalf("write bad ATYP request: %v", err)
	}

	reply = readReply(t, conn)
	if reply != replyAddressNotSupported {
		t.Errorf("ATYP reply = %d, want %d", reply, replyAddressNotSupported)
	}

	_ = conn.Close()
	event = sink.wait(t)
	if event.TerminationReason != pipeline.ReasonUnsupportedAddressType {
		t.Errorf("reason = %q, want unsupported_address_type", event.TerminationReason)
	}
}

func TestShutdownClosesActiveSessionsAndEmitsEvent(t *testing.T) {
	upstream := startTestUpstream(t, func(conn net.Conn) {
		_, _ = io.Copy(conn, conn)
	})

	sink := newTestSink()
	server, _ := startTestProxy(t, testProxyConfig(), sink)

	conn, code := openSOCKS(t, server.Addr().String(), "127.0.0.1", upstream.Port, nil)
	if code != replySucceeded {
		t.Fatalf("SOCKS reply = %d", code)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if err := server.Shutdown(ctx); err == nil {
		t.Fatal("expected shutdown grace-period error for an active session")
	}

	event := sink.wait(t)
	if event.Outcome != pipeline.OutcomeConnected {
		t.Errorf("Outcome = %q", event.Outcome)
	}

	if event.TerminationReason != pipeline.ReasonServerShutdown {
		t.Errorf("TerminationReason = %q, want server_shutdown", event.TerminationReason)
	}

	_ = conn.Close()

	if _, err := net.DialTimeout("tcp", server.Addr().String(), 100*time.Millisecond); err == nil {
		t.Fatal("listener must be closed after shutdown")
	}
}

func TestShutdownWhenIdleIsClean(t *testing.T) {
	server, _ := startTestProxy(t, testProxyConfig(), newTestSink())

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

type proxyCredentials struct {
	username string
	password string
}

type testSink struct {
	events chan pipeline.ConnectionEvent
}

func newTestSink() *testSink {
	return &testSink{events: make(chan pipeline.ConnectionEvent, 100)}
}

//nolint:gocritic // satisfies proxy.EventSink value semantics used by the production path
func (s *testSink) Collect(event pipeline.ConnectionEvent) bool {
	select {
	case s.events <- event:
		return true
	default:
		return false
	}
}

func (s *testSink) wait(tb testing.TB) pipeline.ConnectionEvent {
	tb.Helper()

	select {
	case event := <-s.events:
		return event
	case <-time.After(3 * time.Second):
		tb.Fatal("timed out waiting for connection event")

		return pipeline.ConnectionEvent{}
	}
}

func (s *testSink) expectNone(tb testing.TB, duration time.Duration) {
	tb.Helper()

	select {
	case event := <-s.events:
		tb.Fatalf("unexpected event: %+v", event)
	case <-time.After(duration):
	}
}

type testUpstream struct {
	listener net.Listener
	Port     int
}

func listenerPort(tb testing.TB, listener net.Listener) int {
	tb.Helper()

	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		tb.Fatalf("listener address is not TCP: %T", listener.Addr())
	}

	return address.Port
}

func startTestUpstream(tb testing.TB, handler func(net.Conn)) *testUpstream {
	tb.Helper()
	t := tb

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen upstream: %v", err)
	}

	upstream := &testUpstream{
		listener: listener,
		Port:     listenerPort(t, listener),
	}

	var wg sync.WaitGroup
	done := make(chan struct{})

	go func() {
		defer close(done)

		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}

			wg.Add(1)

			go func() {
				defer wg.Done()
				defer func() { _ = conn.Close() }()

				handler(conn)
			}()
		}
	}()

	t.Cleanup(func() {
		_ = listener.Close()
		<-done
		wg.Wait()
	})

	return upstream
}

func startTestProxy(
	tb testing.TB, cfg *config.Config, sink EventSink,
) (*Server, *metrics.Metrics) {
	tb.Helper()
	t := tb

	m, err := metrics.NewMetrics()
	if err != nil {
		t.Fatalf("NewMetrics() error = %v", err)
	}

	server, err := NewServer(cfg, zap.NewNop(), sink, m)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	if err := server.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		_ = server.Shutdown(ctx)
	})

	return server, m
}

func testProxyConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Proxy.Address = "127.0.0.1"
	cfg.Proxy.Port = 0
	cfg.Proxy.MaxConnections = 100
	cfg.Proxy.DialTimeoutMs = 2000
	cfg.Proxy.HandshakeTimeoutMs = 2000
	cfg.Proxy.ShutdownGraceMs = 1000
	cfg.RateLimit.MaxTrackedClients = 100
	cfg.Metrics.Enabled = false

	return cfg
}

func openSOCKS(
	tb testing.TB, proxyAddress, host string, port int, credentials *proxyCredentials,
) (conn net.Conn, code byte) {
	tb.Helper()
	t := tb

	conn, err := net.DialTimeout("tcp", proxyAddress, 2*time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}

	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))

	methods := []byte{methodNoAuth}
	if credentials != nil {
		methods = []byte{methodUserPassword}
	}

	greeting := append([]byte{socksVersion, byte(len(methods))}, methods...)
	if _, err := conn.Write(greeting); err != nil {
		t.Fatalf("write greeting: %v", err)
	}

	methodReply := make([]byte, 2)
	if _, err := io.ReadFull(conn, methodReply); err != nil {
		t.Fatalf("read method reply: %v", err)
	}

	if methodReply[1] == methodNoAcceptable {
		return conn, methodNoAcceptable
	}

	if methodReply[1] == methodUserPassword {
		if authCode := authenticateClient(t, conn, credentials); authCode != 0 {
			return conn, authCode
		}
	}

	if _, err := conn.Write(buildRequest(host, port)); err != nil {
		t.Fatalf("write request: %v", err)
	}

	return conn, readReply(t, conn)
}

func authenticateClient(tb testing.TB, conn net.Conn, credentials *proxyCredentials) byte {
	tb.Helper()
	t := tb

	if credentials == nil {
		t.Fatal("proxy requested credentials but none were provided")
	}

	authRequest := make(
		[]byte,
		0,
		2+len(credentials.username)+1+len(credentials.password),
	)
	authRequest = append(authRequest, authVersion, byte(len(credentials.username)))
	authRequest = append(authRequest, []byte(credentials.username)...)
	authRequest = append(authRequest, byte(len(credentials.password)))
	authRequest = append(authRequest, []byte(credentials.password)...)

	if _, err := conn.Write(authRequest); err != nil {
		t.Fatalf("write auth: %v", err)
	}

	authReply := make([]byte, 2)
	if _, err := io.ReadFull(conn, authReply); err != nil {
		t.Fatalf("read auth reply: %v", err)
	}

	return authReply[1]
}

func buildRequest(host string, port int) []byte {
	requestBytes := []byte{socksVersion, commandConnect, 0x00}

	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			requestBytes = append(requestBytes, addressIPv4)
			requestBytes = append(requestBytes, ip4...)
		} else {
			requestBytes = append(requestBytes, addressIPv6)
			requestBytes = append(requestBytes, ip.To16()...)
		}
	} else {
		requestBytes = append(requestBytes, addressDomain, byte(len(host)))
		requestBytes = append(requestBytes, []byte(host)...)
	}

	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, uint16(port))

	return append(requestBytes, portBytes...)
}

func readReply(tb testing.TB, conn net.Conn) byte {
	tb.Helper()
	t := tb

	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		t.Fatalf("read reply header: %v", err)
	}

	var addressLength int

	switch header[3] {
	case addressIPv4:
		addressLength = net.IPv4len
	case addressIPv6:
		addressLength = net.IPv6len
	case addressDomain:
		length := make([]byte, 1)
		if _, err := io.ReadFull(conn, length); err != nil {
			t.Fatalf("read reply domain length: %v", err)
		}

		addressLength = int(length[0])
	default:
		t.Fatalf("unexpected reply address type: %d", header[3])
	}

	discard := make([]byte, addressLength+2)
	if _, err := io.ReadFull(conn, discard); err != nil {
		t.Fatalf("read reply address: %v", err)
	}

	return header[1]
}

func greetNoAuth(tb testing.TB, proxyAddress string) net.Conn {
	tb.Helper()
	t := tb

	conn, err := net.DialTimeout("tcp", proxyAddress, 2*time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}

	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))

	if _, err := conn.Write([]byte{socksVersion, 1, methodNoAuth}); err != nil {
		t.Fatalf("write greeting: %v", err)
	}

	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("read greeting reply: %v", err)
	}

	if reply[1] != methodNoAuth {
		t.Fatalf("method = %d, want no auth", reply[1])
	}

	return conn
}

func TestRequestAddress(t *testing.T) {
	tests := []struct {
		host     string
		port     int
		expected string
	}{
		{"127.0.0.1", 80, "127.0.0.1:80"},
		{"::1", 443, "[::1]:443"},
		{"example.test", 8080, "example.test:8080"},
	}

	for _, test := range tests {
		if got := requestAddress(test.host, test.port); got != test.expected {
			t.Errorf("requestAddress(%q, %d) = %q, want %q", test.host, test.port, got, test.expected)
		}
	}
}

func TestClassifyDialError(t *testing.T) {
	if got := classifyDialError(&net.DNSError{Err: "no such host", Name: "example.test"}); got != "dns" {
		t.Errorf("DNS classification = %q", got)
	}

	if got := classifyDialError(errors.New("connect: connection refused")); got != "refused" {
		t.Errorf("refused classification = %q", got)
	}

	if got := classifyDialError(context.DeadlineExceeded); got != "timeout" {
		t.Errorf("timeout classification = %q", got)
	}
}

func TestPortFromString(t *testing.T) {
	if got := portFromString("8080"); got != 8080 {
		t.Errorf("portFromString = %d", got)
	}

	if got := portFromString("x"); got != 0 {
		t.Errorf("portFromString invalid = %d", got)
	}
}
