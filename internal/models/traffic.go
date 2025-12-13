package models

import (
	"time"

	"gorm.io/gorm"
)

type TrafficLog struct {
	ID            uint           `gorm:"primaryKey" json:"id"`
	SourceIP      string         `gorm:"index" json:"source_ip"`
	DestinationIP string         `gorm:"index" json:"destination_ip"`
	Domain        string         `gorm:"index" json:"domain"`
	Port          int            `json:"port"`
	Timestamp     time.Time      `gorm:"index" json:"timestamp"`
	LatencyMs     int64          `json:"latency_ms"`
	BytesIn       int64          `json:"bytes_in"`
	BytesOut      int64          `json:"bytes_out"`
	Protocol      string         `json:"protocol"`
	CreatedAt     time.Time      `gorm:"autoCreateTime" json:"created_at"`
	DeletedAt     gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName specifies the table name
func (TrafficLog) TableName() string {
	return "traffic_logs"
}

// DomainStats represents statistics for a domain
type DomainStats struct {
	Domain        string  `json:"domain"`
	Count         int64   `json:"count"`
	TotalBytesIn  int64   `json:"total_bytes_in"`
	TotalBytesOut int64   `json:"total_bytes_out"`
	AvgLatency    float64 `json:"avg_latency_ms"`
}

// SourceIPStats represents statistics for a source IP
type SourceIPStats struct {
	SourceIP      string  `json:"source_ip"`
	Count         int64   `json:"count"`
	TotalBytesIn  int64   `json:"total_bytes_in"`
	TotalBytesOut int64   `json:"total_bytes_out"`
	AvgLatency    float64 `json:"avg_latency_ms"`
}

// TrafficStats represents overall traffic statistics
type TrafficStats struct {
	TotalConnections int64   `json:"total_connections"`
	TotalBytesIn     int64   `json:"total_bytes_in"`
	TotalBytesOut    int64   `json:"total_bytes_out"`
	AvgLatency       float64 `json:"avg_latency_ms"`
}
