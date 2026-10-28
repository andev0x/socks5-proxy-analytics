package security

import (
	"net"
	"testing"
	"time"
)

func TestAuthenticator(t *testing.T) {
	auth := NewAuthenticator("testuser", "testpass")

	if !auth.IsEnabled() {
		t.Error("expected authenticator to be enabled")
	}

	if !auth.Authenticate("testuser", "testpass") {
		t.Error("expected valid credentials to authenticate")
	}

	if auth.Authenticate("wronguser", "testpass") {
		t.Error("expected invalid username to fail")
	}

	if auth.Authenticate("testuser", "wrongpass") {
		t.Error("expected invalid password to fail")
	}

	if auth.Authenticate("", "") {
		t.Error("expected empty credentials to fail")
	}
}

func TestIPWhitelistExactAndCIDR(t *testing.T) {
	whitelist, err := NewIPWhitelist([]string{"192.168.1.10", "10.0.0.0/8", "fd00::/8", "::1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tests := []struct {
		ip      string
		allowed bool
	}{
		{"192.168.1.10", true},
		{"10.20.30.40", true},
		{"fd00::1", true},
		{"::1", true},
		{"192.168.1.11", false},
		{"8.8.8.8", false},
		{"not-an-ip", false},
	}

	for _, test := range tests {
		if got := whitelist.IsAllowed(test.ip); got != test.allowed {
			t.Errorf("IsAllowed(%q) = %v, want %v", test.ip, got, test.allowed)
		}
	}
}

func TestIPWhitelistEmptyAllowsAll(t *testing.T) {
	whitelist, err := NewIPWhitelist(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !whitelist.IsAllowed("192.168.1.1") {
		t.Error("expected empty whitelist to allow all IPs")
	}
}

func TestIPWhitelistRejectsInvalidEntry(t *testing.T) {
	if _, err := NewIPWhitelist([]string{"not-an-ip"}); err == nil {
		t.Error("expected invalid whitelist entry to fail")
	}
}

func TestIPWhitelistAddAndRemove(t *testing.T) {
	whitelist, err := NewIPWhitelist([]string{"192.168.1.1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	whitelist.AddIP("10.0.0.1")
	if !whitelist.IsAllowed("10.0.0.1") {
		t.Error("expected 10.0.0.1 to be allowed after adding")
	}

	whitelist.RemoveIP("192.168.1.1")
	if whitelist.IsAllowed("192.168.1.1") {
		t.Error("expected 192.168.1.1 to be disallowed after removal")
	}
}

func TestRateLimiterBurstAndRefill(t *testing.T) {
	limiter := NewRateLimiter(2, true)
	now := time.Now()
	limiter.now = func() time.Time { return now }

	if !limiter.Allow("client-a") {
		t.Fatal("first request should be allowed")
	}

	if !limiter.Allow("client-a") {
		t.Fatal("second request should be allowed")
	}

	if limiter.Allow("client-a") {
		t.Fatal("third request should be rate limited")
	}

	if !limiter.Allow("client-b") {
		t.Fatal("other clients must have independent buckets")
	}

	now = now.Add(time.Second)
	if !limiter.Allow("client-a") {
		t.Fatal("bucket should refill after one second")
	}
}

func TestRateLimiterDisabled(t *testing.T) {
	limiter := NewRateLimiter(1, false)

	for i := 0; i < 100; i++ {
		if !limiter.Allow("client") {
			t.Fatalf("request %d should be allowed when disabled", i+1)
		}
	}
}

func TestRateLimiterBoundsTrackedClients(t *testing.T) {
	limiter := NewRateLimiter(10, true)
	limiter.SetMaxClients(3)
	now := time.Now()
	limiter.now = func() time.Time { return now }

	for i := 0; i < 20; i++ {
		limiter.Allow("client-" + string(rune('a'+i)))
		now = now.Add(time.Millisecond)
	}

	if tracked := limiter.TrackedClients(); tracked > 3 {
		t.Fatalf("tracked clients = %d, want <= 3", tracked)
	}
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		address  net.Addr
		expected string
	}{
		{&net.TCPAddr{IP: net.ParseIP("192.168.1.1"), Port: 5000}, "192.168.1.1"},
		{&net.TCPAddr{IP: net.ParseIP("::1"), Port: 8080}, "::1"},
		{nil, ""},
	}

	for _, test := range tests {
		if got := ClientIP(test.address); got != test.expected {
			t.Errorf("ClientIP(%v) = %q, want %q", test.address, got, test.expected)
		}
	}
}
