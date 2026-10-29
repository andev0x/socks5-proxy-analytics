// Package security provides authentication, client address rules and rate limiting for the proxy.
package security

import (
	"crypto/subtle"
	"errors"
	"net"
	"strings"
	"sync"
	"time"
)

// WorkloadCredential maps a workload identity to SOCKS5 credentials.
type WorkloadCredential struct {
	WorkloadID string
	Username   string
	Password   string
}

// Authenticator validates SOCKS5 username/password credentials (RFC 1929) and
// resolves them to a workload identity.
type Authenticator struct {
	credentials map[string]credentialEntry
	enabled     bool
}

type credentialEntry struct {
	password   string
	workloadID string
}

// NewAuthenticator creates a new authenticator with a single legacy credential.
// The resolved workload identity is "default".
func NewAuthenticator(username, password string) *Authenticator {
	auth := &Authenticator{
		credentials: make(map[string]credentialEntry),
		enabled:     true,
	}
	auth.credentials[username] = credentialEntry{password: password, workloadID: "default"}

	return auth
}

// NewAuthenticatorFromWorkloads creates an authenticator for multiple workloads.
func NewAuthenticatorFromWorkloads(credentials []WorkloadCredential) *Authenticator {
	auth := &Authenticator{
		credentials: make(map[string]credentialEntry),
		enabled:     true,
	}

	for _, credential := range credentials {
		auth.credentials[credential.Username] = credentialEntry{
			password:   credential.Password,
			workloadID: credential.WorkloadID,
		}
	}

	return auth
}

// Authenticate checks the provided credentials using constant-time comparisons.
func (a *Authenticator) Authenticate(username, password string) bool {
	_, ok := a.AuthenticateWithIdentity(username, password)

	return ok
}

// AuthenticateWithIdentity validates credentials and returns the workload identity.
func (a *Authenticator) AuthenticateWithIdentity(username, password string) (string, bool) {
	if !a.enabled {
		return "", true
	}

	entry, ok := a.credentials[username]
	if !ok {
		// Constant-time dummy comparison to avoid timing leaks.
		subtle.ConstantTimeCompare([]byte(password), []byte{})

		return "", false
	}

	userMatch := subtle.ConstantTimeCompare([]byte(username), []byte(username)) == 1
	passMatch := subtle.ConstantTimeCompare([]byte(password), []byte(entry.password)) == 1

	if !userMatch || !passMatch {
		return "", false
	}

	return entry.workloadID, true
}

// IsEnabled returns whether authentication is enabled.
func (a *Authenticator) IsEnabled() bool {
	return a.enabled
}

// IPWhitelist filters client IPs by exact IP or CIDR prefix.
type IPWhitelist struct {
	allowedIPs map[string]struct{}
	allowedNet []*net.IPNet
	enabled    bool
	mu         sync.RWMutex
}

// NewIPWhitelist creates a whitelist from IP addresses and CIDR ranges.
// An invalid entry returns an error so misconfiguration never silently disables the rule.
func NewIPWhitelist(entries []string) (*IPWhitelist, error) {
	whitelist := &IPWhitelist{
		allowedIPs: make(map[string]struct{}),
		enabled:    len(entries) > 0,
	}

	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			return nil, errors.New("empty IP whitelist entry")
		}

		if ip := net.ParseIP(entry); ip != nil {
			whitelist.allowedIPs[ip.String()] = struct{}{}

			continue
		}

		_, network, err := net.ParseCIDR(entry)
		if err != nil {
			return nil, errors.New("IP whitelist entry must be an IP address or CIDR")
		}

		whitelist.allowedNet = append(whitelist.allowedNet, network)
	}

	return whitelist, nil
}

// IsAllowed checks if an IP address is allowed. An empty whitelist allows all clients.
func (w *IPWhitelist) IsAllowed(ip string) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if !w.enabled {
		return true
	}

	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}

	if _, ok := w.allowedIPs[parsed.String()]; ok {
		return true
	}

	for _, network := range w.allowedNet {
		if network.Contains(parsed) {
			return true
		}
	}

	return false
}

