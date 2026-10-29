// Package proxy implements the Phase 1 SOCKS5 CONNECT proxy, its security controls,
// relay accounting, connection limits and lifecycle.
package proxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/andev0x/socks5-proxy-analytics/internal/config"
	"github.com/andev0x/socks5-proxy-analytics/internal/metrics"
	"github.com/andev0x/socks5-proxy-analytics/internal/pipeline"
	"github.com/andev0x/socks5-proxy-analytics/internal/policy"
	"github.com/andev0x/socks5-proxy-analytics/internal/security"
	"go.uber.org/zap"
)

// EventSink accepts best-effort connection events without blocking the relay.
type EventSink interface {
	Collect(event pipeline.ConnectionEvent) bool
}

const (
	dialReasonDNS         = "dns"
	dialReasonTimeout     = "timeout"
	dialReasonCanceled    = "canceled"
	dialReasonRefused     = "refused"
	dialReasonUnreachable = "network_unreachable"
	dialReasonOther       = "other"
)

// Server owns the SOCKS5 listener, admitted sessions and their cleanup.
type Server struct {
	cfg          *config.Config
	log          *zap.Logger
	sink         EventSink
	m            *metrics.Metrics
	auth         *security.Authenticator
	whitelist    *security.IPWhitelist
	limiter      *security.RateLimiter
	policyEngine *policy.Engine

	ctx        context.Context
	cancel     context.CancelFunc
	listener   net.Listener
	mu         sync.Mutex
	conns      map[net.Conn]struct{}
	wg         sync.WaitGroup
	active     atomic.Int64
	started    atomic.Bool
	shutting   atomic.Bool
	forced     atomic.Bool
	acceptDone chan struct{}
}

// NewServer creates a SOCKS5 server with validated security controls.
func NewServer(
	cfg *config.Config, log *zap.Logger, sink EventSink, m *metrics.Metrics, policyEngine *policy.Engine,
) (*Server, error) {
	if sink == nil || m == nil {
		return nil, errors.New("proxy requires an event sink and metrics registry")
	}

	whitelist, err := security.NewIPWhitelist(cfg.Proxy.IPWhitelist)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy IP whitelist: %w", err)
	}

	limiter := security.NewRateLimiter(cfg.RateLimit.RequestsPerSecond, cfg.RateLimit.Enabled)
	limiter.SetMaxClients(cfg.RateLimit.MaxTrackedClients)

	ctx, cancel := context.WithCancel(context.Background())

	server := &Server{
		ctx:          ctx,
		cancel:       cancel,
		cfg:          cfg,
		log:          log,
		sink:         sink,
		m:            m,
		whitelist:    whitelist,
		limiter:      limiter,
		policyEngine: policyEngine,
		conns:        make(map[net.Conn]struct{}),
		acceptDone:   make(chan struct{}),
	}

	if cfg.Proxy.Auth.Enabled {
		if len(cfg.Proxy.Workloads) > 0 {
			server.auth = security.NewAuthenticatorFromWorkloads(policy.WorkloadCredentialsFromConfig(cfg))
		} else {
			server.auth = security.NewAuthenticator(cfg.Proxy.Auth.Username, cfg.Proxy.Auth.Password)
		}
	}

	return server, nil
}

