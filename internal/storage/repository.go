package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/andev0x/socks5-proxy-analytics/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Repository defines the interface for traffic log storage operations.
type Repository interface {
	SaveTrafficLog(ctx context.Context, log *models.TrafficLog) error
	SaveTrafficLogs(ctx context.Context, logs []*models.TrafficLog) error
	GetTopDestinations(ctx context.Context, limit int) ([]models.DestinationStats, error)
	GetTopClientIPs(ctx context.Context, limit int) ([]models.ClientIPStats, error)
	GetTrafficStats(ctx context.Context, startTime, endTime time.Time) (*models.TrafficStats, error)
	GetTrafficByTimeRange(
		ctx context.Context, startTime, endTime time.Time, limit, offset int,
	) ([]models.TrafficLog, error)
	Ping(ctx context.Context) error
	Close() error
}

// PostgresRepository implements Repository using PostgreSQL.
type PostgresRepository struct {
	db *gorm.DB
}

// NewPostgresRepository creates a new PostgreSQL repository.
func NewPostgresRepository(db *gorm.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

// SaveTrafficLog saves a single traffic log to the database.
func (r *PostgresRepository) SaveTrafficLog(ctx context.Context, log *models.TrafficLog) error {
	return r.db.WithContext(ctx).Create(log).Error
}

// SaveTrafficLogs writes a batch atomically. Event IDs are unique and conflicts are ignored,
// so a retry after an ambiguous failure cannot create duplicate analytics records.
func (r *PostgresRepository) SaveTrafficLogs(ctx context.Context, logs []*models.TrafficLog) error {
	if len(logs) == 0 {
		return nil
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "event_id"}},
			DoNothing: true,
		}).CreateInBatches(logs, 100).Error
	})
}

// GetTopDestinations retrieves destinations by connection count.
// A destination is the requested hostname when supplied, otherwise the resolved IP.
func (r *PostgresRepository) GetTopDestinations(
	ctx context.Context, limit int,
) ([]models.DestinationStats, error) {
	var stats []models.DestinationStats
	err := r.db.WithContext(ctx).
		Table("traffic_logs").
		Select(
			"COALESCE(NULLIF(requested_host, ''), destination_ip) as destination",
			"COUNT(*) as count",
			"COALESCE(SUM(bytes_client_to_upstream), 0) as total_bytes_client_to_upstream",
			"COALESCE(SUM(bytes_upstream_to_client), 0) as total_bytes_upstream_to_client",
			"COALESCE(AVG(dial_latency_ms), 0) as avg_dial_latency_ms",
		).
		Where("COALESCE(NULLIF(requested_host, ''), destination_ip) <> ''").
		Group("destination").
		Order("count DESC").
		Limit(limit).
		Scan(&stats).Error

	return stats, err
}

// GetTopClientIPs retrieves SOCKS5 client IPs by connection count.
func (r *PostgresRepository) GetTopClientIPs(
	ctx context.Context, limit int,
) ([]models.ClientIPStats, error) {
	var stats []models.ClientIPStats
	err := r.db.WithContext(ctx).
		Table("traffic_logs").
		Select(
			"client_ip",
			"COUNT(*) as count",
			"COALESCE(SUM(bytes_client_to_upstream), 0) as total_bytes_client_to_upstream",
			"COALESCE(SUM(bytes_upstream_to_client), 0) as total_bytes_upstream_to_client",
			"COALESCE(AVG(dial_latency_ms), 0) as avg_dial_latency_ms",
		).
		Where("client_ip <> ''").
		Group("client_ip").
		Order("count DESC").
		Limit(limit).
		Scan(&stats).Error

	return stats, err
}

// GetTrafficStats retrieves aggregate traffic statistics for a time range.
func (r *PostgresRepository) GetTrafficStats(
	ctx context.Context, startTime, endTime time.Time,
) (*models.TrafficStats, error) {
	var stats models.TrafficStats
	err := r.db.WithContext(ctx).
		Table("traffic_logs").
		Select(
			"COUNT(*) as total_connections",
			"COALESCE(SUM(bytes_client_to_upstream), 0) as total_bytes_client_to_upstream",
			"COALESCE(SUM(bytes_upstream_to_client), 0) as total_bytes_upstream_to_client",
			"COALESCE(AVG(dial_latency_ms), 0) as avg_dial_latency_ms",
			"COALESCE(AVG(duration_ms), 0) as avg_duration_ms",
		).
		Where("timestamp >= ? AND timestamp <= ?", startTime, endTime).
		Scan(&stats).Error

	return &stats, err
}

// GetTrafficByTimeRange retrieves paginated traffic logs for a time range.
func (r *PostgresRepository) GetTrafficByTimeRange(
	ctx context.Context, startTime, endTime time.Time, limit, offset int,
) ([]models.TrafficLog, error) {
	var logs []models.TrafficLog
	err := r.db.WithContext(ctx).
		Where("timestamp >= ? AND timestamp <= ?", startTime, endTime).
		Order("timestamp DESC").
		Limit(limit).
		Offset(offset).
		Find(&logs).Error

	return logs, err
}

// Ping verifies database reachability for health/readiness checks.
func (r *PostgresRepository) Ping(ctx context.Context) error {
	sqlDB, err := r.db.DB()
	if err != nil {
		return fmt.Errorf("failed to get database instance: %w", err)
	}

	return sqlDB.PingContext(ctx)
}

// Close closes the database connection.
func (r *PostgresRepository) Close() error {
	sqlDB, err := r.db.DB()
	if err != nil {
		return fmt.Errorf("failed to get database instance: %w", err)
	}

	return sqlDB.Close()
}