// AddIP adds an exact IP address to the whitelist.
func (w *IPWhitelist) AddIP(ip string) {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	w.allowedIPs[parsed.String()] = struct{}{}
	w.enabled = true
}

// RemoveIP removes an exact IP address from the whitelist.
func (w *IPWhitelist) RemoveIP(ip string) {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	delete(w.allowedIPs, parsed.String())
}

// RateLimiter implements a bounded-memory, per-client token bucket rate limiter.
// The unit is one new TCP connection per source IP.
type RateLimiter struct {
	requestsPerSecond int
	buckets           map[string]*tokenBucket
	maxClients        int
	mu                sync.Mutex
	enabled           bool
	now               func() time.Time
}

type tokenBucket struct {
	tokens   float64
	lastTime time.Time
	lastSeen time.Time
}

// NewRateLimiter creates a rate limiter with the given per-client rate.
func NewRateLimiter(requestsPerSecond int, enabled bool) *RateLimiter {
	return &RateLimiter{
		requestsPerSecond: requestsPerSecond,
		buckets:           make(map[string]*tokenBucket),
		maxClients:        100000,
		enabled:           enabled,
		now:               time.Now,
	}
}

// SetMaxClients bounds the number of tracked client buckets.
func (rl *RateLimiter) SetMaxClients(maxClients int) {
	if maxClients <= 0 {
		return
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.maxClients = maxClients
	rl.evictLocked()
}

// Allow reports whether a new connection from the identifier is allowed.
func (rl *RateLimiter) Allow(identifier string) bool {
	if !rl.enabled {
		return true
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.now()
	bucket, exists := rl.buckets[identifier]

	if !exists {
		rl.evictLocked()
		rl.buckets[identifier] = &tokenBucket{
			tokens:   float64(rl.requestsPerSecond) - 1,
			lastTime: now,
			lastSeen: now,
		}

		return true
	}

	elapsed := now.Sub(bucket.lastTime).Seconds()
	bucket.tokens += elapsed * float64(rl.requestsPerSecond)
	if bucket.tokens > float64(rl.requestsPerSecond) {
		bucket.tokens = float64(rl.requestsPerSecond)
	}

	bucket.lastTime = now
	bucket.lastSeen = now

	if bucket.tokens >= 1 {
		bucket.tokens--

		return true
	}

	return false
}

// evictLocked removes idle buckets; callers must hold rl.mu.
func (rl *RateLimiter) evictLocked() {
	if len(rl.buckets) < rl.maxClients {
		return
	}

	cutoff := rl.now().Add(-time.Minute)
	for key, bucket := range rl.buckets {
		if bucket.lastSeen.Before(cutoff) {
			delete(rl.buckets, key)
		}
	}

	// If every bucket is active, evict the least recently seen entry to keep memory bounded.
	for len(rl.buckets) >= rl.maxClients {
		var (
			oldestKey  string
			oldestTime time.Time
		)

		for key, bucket := range rl.buckets {
			if oldestKey == "" || bucket.lastSeen.Before(oldestTime) {
				oldestKey = key
				oldestTime = bucket.lastSeen
			}
		}

		if oldestKey == "" {
			return
		}

		delete(rl.buckets, oldestKey)
	}
}

// TrackedClients returns the number of rate limiter buckets (for tests and diagnostics).
func (rl *RateLimiter) TrackedClients() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	return len(rl.buckets)
}

// ClientIP extracts the IP from a host:port remote address.
func ClientIP(remoteAddr net.Addr) string {
	if remoteAddr == nil {
		return ""
	}

	host, _, err := net.SplitHostPort(remoteAddr.String())
	if err != nil {
		return remoteAddr.String()
	}

	return host
}
