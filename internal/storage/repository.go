package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/andev0x/socks5-proxy-analytics/internal/models"
	"gorm.io/gorm"
)

// Repository defines the interface for traffic log storage operations.
type Repository interface {
	SaveTrafficLog(ctx context.Context, log *models.TrafficLog) error
	SaveTrafficLogs(ctx context.Context, logs []*models.TrafficLog) error
	GetTopDomains(ctx context.Context, limit int) ([]models.DomainStats, error)
	GetTopSourceIPs(ctx context.Context, limit int) ([]models.SourceIPStats, error)
	GetTrafficStats(ctx context.Context, startTime, endTime time.Time) (*models.TrafficStats, error)
	GetTrafficByTimeRange(
		ctx context.Context, startTime, endTime time.Time, limit, offset int,
	) ([]models.TrafficLog, error)
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

// SaveTrafficLogs saves multiple traffic logs to the database in batches.
func (r *PostgresRepository) SaveTrafficLogs(ctx context.Context, logs []*models.TrafficLog) error {
	if len(logs) == 0 {
		return nil
	}

	return r.db.WithContext(ctx).CreateInBatches(logs, 100).Error
}

// GetTopDomains retrieves the top domains by connection count.
func (r *PostgresRepository) GetTopDomains(ctx context.Context, limit int) ([]models.DomainStats, error) {
	var stats []models.DomainStats
	err := r.db.WithContext(ctx).
		Table("traffic_logs").
		Select(
			"domain",
			"COUNT(*) as count",
			"COALESCE(SUM(bytes_in), 0) as total_bytes_in",
			"COALESCE(SUM(bytes_out), 0) as total_bytes_out",
			"COALESCE(AVG(latency_ms), 0) as avg_latency",
		).
		Where("domain != ''").
		Group("domain").
		Order("count DESC").
		Limit(limit).
		Scan(&stats).Error

	return stats, err
}

// GetTopSourceIPs retrieves the top source IPs by connection count.
func (r *PostgresRepository) GetTopSourceIPs(ctx context.Context, limit int) ([]models.SourceIPStats, error) {
	var stats []models.SourceIPStats
	err := r.db.WithContext(ctx).
		Table("traffic_logs").
		Select(
			"source_ip",
			"COUNT(*) as count",
			"COALESCE(SUM(bytes_in), 0) as total_bytes_in",
			"COALESCE(SUM(bytes_out), 0) as total_bytes_out",
			"COALESCE(AVG(latency_ms), 0) as avg_latency",
		).
		Group("source_ip").
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
			"COALESCE(SUM(bytes_in), 0) as total_bytes_in",
			"COALESCE(SUM(bytes_out), 0) as total_bytes_out",
			"COALESCE(AVG(latency_ms), 0) as avg_latency",
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

// Close closes the database connection.
func (r *PostgresRepository) Close() error {
	sqlDB, err := r.db.DB()
	if err != nil {
		return fmt.Errorf("failed to get database instance: %w", err)
	}

	return sqlDB.Close()
}
