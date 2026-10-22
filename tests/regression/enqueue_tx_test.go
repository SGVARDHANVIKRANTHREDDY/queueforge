package regression_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"queueforge/internal/config"
	"queueforge/internal/domain"
	"queueforge/internal/postgres"
	"queueforge/internal/testutil"
)

func txRig(t *testing.T) (context.Context, *postgres.DB, *postgres.JobRepo, *domain.Tenant) {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	testutil.IsolatedDatabase(t, cfg)
	ctx := context.Background()
	db, err := postgres.NewDB(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	tn, err := postgres.NewTenantRepo(db).CreateTenant(ctx, postgres.CreateTenantParams{
		Name: "tx-tenant", EnqueueRatePerSecond: 1000, EnqueueBurstCapacity: 1000,
		MaxPendingJobs: 2, MaxConcurrentJobs: 4, MaxSchedules: 5})
	if err != nil {
		t.Fatal(err)
	}
	return ctx, db, postgres.NewJobRepo(db), tn
}

func countJobs(t *testing.T, ctx context.Context, db *postgres.DB) int {
	t.Helper()
	var n int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The job commits together with the caller's own transaction...
func TestEnqueueJobTx_CommitsWithCallerTransaction(t *testing.T) {
	ctx, db, jobs, tn := txRig(t)
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.EnqueueJobTx(ctx, tx, postgres.EnqueueParams{TenantID: tn.ID, TaskType: "echo", Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if got := countJobs(t, ctx, db); got != 0 {
		t.Fatalf("job visible before the caller committed: %d", got)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if got := countJobs(t, ctx, db); got != 1 {
		t.Fatalf("want 1 job after commit, got %d", got)
	}
}

// ...and vanishes if the caller rolls back (this is the whole point of transactional enqueue).
func TestEnqueueJobTx_RollsBackWithCallerTransaction(t *testing.T) {
	ctx, db, jobs, tn := txRig(t)
	tx, _ := db.Pool.Begin(ctx)
	if _, err := jobs.EnqueueJobTx(ctx, tx, postgres.EnqueueParams{TenantID: tn.ID, TaskType: "echo", Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback(ctx)
	if got := countJobs(t, ctx, db); got != 0 {
		t.Fatalf("job survived the caller's rollback: %d", got)
	}
	var usage int
	if err := db.Pool.QueryRow(ctx, `SELECT outstanding_jobs FROM tenant_usage WHERE tenant_id=$1`, tn.ID).Scan(&usage); err != nil || usage != 0 {
		t.Fatalf("quota counter leaked: usage=%d err=%v", usage, err)
	}
}

// A quota rejection must undo only the enqueue (savepoint), not poison the caller's transaction.
func TestEnqueueJobTx_QuotaRejectionLeavesCallerTransactionUsable(t *testing.T) {
	ctx, db, jobs, tn := txRig(t) // MaxPendingJobs = 2
	tx, _ := db.Pool.Begin(ctx)
	defer tx.Rollback(ctx)
	for i := 0; i < 2; i++ {
		if _, err := jobs.EnqueueJobTx(ctx, tx, postgres.EnqueueParams{TenantID: tn.ID, TaskType: "echo", Payload: json.RawMessage(`{}`)}); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
	_, err := jobs.EnqueueJobTx(ctx, tx, postgres.EnqueueParams{TenantID: tn.ID, TaskType: "echo", Payload: json.RawMessage(`{}`)})
	if !errors.Is(err, domain.ErrQuotaExceeded) {
		t.Fatalf("want ErrQuotaExceeded, got %v", err)
	}
	// the caller's transaction must still work after the rejected enqueue
	var one int
	if err := tx.QueryRow(ctx, `SELECT 1`).Scan(&one); err != nil || one != 1 {
		t.Fatalf("caller transaction unusable after rejected enqueue: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if got := countJobs(t, ctx, db); got != 2 {
		t.Fatalf("want exactly the 2 accepted jobs, got %d", got)
	}
}