// Start binds the listener and starts accepting connections.
func (s *Server) Start() error {
	if s.started.Swap(true) {
		return errors.New("proxy server is already started")
	}

	addr := net.JoinHostPort(s.cfg.Proxy.Address, fmt.Sprintf("%d", s.cfg.Proxy.Port))

	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", addr)
	if err != nil {
		s.started.Store(false)

		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	s.listener = listener

	s.log.Info("SOCKS5 listener started",
		zap.String("address", listener.Addr().String()),
		zap.Bool("auth_enabled", s.cfg.Proxy.Auth.Enabled),
		zap.Int("max_connections", s.cfg.Proxy.MaxConnections),
		zap.Bool("rate_limit_enabled", s.cfg.RateLimit.Enabled))

	if s.auth == nil && !isLoopbackBind(s.cfg.Proxy.Address) {
		s.log.Warn("SOCKS5 proxy is bound to a non-loopback address without authentication; " +
			"any reachable client can use it as an open proxy")
	}

	go s.acceptLoop()

	return nil
}

// Addr returns the bound listener address, or nil before Start.
func (s *Server) Addr() net.Addr {
	if s.listener == nil {
		return nil
	}

	return s.listener.Addr()
}

func (s *Server) acceptLoop() {
	defer close(s.acceptDone)

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if s.shutting.Load() || errors.Is(err, net.ErrClosed) {
				return
			}

			s.log.Error("failed to accept SOCKS5 connection", zap.Error(err))
			time.Sleep(10 * time.Millisecond)

			continue
		}

		acceptedAt := time.Now().UTC()
		if !s.admit(conn) {
			_ = conn.Close()

			continue
		}

		s.wg.Add(1)

		go func() {
			defer s.wg.Done()

			s.handle(conn, acceptedAt)
		}()
	}
}

// admit applies cheap pre-handshake controls. Rejections are counted, never silently ignored.
func (s *Server) admit(conn net.Conn) bool {
	if s.shutting.Load() {
		s.m.RejectedConnections.WithLabelValues(metrics.RejectShuttingDown).Inc()

		return false
	}

	clientIP := security.ClientIP(conn.RemoteAddr())

	if !s.whitelist.IsAllowed(clientIP) {
		s.m.RejectedConnections.WithLabelValues(metrics.RejectNotAllowed).Inc()
		s.log.Warn("SOCKS5 client rejected by IP whitelist", zap.String("client_ip", clientIP))

		return false
	}

	if !s.limiter.Allow(clientIP) {
		s.m.RejectedConnections.WithLabelValues(metrics.RejectRateLimited).Inc()
		s.log.Warn("SOCKS5 client rejected by per-IP rate limit", zap.String("client_ip", clientIP))

		return false
	}

	active := s.active.Add(1)
	if active > int64(s.cfg.Proxy.MaxConnections) {
		s.active.Add(-1)
		s.m.RejectedConnections.WithLabelValues(metrics.RejectMaxConnections).Inc()
		s.log.Warn("SOCKS5 client rejected by connection limit",
			zap.Int64("active", active-1),
			zap.Int("max", s.cfg.Proxy.MaxConnections))

		return false
	}

	s.m.ActiveConnections.Set(float64(active))
	s.m.AcceptedConnections.Inc()
	s.track(conn)

	return true
}

func (s *Server) handle(client net.Conn, startedAt time.Time) {
	sess := newSession(client.RemoteAddr(), startedAt)

	defer s.finish(client, sess)

	deadline := time.Now().Add(time.Duration(s.cfg.Proxy.HandshakeTimeoutMs) * time.Millisecond)
	_ = client.SetDeadline(deadline)

	req, clientReader, err := s.handshake(client)
	if err != nil {
		s.recordHandshakeFailure(sess, err)

		return
	}

	sess.workloadID = req.workloadID
	sess.requestedHost = req.requestedHost
	sess.destinationPort = req.port

	_ = client.SetDeadline(time.Time{})

	// Policy enforcement before dialing.
	if s.isPolicyEnabled() {
		decisionAllowed, reason, resolvedHost, policyDecision := s.evaluatePolicy(req)
		recordPolicyDecision(sess, &policyDecision, s.m)
		if !decisionAllowed {
			denyPolicyRequest(sess, client, reason, s.log)

			return
		}

		req.dialHost = resolvedHost
	}

	upstream, dialLatency, err := s.dialUpstream(req)
	sess.dialLatencyMs = dialLatency

	if err != nil {
		sess.outcome = pipeline.OutcomeDialFailed
		sess.reason = pipeline.ReasonDialError
		_ = writeReply(client, replyCodeForDialError(err), nil)
		s.log.Warn("SOCKS5 upstream dial failed",
			zap.String("client_ip", sess.clientIP),
			zap.String("destination", requestAddress(req.dialHost, req.port)),
			zap.Error(err))

		return
	}

	s.track(upstream)

	defer func() {
		s.untrack(upstream)
		_ = upstream.Close()
	}()

	sess.outcome = pipeline.OutcomeConnected
	sess.destinationIP = remoteIP(upstream.RemoteAddr())

	if err := writeReply(client, replySucceeded, upstream.LocalAddr()); err != nil {
		sess.reason = pipeline.ReasonRelayError
		s.log.Warn("failed to send SOCKS5 success reply", zap.String("client_ip", sess.clientIP), zap.Error(err))

		return
	}

	sess.clientReader = clientReader
	s.relay(client, upstream, sess, time.Duration(s.cfg.Proxy.IdleTimeoutMs)*time.Millisecond)
}

