package proxy

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/andev0x/socks5-proxy-analytics/internal/config"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// isolatedDatabaseConfig creates a throwaway database for one test so that integration tests
// in different packages (which `go test ./...` runs in parallel) cannot truncate or read each
// other's rows. It skips when the configured role cannot create databases.
func isolatedDatabaseConfig(t *testing.T, base *config.Config) *config.Config {
	t.Helper()

	admin, err := gorm.Open(postgres.Open(adminDSN(base)), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Skipf("cannot connect to the maintenance database: %v", err)
	}

	t.Cleanup(func() {
		if sqlDB, dbErr := admin.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	name := fmt.Sprintf("socks5_proxy_it_%d", time.Now().UnixNano())

	if err := admin.Exec("CREATE DATABASE " + name).Error; err != nil {
		t.Skipf("cannot create an isolated test database (needs CREATEDB): %v", err)
	}

	t.Cleanup(func() {
		_ = admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)").Error
	})

	cfg := *base
	cfg.Database.Database = name

	return &cfg
}

func adminDSN(cfg *config.Config) string {
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(cfg.Database.User, cfg.Database.Password),
		Host:   net.JoinHostPort(cfg.Database.Host, strconv.Itoa(cfg.Database.Port)),
		Path:   "/postgres",
	}

	query := u.Query()
	query.Set("sslmode", cfg.Database.SSLMode)
	query.Set("connect_timeout", "5")
	u.RawQuery = query.Encode()

	return u.String()
}
