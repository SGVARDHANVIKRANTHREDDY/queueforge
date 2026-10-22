package chaos_test

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/config"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/testutil"
)

// privateDatabase creates, migrates and (on cleanup) drops a throwaway database and returns its
// URL. Tests that spawn real worker processes MUST use it: those processes run the scheduler and
// claim every runnable job they can see, so they cannot share a database with parallel tests.
func privateDatabase(t *testing.T, cfg *config.Config) string {
	t.Helper()
	return testutil.IsolatedDatabase(t, cfg)
}

// buildWorkerBinary compiles the real worker once per test and returns its path.
func buildWorkerBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "qf-worker")
	build := exec.Command("go", "build", "-o", bin, "./cmd/worker")
	build.Dir = "../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build worker: %v\n%s", err, out)
	}
	return bin
}
