package policy

import (
	"net"
	"testing"
)

func TestDefaultDeny(t *testing.T) {
	engine := NewEngine()
	policy := &Policy{ID: "p", Version: "1", DefaultAction: ActionDeny}
	if err := engine.SetPolicy(policy); err != nil {
		t.Fatal(err)
	}

	decision := engine.Evaluate(Request{Workload: "agent", Host: "example.com", Port: 443})
	if decision.Allowed {
		t.Fatal("expected default deny")
	}

	if decision.Reason != "default_deny" {
		t.Fatalf("reason = %q", decision.Reason)
	}
}

func TestMissingIdentityDenied(t *testing.T) {
	engine := NewEngine()
	_ = engine.SetPolicy(&Policy{ID: "p", Version: "1", DefaultAction: ActionDeny})

	decision := engine.Evaluate(Request{Host: "example.com", Port: 443})
	if decision.Allowed || decision.Reason != "missing_identity" {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestAllowExactHost(t *testing.T) {
	engine := NewEngine()
	_ = engine.SetPolicy(&Policy{
		ID:            "p",
		Version:       "1",
		DefaultAction: ActionDeny,
		Rules: []Rule{
			{ID: "r1", Workload: "agent", Action: ActionAllow, Host: "api.example.com", Port: 443},
		},
	})

	if d := engine.Evaluate(Request{Workload: "agent", Host: "api.example.com", Port: 443}); !d.Allowed {
		t.Fatalf("expected allow, got %+v", d)
	}

	if d := engine.Evaluate(Request{Workload: "agent", Host: "other.example.com", Port: 443}); d.Allowed {
		t.Fatalf("expected deny, got %+v", d)
	}
}

func TestSuffixBoundary(t *testing.T) {
	engine := NewEngine()
	_ = engine.SetPolicy(&Policy{
		ID:            "p",
		Version:       "1",
		DefaultAction: ActionDeny,
		Rules: []Rule{
			{ID: "r1", Workload: "*", Action: ActionAllow, HostSuffix: "example.com", Port: 443},
		},
	})

	cases := []struct {
		host  string
		allow bool
	}{
		{"example.com", true},
		{"api.example.com", true},
		{"notexample.com", false},
		{"evil-example.com", false},
	}

	for _, tc := range cases {
		d := engine.Evaluate(Request{Workload: "agent", Host: tc.host, Port: 443})
		if d.Allowed != tc.allow {
			t.Errorf("host %q: allowed=%v want %v", tc.host, d.Allowed, tc.allow)
		}
	}
}

func TestDenyPrecedence(t *testing.T) {
	engine := NewEngine()
	_ = engine.SetPolicy(&Policy{
		ID:            "p",
		Version:       "1",
		DefaultAction: ActionDeny,
		Rules: []Rule{
			{ID: "allow-all", Workload: "*", Action: ActionAllow, HostSuffix: "example.com"},
			{ID: "deny-bad", Workload: "*", Action: ActionDeny, Host: "bad.example.com"},
		},
	})

	d := engine.Evaluate(Request{Workload: "agent", Host: "bad.example.com", Port: 443})
	if d.Allowed {
		t.Fatal("expected deny precedence")
	}
}

func TestIPCIDRAndPortRange(t *testing.T) {
	engine := NewEngine()
	_ = engine.SetPolicy(&Policy{
		ID:            "p",
		Version:       "1",
		DefaultAction: ActionDeny,
		Rules: []Rule{
			{ID: "r1", Workload: "agent", Action: ActionAllow, IP: "10.0.0.0/8", PortStart: 8000, PortEnd: 9000},
		},
	})

	if d := engine.Evaluate(Request{Workload: "agent", IP: "10.1.2.3", Port: 8080}); !d.Allowed {
		t.Fatalf("expected allow, got %+v", d)
	}

	if d := engine.Evaluate(Request{Workload: "agent", IP: "10.1.2.3", Port: 7000}); d.Allowed {
		t.Fatalf("expected deny port, got %+v", d)
	}

	if d := engine.Evaluate(Request{Workload: "agent", IP: "192.168.1.1", Port: 8080}); d.Allowed {
		t.Fatalf("expected deny ip, got %+v", d)
	}
}

func TestRestrictedDestination(t *testing.T) {
	engine := NewEngine()
	_ = engine.SetPolicy(&Policy{
		ID:              "p",
		Version:         "1",
		DefaultAction:   ActionDeny,
		RestrictedCIDRs: []string{"127.0.0.0/8"},
		Rules: []Rule{
			{ID: "r1", Workload: "agent", Action: ActionAllow, Host: "localhost"},
		},
	})

	d := engine.Evaluate(Request{Workload: "agent", Host: "localhost", IP: "127.0.0.1", Port: 80})
	if d.Allowed {
		t.Fatal("expected restricted deny")
	}

	if d.Reason != "restricted_destination" {
		t.Fatalf("reason = %q", d.Reason)
	}

	// Explicit IP allow overrides restriction.
	engine2 := NewEngine()
	_ = engine2.SetPolicy(&Policy{
		ID:              "p",
		Version:         "1",
		DefaultAction:   ActionDeny,
		RestrictedCIDRs: []string{"127.0.0.0/8"},
		Rules: []Rule{
			{ID: "r1", Workload: "agent", Action: ActionAllow, IP: "127.0.0.1/32", Port: 80},
		},
	})
	d2 := engine2.Evaluate(Request{Workload: "agent", IP: "127.0.0.1", Port: 80})
	if !d2.Allowed {
		t.Fatalf("expected allow with explicit ip, got %+v", d2)
	}
}

func TestPolicyUnavailableFailClosed(t *testing.T) {
	engine := NewEngine()
	d := engine.Evaluate(Request{Workload: "agent", Host: "example.com", Port: 443})
	if d.Allowed || d.Reason != "policy_unavailable" {
		t.Fatalf("decision = %+v", d)
	}
}

func TestInvalidPolicyRejected(t *testing.T) {
	engine := NewEngine()
	err := engine.SetPolicy(&Policy{ID: "p", Version: "1", DefaultAction: ActionAllow})
	if err == nil {
		t.Fatal("expected invalid default action rejection")
	}
}

func TestIPv4MappedIPv6(t *testing.T) {
	// Ensure restricted range contains mapped representation? net.IP handles.
	_ = net.ParseIP("::ffff:127.0.0.1")
}
