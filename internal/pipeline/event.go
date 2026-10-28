package pipeline

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync/atomic"
	"time"
)

var eventIDFallbackCounter atomic.Uint64

// Connection outcomes persisted in the analytics schema.
const (
	OutcomeConnected       = "connected"
	OutcomeDialFailed      = "dial_failed"
	OutcomeAuthFailed      = "auth_failed"
	OutcomeHandshakeFailed = "handshake_failed"
)

// Termination reasons persisted in the analytics schema.
const (
	ReasonClientClose            = "client_close"
	ReasonUpstreamClose          = "upstream_close"
	ReasonRelayError             = "relay_error"
	ReasonServerShutdown         = "server_shutdown"
	ReasonDialError              = "dial_error"
	ReasonAuthFailed             = "auth_failed"
	ReasonMalformedRequest       = "malformed_request"
	ReasonUnsupportedCommand     = "unsupported_command"
	ReasonUnsupportedAddressType = "unsupported_address_type"
	ReasonNoAcceptableAuth       = "no_acceptable_auth"
	ReasonTimeout                = "timeout"
)

// ConnectionEvent is the raw, best-effort event emitted at the end of a SOCKS5 session.
type ConnectionEvent struct {
	EventID               string
	StartedAt             time.Time
	EndedAt               time.Time
	ClientIP              string
	ClientPort            int
	RequestedHost         string
	DestinationIP         string
	DestinationPort       int
	Protocol              string
	Outcome               string
	TerminationReason     string
	DialLatencyMs         int64
	DurationMs            int64
	BytesClientToUpstream int64
	BytesUpstreamToClient int64
}

// NewEventID returns a random, non-sensitive correlation/deduplication identity.
func NewEventID() string {
	var buf [16]byte

	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("fallback-%016x-%08x", time.Now().UnixNano(), eventIDFallbackCounter.Add(1))
	}

	return hex.EncodeToString(buf[:])
}
