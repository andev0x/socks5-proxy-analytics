package storage

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/andev0x/socks5-proxy-analytics/internal/config"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestConcurrentMigrations verifies that several processes can migrate an empty database at
// the same time. This is the documented Docker Compose startup path: the proxy and the API
// both start as soon as PostgreSQL is healthy, so both run AutoMigrate concurrently.
//
// The test creates a throwaway database so it never interferes with other integration tests.
func TestConcurrentMigrations(t *testing.T) {
	base := testDatabaseConfig()
	if base == nil {
		t.Skip("TEST_DB_HOST is not set; concurrent migration test skipped")
	}

	admin := openAdminDatabase(t, base)

	name := fmt.Sprintf("socks5_migrate_%d", time.Now().UnixNano())

	if err := admin.Exec("CREATE DATABASE " + name).Error; err != nil {
		t.Skipf("cannot create a throwaway database (needs CREATEDB): %v", err)
	}

	t.Cleanup(func() {
		_ = admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)").Error
	})

	cfg := *base
	cfg.Database.Database = name

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const workers = 4

	var wg sync.WaitGroup

	errs := make(chan error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			db, err := NewDatabase(ctx, &cfg)
			if err != nil {
				errs <- err

				return
			}

			sqlDB, err := db.DB()
			if err != nil {
				errs <- err

				return
			}

			_ = sqlDB.Close()
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent NewDatabase() error = %v", err)
	}
}

func openAdminDatabase(t *testing.T, cfg *config.Config) *gorm.DB {
	t.Helper()

	dsn := databaseURL(cfg)
	// Connect to the maintenance database so the target can be created or dropped.
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}

	parsed.Path = "/postgres"

	db, err := gorm.Open(postgres.Open(parsed.String()), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Skipf("admin connection failed: %v", err)
	}

	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	return db
}