// evaluatePolicy normalizes the destination, resolves DNS once for domains, and evaluates
// policy. It returns whether the request is allowed, a reason, the host/IP to dial, and
// the policy decision metadata.
func (s *Server) evaluatePolicy(req *request) (allowed bool, reason, dialHost string, decision policy.Decision) {
	start := time.Now()

	host := req.requestedHost
	ipLiteral := req.dialHost
	if host != "" {
		ipLiteral = ""
	}

	candidates, decision, ok := s.resolveCandidates(req, host, ipLiteral, start)
	if !ok {
		return false, decision.Reason, "", decision
	}

	chosen, lastDecision := s.chooseAllowed(candidates, req, host)
	s.m.PolicyLatency.Observe(time.Since(start).Seconds())

	if chosen == nil {
		reason := normalizeDenyReason(lastDecision.Reason)
		lastDecision.Reason = reason

		return false, reason, "", lastDecision
	}

	chosenIP := chosen.String()
	lastDecision.Allowed = true
	lastDecision.IP = chosenIP
	if lastDecision.Reason == "" {
		lastDecision.Reason = "allowed"
	}

	return true, "allowed", chosenIP, lastDecision
}

func (s *Server) resolveCandidates(
	req *request, host, ipLiteral string, start time.Time,
) ([]net.IP, policy.Decision, bool) {
	if host != "" {
		resolver := &net.Resolver{}
		lookupCtx, cancel := context.WithTimeout(
			context.Background(),
			time.Duration(s.cfg.Proxy.DialTimeoutMs)*time.Millisecond,
		)
		defer cancel()

		resolved, err := resolver.LookupIP(lookupCtx, "ip", host)
		if err != nil {
			decision := denyDecision(s.policyEngine, req, host, pipeline.ReasonDNSFailure)

			s.m.PolicyLatency.Observe(time.Since(start).Seconds())

			return nil, decision, false
		}

		if len(resolved) == 0 {
			decision := denyDecision(s.policyEngine, req, host, pipeline.ReasonDNSFailure)
			s.m.PolicyLatency.Observe(time.Since(start).Seconds())

			return nil, decision, false
		}

		return resolved, policy.Decision{}, true
	}

	parsed := net.ParseIP(ipLiteral)
	if parsed == nil {
		decision := denyDecision(s.policyEngine, req, host, pipeline.ReasonDNSFailure)
		s.m.PolicyLatency.Observe(time.Since(start).Seconds())

		return nil, decision, false
	}

	return []net.IP{parsed}, policy.Decision{}, true
}

func (s *Server) chooseAllowed(candidates []net.IP, req *request, host string) (net.IP, policy.Decision) {
	var chosen net.IP
	var lastDecision policy.Decision

	for _, candidate := range candidates {
		evalReq := policy.Request{
			Workload: req.workloadID,
			Host:     host,
			IP:       candidate.String(),
			Port:     req.port,
		}

		decision := s.policyEngine.Evaluate(evalReq)
		lastDecision = decision
		if decision.Allowed {
			chosen = candidate
			lastDecision = decision

			break
		}
	}

	return chosen, lastDecision
}

