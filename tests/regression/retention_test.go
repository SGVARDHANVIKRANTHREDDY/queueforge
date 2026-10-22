package regression_test

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/domain"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/postgres"
	"github.com/google/uuid"
)

func (e *env) bulkTerminal(t *testing.T, tn *domain.Tenant, n int, age string) {
	t.Helper()
	_, err := e.db.Pool.Exec(context.Background(), `
		INSERT INTO jobs (id, tenant_id, queue_name, task_type, payload, status, priority, run_at, created_at, updated_at, completed_at, attempts_started, result, trace_id)
		SELECT gen_random_uuid(), $1, 'default', 'reg.retention', '{}', 'succeeded', 5,
		       clock_timestamp() - $3::interval, clock_timestamp() - $3::interval, clock_timestamp(), clock_timestamp() - $3::interval, 1, '{}', ''
		FROM generate_series(1, $2)`, tn.ID, n, age)
	if err != nil {
		t.Fatal(err)
	}
}

func (e *env) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := e.db.Pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Retention must delete exactly the old TERMINAL jobs (with their children) and nothing else.
func TestRetention_DeletesOnlyOldTerminalJobsAndTheirChildren(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	rr := postgres.NewRetentionRepo(e.db)
	tn := e.tenant(t, "ret", 8)
	taskType := "reg.ret." + uuid.NewString()[:5]

	mk := func(complete bool) uuid.UUID { // enqueue, claim, optionally complete -> a job with an attempt row
		id := e.enqueue(t, tn, taskType, 5)
		c, err := e.jobs.ClaimJob(ctx, uuid.New(), []string{"default"}, []string{taskType}, time.Minute)
		if err != nil || c == nil {
			t.Fatalf("claim: %v %v", c, err)
		}
		if complete {
			if err := e.jobs.CompleteJobSuccess(ctx, tn.ID, c.Job.ID, c.LeaseToken, []byte(`{}`), 1); err != nil {
				t.Fatal(err)
			}
		}
		_ = id
		return c.Job.ID
	}
	oldDone1, oldDone2, recentDone := mk(true), mk(true), mk(true)
	stillRunning := mk(false)
	queuedOld := e.enqueue(t, tn, taskType, 5)
	for _, id := range []uuid.UUID{oldDone1, oldDone2} {
		if _, err := e.db.Pool.Exec(ctx, `UPDATE jobs SET completed_at = clock_timestamp() - interval '40 days' WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
	}
	// running and queued jobs are ANCIENT but not terminal: they must survive
	if _, err := e.db.Pool.Exec(ctx, `UPDATE jobs SET created_at = clock_timestamp() - interval '90 days' WHERE id = ANY($1)`, []uuid.UUID{stillRunning, queuedOld}); err != nil {
		t.Fatal(err)
	}
	// a not-yet-expired idempotency key pointing at an expiring job: it must go with the job
	// (otherwise the foreign key would block the delete).
	if _, err := e.db.Pool.Exec(ctx, `INSERT INTO idempotency_keys (tenant_id, idempotency_key, request_hash, job_id, expires_at)
		VALUES ($1, 'k-old', '\x00', $2, clock_timestamp() + interval '1 day')`, tn.ID, oldDone1); err != nil {
		t.Fatalf("idempotency fixture: %v", err)
	}

	res, err := rr.CleanupExpired(ctx, 30*24*time.Hour, 100)
	if err != nil {
		t.Fatal(err)
	}
	if res.TerminalJobsDeleted != 2 {
		t.Fatalf("deleted %d terminal jobs, want exactly 2", res.TerminalJobsDeleted)
	}
	for _, gone := range []uuid.UUID{oldDone1, oldDone2} {
		if e.count(t, `SELECT count(*) FROM jobs WHERE id=$1`, gone) != 0 || e.count(t, `SELECT count(*) FROM job_attempts WHERE job_id=$1`, gone) != 0 {
			t.Fatalf("old terminal job %s (or its attempts) still exists", gone)
		}
	}
	for name, kept := range map[string]uuid.UUID{"recent terminal": recentDone, "running": stillRunning, "queued": queuedOld} {
		if e.count(t, `SELECT count(*) FROM jobs WHERE id=$1`, kept) != 1 {
			t.Fatalf("%s job was deleted by retention", name)
		}
	}
	if e.count(t, `SELECT count(*) FROM job_attempts WHERE job_id=$1`, recentDone) != 1 {
		t.Fatal("a kept job lost its attempt row")
	}
	if e.count(t, `SELECT count(*) FROM idempotency_keys WHERE job_id=$1`, oldDone1) != 0 {
		t.Fatal("the idempotency key of a deleted job survived (dangling reference)")
	}
}

// RETENTION BUG 1: one batch of 100 per hourly tick = at most 2,400 deletions per day, so any
// real workload grew the table without bound. One call must drain a backlog of any size.
func TestRetention_DrainsABacklogLargerThanOneBatch(t *testing.T) {
	e := setup(t)
	tn := e.tenant(t, "ret-backlog", 4)
	e.bulkTerminal(t, tn, 5000, "40 days")
	res, err := postgres.NewRetentionRepo(e.db).CleanupExpired(context.Background(), 30*24*time.Hour, 300)
	if err != nil {
		t.Fatal(err)
	}
	if res.TerminalJobsDeleted != 5000 || e.count(t, `SELECT count(*) FROM jobs WHERE tenant_id=$1`, tn.ID) != 0 {
		t.Fatalf("deleted %d of 5000 expired jobs in one call (batch size 300): retention cannot keep up", res.TerminalJobsDeleted)
	}
}

// RETENTION BUG 2: errors were swallowed (`_, _ = tx.Exec`) inside a transaction, so a failure
// aborted the transaction and silently deleted nothing. A poisoned row must not stall the rest,
// and the failure must be reported.
func TestRetention_PoisonRowIsIsolatedAndReported(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	tn := e.tenant(t, "ret-poison", 4)
	e.bulkTerminal(t, tn, 20, "40 days")
	var poison uuid.UUID
	if err := e.db.Pool.QueryRow(ctx, `SELECT id FROM jobs WHERE tenant_id=$1 ORDER BY id LIMIT 1`, tn.ID).Scan(&poison); err != nil {
		t.Fatal(err)
	}
	fn := "qf_ret_poison_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	if _, err := e.db.Pool.Exec(ctx, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'cannot delete'; END $$ LANGUAGE plpgsql`, fn)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Pool.Exec(ctx, fmt.Sprintf(`CREATE TRIGGER %s BEFORE DELETE ON jobs FOR EACH ROW WHEN (OLD.id = '%s'::uuid) EXECUTE FUNCTION %s()`, fn, poison, fn)); err != nil {
		t.Fatal(err)
	}
	res, err := postgres.NewRetentionRepo(e.db).CleanupExpired(ctx, 30*24*time.Hour, 10)
	if err == nil || !strings.Contains(err.Error(), "cannot delete") {
		t.Fatalf("the failure must be reported, got err=%v", err)
	}
	if res.TerminalJobsDeleted != 19 {
		t.Fatalf("deleted %d, want 19: one poisoned row must not stall the other 19", res.TerminalJobsDeleted)
	}
	if e.count(t, `SELECT count(*) FROM jobs WHERE id=$1`, poison) != 1 {
		t.Fatal("poison row should remain")
	}
}

// RETENTION BUG 3: no index on completed_at, so a pass with nothing to delete (the normal steady
// state) was a sequential scan of the whole table. Asserted as a RATIO against the same call on a
// tiny table, so it is independent of the machine.
func TestRetention_SteadyStateCostIndependentOfTableSize(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	rr := postgres.NewRetentionRepo(e.db)
	tn := e.tenant(t, "ret-scale", 4)
	median := func() float64 {
		var ds []float64
		for i := 0; i < 7; i++ {
			t0 := time.Now()
			if _, err := rr.CleanupExpired(ctx, 30*24*time.Hour, 1000); err != nil {
				t.Fatal(err)
			}
			ds = append(ds, float64(time.Since(t0).Microseconds())/1000)
		}
		sort.Float64s(ds)
		return ds[len(ds)/2]
	}
	e.bulkTerminal(t, tn, 1000, "1 hour") // recent: nothing eligible
	if _, err := e.db.Pool.Exec(ctx, `ANALYZE jobs`); err != nil {
		t.Fatal(err)
	}
	small := median()
	e.bulkTerminal(t, tn, 400000, "1 hour")
	if _, err := e.db.Pool.Exec(ctx, `ANALYZE jobs`); err != nil {
		t.Fatal(err)
	}
	big := median()
	t.Logf("retention pass with nothing to delete: %.2f ms (1k rows) vs %.2f ms (401k rows)", small, big)
	if big > 8 && big > 6*small {
		t.Fatalf("pass cost grew %.1fx (%.2f -> %.2f ms) with table size: it must not scan the whole table", big/small, small, big)
	}
}
