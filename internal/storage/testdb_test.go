package storage

import (
	"fmt"
	"testing"
	"time"

	"github.com/andev0x/socks5-proxy-analytics/internal/config"
)

// isolatedDatabaseConfig creates a throwaway database for one test so that integration tests
// in different packages (which `go test ./...` runs in parallel) cannot truncate or read each
// other's rows. It skips when the configured role cannot create databases.
func isolatedDatabaseConfig(t *testing.T, base *config.Config) *config.Config {
	t.Helper()

	admin := openAdminDatabase(t, base)

	name := fmt.Sprintf("socks5_it_%d", time.Now().UnixNano())

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