func denyDecision(engine *policy.Engine, req *request, host, reason string) policy.Decision {
	return policy.Decision{
		Allowed:       false,
		Reason:        reason,
		PolicyID:      policyID(engine),
		PolicyVersion: policyVersion(engine),
		Workload:      req.workloadID,
		Host:          host,
		Port:          req.port,
		Timestamp:     time.Now().UTC(),
	}
}

func normalizeDenyReason(reason string) string {
	if reason == "" {
		return pipeline.ReasonPolicyDenied
	}

	if reason == "restricted_destination" {
		return pipeline.ReasonRestrictedDestination
	}

	if reason == "missing_identity" {
		return pipeline.ReasonPolicyDenied
	}

	return reason
}

func policyID(engine *policy.Engine) string {
	if p := engine.Policy(); p != nil {
		return p.ID
	}

	return "none"
}

func policyVersion(engine *policy.Engine) string {
	if p := engine.Policy(); p != nil {
		return p.Version
	}

	return "none"
}

func (s *Server) isPolicyEnabled() bool {
	return s.policyEngine != nil && s.cfg.Policy.Enabled
}

func recordPolicyDecision(sess *session, decision *policy.Decision, m *metrics.Metrics) {
	if decision.Allowed {
		sess.policyDecision = "allow"
	} else {
		sess.policyDecision = "deny"
	}

	sess.policyReason = decision.Reason
	sess.policyID = decision.PolicyID
	sess.policyVersion = decision.PolicyVersion

	if decision.Allowed {
		m.PolicyDecisions.WithLabelValues("allow").Inc()
	} else {
		m.PolicyDecisions.WithLabelValues("deny").Inc()
	}
}

func denyPolicyRequest(sess *session, client net.Conn, reason string, log *zap.Logger) {
	sess.outcome = pipeline.OutcomePolicyDenied
	sess.reason = reason
	_ = writeReply(client, replyNotAllowed, nil)
	log.Info("SOCKS5 policy denied",
		zap.String("workload", sess.workloadID),
		zap.String("host", sess.requestedHost),
		zap.String("reason", reason))
}

func (s *Server) dialUpstream(req *request) (net.Conn, int64, error) {
	start := time.Now()
	dialer := net.Dialer{
		Timeout:   time.Duration(s.cfg.Proxy.DialTimeoutMs) * time.Millisecond,
		KeepAlive: 30 * time.Second,
	}

	ctx, cancel := context.WithTimeout(s.ctx, time.Duration(s.cfg.Proxy.DialTimeoutMs)*time.Millisecond)
	defer cancel()

	conn, err := dialer.DialContext(ctx, "tcp", requestAddress(req.dialHost, req.port))
	elapsed := time.Since(start)
	s.m.DialLatency.Observe(elapsed.Seconds())

	if err != nil {
		s.m.DialErrors.WithLabelValues(classifyDialError(err)).Inc()

		return nil, elapsed.Milliseconds(), err
	}

	return conn, elapsed.Milliseconds(), nil
}

func (s *Server) recordHandshakeFailure(sess *session, err error) {
	var handshakeErr *handshakeError
	if !errors.As(err, &handshakeErr) {
		handshakeErr = &handshakeError{reason: metrics.HandshakeMalformedRequest}
	}

	sess.outcome = pipeline.OutcomeHandshakeFailed
	sess.reason = handshakeErr.reason

	if handshakeErr.reason == pipeline.ReasonAuthFailed {
		sess.outcome = pipeline.OutcomeAuthFailed
	}

	s.m.HandshakeFailures.WithLabelValues(handshakeErr.reason).Inc()
	s.log.Debug("SOCKS5 handshake rejected",
		zap.String("client_ip", sess.clientIP),
		zap.String("reason", handshakeErr.reason))
}

