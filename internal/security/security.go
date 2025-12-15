// Package security provides authentication, authorization, and rate limiting.
package security

import (
	"net"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Authenticator handles SOCKS5 authentication.
type Authenticator struct {
	username string
	password string
	enabled  bool
}

// NewAuthenticator creates a new authenticator with the given credentials.
func NewAuthenticator(username, password string) *Authenticator {
	return &Authenticator{
		username: username,
		password: password,
		enabled:  true,
	}
}

// Authenticate checks if the provided credentials are valid.
func (a *Authenticator) Authenticate(username, password string) bool {
	if !a.enabled {
		return true
	}

	return username == a.username && password == a.password
}

// IsEnabled returns whether authentication is enabled.
func (a *Authenticator) IsEnabled() bool {
	return a.enabled
}

// IPWhitelist handles IP filtering.
type IPWhitelist struct {
	allowedIPs map[string]bool
	enabled    bool
	mu         sync.RWMutex
}

// NewIPWhitelist creates a new IP whitelist from the given IP addresses.
func NewIPWhitelist(ips []string) *IPWhitelist {
	whitelist := &IPWhitelist{
		allowedIPs: make(map[string]bool),
		enabled:    len(ips) > 0,
	}

	for _, ip := range ips {
		whitelist.allowedIPs[ip] = true
	}

	return whitelist
}

// IsAllowed checks if an IP address is allowed.
func (w *IPWhitelist) IsAllowed(ip string) bool {
	if !w.enabled {
		return true
	}

	w.mu.RLock()
	defer w.mu.RUnlock()

	return w.allowedIPs[ip]
}

// AddIP adds an IP address to the whitelist.
func (w *IPWhitelist) AddIP(ip string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.allowedIPs[ip] = true
}

// RemoveIP removes an IP address from the whitelist.
func (w *IPWhitelist) RemoveIP(ip string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.allowedIPs, ip)
}

// RateLimiter implements token bucket rate limiting.
type RateLimiter struct {
	requestsPerSecond int
	buckets           map[string]*tokenBucket
	mu                sync.RWMutex
	enabled           bool
	log               *zap.Logger
}

type tokenBucket struct {
	tokens    float64
	lastTime  time.Time
	ratePerMs float64
}

// NewRateLimiter creates a new rate limiter with token bucket algorithm.
func NewRateLimiter(requestsPerSecond int, enabled bool, log *zap.Logger) *RateLimiter {
	return &RateLimiter{
		requestsPerSecond: requestsPerSecond,
		buckets:           make(map[string]*tokenBucket),
		enabled:           enabled,
		log:               log,
	}
}

// Allow checks if a request from the identifier is allowed.
func (rl *RateLimiter) Allow(identifier string) bool {
	if !rl.enabled {
		return true
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	bucket, exists := rl.buckets[identifier]
	now := time.Now()

	if !exists {
		bucket = &tokenBucket{
			tokens:    float64(rl.requestsPerSecond),
			lastTime:  now,
			ratePerMs: float64(rl.requestsPerSecond) / 1000.0,
		}
		rl.buckets[identifier] = bucket

		return true
	}

	// Calculate tokens to add based on elapsed time
	elapsed := now.Sub(bucket.lastTime).Milliseconds()
	tokensToAdd := float64(elapsed) * bucket.ratePerMs
	bucket.tokens = minFloat(float64(rl.requestsPerSecond), bucket.tokens+tokensToAdd)
	bucket.lastTime = now

	// Check if we have at least one token
	if bucket.tokens >= 1.0 {
		bucket.tokens -= 1.0

		return true
	}

	return false
}

// GetSourceIP extracts the source IP from a remote address.
func (rl *RateLimiter) GetSourceIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}

	return host
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}

	return b
}
