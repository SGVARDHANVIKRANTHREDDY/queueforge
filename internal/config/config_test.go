package config

import "testing"

// BUG 5: the env var was read as "QF_DATABASE_MAX_CONns" (typo), so the documented
// QF_DATABASE_MAX_CONNS was silently ignored.
func TestLoad_DatabaseMaxConnsEnvVarIsHonoured(t *testing.T) {
	t.Setenv("QF_DATABASE_MAX_CONNS", "7")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DatabaseMaxConns != 7 {
		t.Fatalf("DatabaseMaxConns = %d, want 7", cfg.DatabaseMaxConns)
	}
}
