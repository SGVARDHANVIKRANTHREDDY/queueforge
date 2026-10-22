// Package testutil holds helpers shared by the test suites.
package testutil

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/config"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/postgres"
	"github.com/google/uuid"
)

// IsolatedDatabase creates a brand-new, fully migrated database for ONE test (or one test
// process) and drops it when the test ends. It returns the new connection URL and also writes it
// into cfg.DatabaseURL so everything built from cfg afterwards uses the private database.
//
// Why: several operations in this system are deliberately GLOBAL (the scheduler picks up every due
// schedule, recovery sweeps every expired lease, workers claim any runnable job they can see).
// Tests sharing one database therefore leak into each other: leftover schedules, jobs and leases
// from a failed run made later runs fail in confusing ways. A private database per test removes
// the whole class of problem and makes `go test ./...` safe to run in parallel.
func IsolatedDatabase(t testing.TB, cfg *config.Config) string {
	t.Helper()
	ctx := context.Background()

	admin, err := postgres.NewDB(ctx, cfg)
	if err != nil {
		t.Fatalf("connect to admin database: %v", err)
	}
	name := "qf_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	if _, err := admin.Pool.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		admin.Close()
		t.Fatalf("create isolated database: %v", err)
	}
	u, err := url.Parse(cfg.DatabaseURL)
	if err != nil {
		admin.Close()
		t.Fatalf("parse database url: %v", err)
	}
	u.Path = "/" + name
	if err := postgres.RunMigrations(u.String()); err != nil {
		admin.Close()
		t.Fatalf("migrate isolated database: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Pool.Exec(context.Background(), fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", name))
		admin.Close()
	})
	cfg.DatabaseURL = u.String()
	return u.String()
}
