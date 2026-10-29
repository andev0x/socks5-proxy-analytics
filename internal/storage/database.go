// Package storage provides database connection and repository implementations.
package storage

import (
	"context"
	"database/sql"
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

// migrationLockKey is an application-specific PostgreSQL advisory lock key. It serializes
// schema migration across processes (the proxy and API start together in Compose).
const migrationLockKey int64 = 0x534f434b355f4d49 // "SOCK5_MI"

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

	if err := migrate(timeoutCtx, db); err != nil {
		_ = sqlDB.Close()

		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	return db, nil
}

// migrate applies the schema while holding a PostgreSQL advisory lock. Concurrent first-time
// migrations (for example, proxy and API starting against an empty database) otherwise fail
// with "duplicate key value violates unique constraint pg_type_typname_nsp_index" or
// "relation already exists" and leave one process in a crash loop.
func migrate(ctx context.Context, db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("failed to access database connection pool: %w", err)
	}

	// The advisory lock is session-scoped, so it must be taken and released on one
	// dedicated connection while the migration itself uses the normal pool.
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("failed to reserve a migration connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if err := acquireMigrationLock(ctx, conn); err != nil {
		return err
	}

	//nolint:contextcheck // unlock must not inherit an already-canceled caller context
	defer func() {
		// Use a fresh context: the caller's context may already be canceled.
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, _ = conn.ExecContext(unlockCtx, "SELECT pg_advisory_unlock($1)", migrationLockKey)
	}()

	return db.WithContext(ctx).AutoMigrate(&models.TrafficLog{})
}

func acquireMigrationLock(ctx context.Context, conn *sql.Conn) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		var acquired bool
		if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", migrationLockKey).Scan(&acquired); err != nil {
			return fmt.Errorf("failed to acquire migration lock: %w", err)
		}

		if acquired {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for the migration lock: %w", ctx.Err())
		case <-ticker.C:
		}
	}
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
