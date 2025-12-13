package security

import (
	"testing"

	"go.uber.org/zap"
)

func TestAuthenticator(t *testing.T) {
	auth := NewAuthenticator("testuser", "testpass")

	if !auth.IsEnabled() {
		t.Error("expected authenticator to be enabled")
	}

	// Test valid credentials
	if !auth.Authenticate("testuser", "testpass") {
		t.Error("expected valid credentials to authenticate")
	}

	// Test invalid username
	if auth.Authenticate("wronguser", "testpass") {
		t.Error("expected invalid username to fail")
	}

	// Test invalid password
	if auth.Authenticate("testuser", "wrongpass") {
		t.Error("expected invalid password to fail")
	}
}

func TestIPWhitelist(t *testing.T) {
	ips := []string{"192.168.1.1", "192.168.1.2"}
	whitelist := NewIPWhitelist(ips)

	// Test allowed IPs
	if !whitelist.IsAllowed("192.168.1.1") {
		t.Error("expected 192.168.1.1 to be allowed")
	}

	if !whitelist.IsAllowed("192.168.1.2") {
		t.Error("expected 192.168.1.2 to be allowed")
	}

	// Test disallowed IP
	if whitelist.IsAllowed("10.0.0.1") {
		t.Error("expected 10.0.0.1 to be disallowed")
	}

	// Test adding IP
	whitelist.AddIP("10.0.0.1")
	if !whitelist.IsAllowed("10.0.0.1") {
		t.Error("expected 10.0.0.1 to be allowed after adding")
	}

	// Test removing IP
	whitelist.RemoveIP("192.168.1.1")
	if whitelist.IsAllowed("192.168.1.1") {
		t.Error("expected 192.168.1.1 to be disallowed after removal")
	}
}

func TestEmptyWhitelist(t *testing.T) {
	whitelist := NewIPWhitelist([]string{})

	// Empty whitelist should allow all IPs
	if !whitelist.IsAllowed("192.168.1.1") {
		t.Error("expected empty whitelist to allow all IPs")
	}
}

func TestRateLimiter(t *testing.T) {
	log, _ := zap.NewDevelopment()
	limiter := NewRateLimiter(10, true, log)

	// First 10 requests should be allowed
	allowed := 0
	for i := 0; i < 10; i++ {
		if limiter.Allow("test-client") {
			allowed++
		}
	}

	if allowed < 10 {
		t.Errorf("expected at least 10 requests to be allowed, got %d", allowed)
	}

	// Test per-client isolation
	if !limiter.Allow("other-client") {
		t.Error("expected other-client to have its own token bucket")
	}
}

func TestRateLimiterDisabled(t *testing.T) {
	log, _ := zap.NewDevelopment()
	limiter := NewRateLimiter(10, false, log)

	// All requests should be allowed when disabled
	for i := 0; i < 100; i++ {
		if !limiter.Allow("test-client") {
			t.Errorf("expected request %d to be allowed when rate limiting disabled", i+1)
		}
	}
}

func TestGetSourceIP(t *testing.T) {
	log, _ := zap.NewDevelopment()
	limiter := NewRateLimiter(100, true, log)

	tests := []struct {
		input    string
		expected string
	}{
		{"192.168.1.1:5000", "192.168.1.1"},
		{"10.0.0.1:8080", "10.0.0.1"},
		{"[::1]:8080", "::1"},
		{"invalid", "invalid"},
	}

	for _, tt := range tests {
		result := limiter.GetSourceIP(tt.input)
		if result != tt.expected {
			t.Errorf("expected %s, got %s", tt.expected, result)
		}
	}
}
