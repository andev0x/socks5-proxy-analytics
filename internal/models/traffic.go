// Package models defines the persisted analytics schema and aggregate statistics.
package models

import (
	"time"

	"gorm.io/gorm"
)

// EventSchemaVersion is stored with every record so consumers can evolve safely.
const EventSchemaVersion = 1

// TrafficLog represents one completed SOCKS5 session.
//
// Attribution is explicit: ClientIP is the SOCKS5 peer, RequestedHost is the hostname
// from the request (empty for IP literals), DestinationIP is the IP actually dialed.
// Byte counters are application payload only and exclude SOCKS5 handshake bytes.
type TrafficLog struct {
	ID                    uint           `gorm:"primaryKey" json:"id"`
	EventID               string         `gorm:"size:64;uniqueIndex" json:"event_id"`
	ClientIP              string         `gorm:"column:client_ip;index" json:"client_ip"`
	ClientPort            int            `json:"client_port"`
	RequestedHost         string         `gorm:"column:requested_host;index" json:"requested_host"`
	DestinationIP         string         `gorm:"column:destination_ip;index" json:"destination_ip"`
	DestinationPort       int            `json:"destination_port"`
	Protocol              string         `json:"protocol"`
	Outcome               string         `gorm:"index" json:"outcome"`
	TerminationReason     string         `json:"termination_reason"`
	WorkloadID            string         `gorm:"column:workload_id;index" json:"workload_id"`
	PolicyDecision        string         `gorm:"column:policy_decision" json:"policy_decision"`
	PolicyReason          string         `gorm:"column:policy_reason" json:"policy_reason"`
	PolicyID              string         `gorm:"column:policy_id" json:"policy_id"`
	PolicyVersion         string         `gorm:"column:policy_version" json:"policy_version"`
	Timestamp             time.Time      `gorm:"index" json:"timestamp"`
	DurationMs            int64          `json:"duration_ms"`
	DialLatencyMs         int64          `json:"dial_latency_ms"`
	BytesClientToUpstream int64          `json:"bytes_client_to_upstream"`
	BytesUpstreamToClient int64          `json:"bytes_upstream_to_client"`
	SchemaVersion         int            `json:"schema_version"`
	CreatedAt             time.Time      `gorm:"autoCreateTime" json:"created_at"`
	DeletedAt             gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName specifies the table name.
func (TrafficLog) TableName() string {
	return "traffic_logs"
}

// DestinationStats represents aggregate statistics for a requested destination.
type DestinationStats struct {
	Destination                string  `json:"destination"`
	Count                      int64   `json:"count"`
	TotalBytesClientToUpstream int64   `json:"total_bytes_client_to_upstream"`
	TotalBytesUpstreamToClient int64   `json:"total_bytes_upstream_to_client"`
	AvgDialLatencyMs           float64 `json:"avg_dial_latency_ms"`
}

// ClientIPStats represents aggregate statistics for a SOCKS5 client IP.
type ClientIPStats struct {
	ClientIP                   string  `json:"client_ip"`
	Count                      int64   `json:"count"`
	TotalBytesClientToUpstream int64   `json:"total_bytes_client_to_upstream"`
	TotalBytesUpstreamToClient int64   `json:"total_bytes_upstream_to_client"`
	AvgDialLatencyMs           float64 `json:"avg_dial_latency_ms"`
}

// TrafficStats represents aggregate traffic statistics for a time range.
type TrafficStats struct {
	TotalConnections           int64   `json:"total_connections"`
	TotalBytesClientToUpstream int64   `json:"total_bytes_client_to_upstream"`
	TotalBytesUpstreamToClient int64   `json:"total_bytes_upstream_to_client"`
	AvgDialLatencyMs           float64 `json:"avg_dial_latency_ms"`
	AvgDurationMs              float64 `json:"avg_duration_ms"`
}
