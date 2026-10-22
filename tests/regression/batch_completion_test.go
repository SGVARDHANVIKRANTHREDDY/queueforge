package regression_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/config"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/postgres"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/testutil"
	"github.com/google/uuid"
)

// Batch completion must behave exactly like N single completions: every job fenced by its own
// lease, attempts finished, slots released, quota counter decremented by the number completed.
func TestCompleteJobsSuccessBatch_MatchesSingleCompletionSemantics(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	testutil.IsolatedDatabase(t, cfg)
	ctx := t.Context()
	db, err := postgres.NewDB(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	jobs := postgres.NewJobRepo(db)
	tn, err := postgres.NewTenantRepo(db).CreateTenant(ctx, postgres.CreateTenantParams{Name: "bc-" + uuid.NewString()[:5],
		EnqueueRatePerSecond: 1000, EnqueueBurstCapacity: 1000, MaxPendingJobs: 20, MaxConcurrentJobs: 5, MaxSchedules: 1})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if _, err := jobs.EnqueueJob(ctx, postgres.EnqueueParams{TenantID: tn.ID, TaskType: "bc", Payload: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	claimed, err := jobs.ClaimJobs(ctx, uuid.New(), []string{"default"}, []string{"bc"}, time.Minute, 4)
	if err != nil || len(claimed) != 4 {
		t.Fatalf("claim: n=%d err=%v", len(claimed), err)
	}

	items := make([]postgres.SuccessCompletion, len(claimed))
	for i, c := range claimed {
		items[i] = postgres.SuccessCompletion{TenantID: tn.ID, JobID: c.Job.ID, LeaseToken: c.LeaseToken, Result: json.RawMessage(`{"ok":true}`), DurationMs: 7}
	}
	items[2].LeaseToken = uuid.New() // stale token for one job: must be rejected, others accepted

	ok, err := jobs.CompleteJobsSuccessBatch(ctx, items)
	if err != nil {
		t.Fatal(err)
	}
	want := []bool{true, true, false, true}
	for i := range want {
		if ok[i] != want[i] {
			t.Fatalf("item %d: completed=%v want %v", i, ok[i], want[i])
		}
	}

	var succeeded, running int
	_ = db.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status='succeeded'), count(*) FILTER (WHERE status='running') FROM jobs`).Scan(&succeeded, &running)
	if succeeded != 3 || running != 1 {
		t.Fatalf("want 3 succeeded + 1 running, got %d + %d", succeeded, running)
	}
	var busy, unfinished, outstanding int
	_ = db.Pool.QueryRow(ctx, `SELECT count(*) FROM tenant_execution_slots WHERE current_job_id IS NOT NULL`).Scan(&busy)
	_ = db.Pool.QueryRow(ctx, `SELECT count(*) FROM job_attempts WHERE finished_at IS NULL`).Scan(&unfinished)
	_ = db.Pool.QueryRow(ctx, `SELECT outstanding_jobs FROM tenant_usage WHERE tenant_id=$1`, tn.ID).Scan(&outstanding)
	if busy != 1 || unfinished != 1 || outstanding != 1 {
		t.Fatalf("want 1 busy slot, 1 unfinished attempt, outstanding=1; got %d, %d, %d", busy, unfinished, outstanding)
	}
	// the same completions again are all stale now
	again, err := jobs.CompleteJobsSuccessBatch(ctx, items[:2])
	if err != nil || again[0] || again[1] {
		t.Fatalf("replayed completions must be rejected: %v err=%v", again, err)
	}
}
