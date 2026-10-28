// Package storage provides database connection and repository implementations.
package storage

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"

	"github.com/andev0x/socks5-proxy-analytics/internal/config"
	"github.com/andev0x/socks5-proxy-analytics/internal/models"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// NewDatabase creates a PostgreSQL connection, verifies it and applies the Phase 1 schema.
// Connection settings are passed as a URL so credentials with special characters are safe.
func NewDatabase(ctx context.Context, cfg *config.Config) (*gorm.DB, error) {
	dsn := databaseURL(cfg)

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to access database connection pool: %w", err)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.Database.ConnectTimeoutMs)*time.Millisecond)
	defer cancel()

	if err := sqlDB.PingContext(timeoutCtx); err != nil {
		_ = sqlDB.Close()

		return nil, fmt.Errorf("database ping failed: %w", err)
	}

	sqlDB.SetMaxOpenConns(cfg.Database.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.Database.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)

	if err := db.AutoMigrate(&models.TrafficLog{}); err != nil {
		_ = sqlDB.Close()

		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	return db, nil
}

func databaseURL(cfg *config.Config) string {
	host := net.JoinHostPort(cfg.Database.Host, strconv.Itoa(cfg.Database.Port))
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(cfg.Database.User, cfg.Database.Password),
		Host:   host,
		Path:   "/" + cfg.Database.Database,
	}

	query := u.Query()
	query.Set("sslmode", cfg.Database.SSLMode)
	query.Set("connect_timeout", strconv.Itoa(max(1, cfg.Database.ConnectTimeoutMs/1000)))
	u.RawQuery = query.Encode()

	return u.String()
}