func (s *Server) finish(client net.Conn, sess *session) {
	s.untrack(client)
	_ = client.Close()

	remaining := s.active.Add(-1)
	if remaining < 0 {
		s.active.Store(0)
		remaining = 0
	}

	s.m.ActiveConnections.Set(float64(remaining))
	sess.finish(time.Now().UTC())
	s.m.ConnectionDuration.Observe(float64(sess.durationMs()) / 1000.0)

	if sess.outcome == "" {
		sess.outcome = pipeline.OutcomeConnected
	}

	if sess.reason == "" {
		sess.reason = pipeline.ReasonClientClose
	}

	event := sess.event()
	if event.EventID == "" {
		s.log.Warn("failed to allocate analytics event ID; event dropped")
		s.m.EventsDropped.WithLabelValues(metrics.StageCollector).Inc()

		return
	}

	if !s.sink.Collect(event) {
		s.log.Debug("connection event not accepted by analytics queue", zap.String("event_id", event.EventID))
	}
}

// Shutdown stops accepting new connections, waits for active sessions until ctx expires,
// then force-closes the remaining sessions. It returns an error if a grace deadline was hit.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.shutting.Swap(true) {
		return nil
	}

	if s.listener != nil {
		_ = s.listener.Close()
		safeWait(ctx, s.acceptDone)
	}

	sessionsDone := make(chan struct{})

	go func() {
		s.wg.Wait()
		close(sessionsDone)
	}()

	select {
	case <-sessionsDone:
		s.cancel()

		return nil
	case <-ctx.Done():
		s.forced.Store(true)
		s.cancel()
		s.closeAll()

		forceCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		select {
		case <-sessionsDone:
			return fmt.Errorf("proxy shutdown grace period expired; active sessions were closed: %w", ctx.Err())
		case <-forceCtx.Done():
			return errors.New("proxy shutdown timed out after force-closing active sessions")
		}
	}
}

// safeWait waits for ch unless ctx expires first.
func safeWait(ctx context.Context, ch <-chan struct{}) {
	select {
	case <-ch:
	case <-ctx.Done():
	}
}

func (s *Server) closeAll() {
	s.mu.Lock()
	conns := make([]net.Conn, 0, len(s.conns))
	for conn := range s.conns {
		conns = append(conns, conn)
	}
	s.mu.Unlock()

	for _, conn := range conns {
		_ = conn.Close()
	}
}

func (s *Server) track(conn net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.conns[conn] = struct{}{}
}

func (s *Server) untrack(conn net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.conns, conn)
}

func isLoopbackBind(address string) bool {
	if address == "localhost" {
		return true
	}

	ip := net.ParseIP(address)

	return ip != nil && ip.IsLoopback()
}

func remoteIP(addr net.Addr) string {
	if tcpAddr, ok := addr.(*net.TCPAddr); ok {
		return tcpAddr.IP.String()
	}

	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return ""
	}

	return host
}

func classifyDialError(err error) string {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return dialReasonDNS
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return dialReasonTimeout
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return dialReasonTimeout
	}

	if errors.Is(err, context.Canceled) {
		return dialReasonCanceled
	}

	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "refused"):
		return dialReasonRefused
	case strings.Contains(message, "network is unreachable"):
		return dialReasonUnreachable
	case strings.Contains(message, "no such host"):
		return dialReasonDNS
	default:
		return dialReasonOther
	}
}

func replyCodeForDialError(err error) byte {
	reason := classifyDialError(err)

	switch reason {
	case dialReasonRefused:
		return replyConnectionRefused
	case dialReasonUnreachable:
		return replyNetworkUnreachable
	case dialReasonDNS:
		return replyHostUnreachable
	case dialReasonTimeout:
		return replyHostUnreachable
	default:
		return replyGeneralFailure
	}
}
