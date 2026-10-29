package proxy

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/andev0x/socks5-proxy-analytics/internal/config"
	"github.com/andev0x/socks5-proxy-analytics/internal/metrics"
	"github.com/andev0x/socks5-proxy-analytics/internal/pipeline"
	"github.com/andev0x/socks5-proxy-analytics/internal/policy"
	"go.uber.org/zap"
)

func TestPolicyEnforcementDeniesBeforeDial(t *testing.T) {
	upstream := startTestUpstream(t, func(conn net.Conn) {
		_, _ = io.Copy(conn, conn)
	})
	defer func() { _ = upstream }()

	cfg := testProxyConfig()
	cfg.Proxy.Auth.Enabled = true
	cfg.Proxy.Auth.Username = "agent"
	cfg.Proxy.Auth.Password = "secret"
	cfg.Proxy.Workloads = []config.WorkloadConfig{
		{ID: "agent-1", Username: "agent", Password: "secret"},
	}
	cfg.Policy.Enabled = true
	cfg.Policy.ID = "p"
	cfg.Policy.Version = "1"
	cfg.Policy.DefaultAction = "deny"
	cfg.Policy.Rules = []config.PolicyRuleConfig{
		{ID: "allow-localhost", Workload: "agent-1", Action: "allow", Host: "localhost", IP: "127.0.0.1/32"},
	}

	engine, err := policy.NewEngineFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}

	m, err := metrics.NewMetrics()
	if err != nil {
		t.Fatal(err)
	}

	sink := newTestSink()
	server, err := NewServer(cfg, zap.NewNop(), sink, m, engine)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	})

	// Denied destination should fail with replyNotAllowed.
	creds := &proxyCredentials{username: "agent", password: "secret"}
	conn, code := openSOCKS(t, server.Addr().String(), "127.0.0.2", 80, creds)
	_ = conn.Close()
	if code != replyNotAllowed {
		t.Fatalf("denied reply = %d, want %d", code, replyNotAllowed)
	}

	event := sink.wait(t)
	if event.Outcome != pipeline.OutcomePolicyDenied {
		t.Errorf("outcome = %q, want %s", event.Outcome, pipeline.OutcomePolicyDenied)
	}
	if event.WorkloadID != "agent-1" {
		t.Errorf("workload = %q", event.WorkloadID)
	}
	if event.PolicyDecision != "deny" {
		t.Errorf("policy decision = %q", event.PolicyDecision)
	}

	// Allowed destination succeeds.
	conn, code = openSOCKS(t, server.Addr().String(), "localhost", upstream.Port, creds)
	if code != replySucceeded {
		t.Fatalf("allowed reply = %d, want success", code)
	}
	_ = conn.Close()
	sink.wait(t)
}

// TestWorkloadOnlyAuthenticationAttributesIdentity verifies the documented configuration
// where workload identities replace the legacy single username/password pair.
func TestWorkloadOnlyAuthenticationAttributesIdentity(t *testing.T) {
	upstream := startTestUpstream(t, func(conn net.Conn) {
		_, _ = io.Copy(conn, conn)
	})

	cfg := testProxyConfig()
	cfg.Proxy.Auth.Enabled = true
	cfg.Proxy.Auth.Username = ""
	cfg.Proxy.Auth.Password = ""
	cfg.Proxy.Workloads = []config.WorkloadConfig{
		{ID: "agent-1", Username: "agent", Password: "secret"},
	}

	sink := newTestSink()
	server, _ := startTestProxy(t, cfg, sink)

	conn, code := openSOCKS(t, server.Addr().String(), "127.0.0.1", upstream.Port,
		&proxyCredentials{username: "agent", password: "secret"})
	if code != replySucceeded {
		t.Fatalf("SOCKS reply = %d, want success", code)
	}

	_ = conn.Close()

	event := sink.wait(t)
	if event.WorkloadID != "agent-1" {
		t.Errorf("WorkloadID = %q, want agent-1", event.WorkloadID)
	}
}

// TestPolicyDefaultDenyWithoutMatchingRule verifies that enabling policy with no rules
// denies every request instead of forwarding it.
func TestPolicyDefaultDenyWithoutMatchingRule(t *testing.T) {
	upstream := startTestUpstream(t, func(conn net.Conn) {
		_, _ = io.Copy(conn, conn)
	})

	cfg := testProxyConfig()
	cfg.Proxy.Auth.Enabled = true
	cfg.Proxy.Workloads = []config.WorkloadConfig{
		{ID: "agent-1", Username: "agent", Password: "secret"},
	}
	cfg.Policy.Enabled = true
	cfg.Policy.DefaultAction = "deny"

	engine, err := policy.NewEngineFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}

	m, err := metrics.NewMetrics()
	if err != nil {
		t.Fatal(err)
	}

	sink := newTestSink()
	server, err := NewServer(cfg, zap.NewNop(), sink, m, engine)
	if err != nil {
		t.Fatal(err)
	}

	if err := server.Start(); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	})

	conn, code := openSOCKS(t, server.Addr().String(), "127.0.0.1", upstream.Port,
		&proxyCredentials{username: "agent", password: "secret"})
	_ = conn.Close()

	if code != replyNotAllowed {
		t.Fatalf("reply = %d, want %d (policy deny)", code, replyNotAllowed)
	}

	event := sink.wait(t)
	if event.Outcome != pipeline.OutcomePolicyDenied {
		t.Errorf("Outcome = %q, want policy_denied", event.Outcome)
	}

	if event.DestinationIP != "" {
		t.Errorf("DestinationIP = %q, want empty because no dial may occur", event.DestinationIP)
	}
}
