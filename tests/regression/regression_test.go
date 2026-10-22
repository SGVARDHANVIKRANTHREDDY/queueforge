// Package regression_test holds tests that each reproduce one real bug found in code review.
// Every test here FAILED against the original code before the corresponding fix.
package regression_test

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/config"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/domain"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/postgres"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/testutil"
	"github.com/google/uuid"
)

type env struct {
	cfg  *config.Config
	db   *postgres.DB
	jobs *postgres.JobRepo
	ten  *postgres.TenantRepo
}

func setup(t *testing.T) *env {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	testutil.IsolatedDatabase(t, cfg)
	db, err := postgres.NewDB(context.Background(), cfg)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(db.Close)
	return &env{cfg: cfg, db: db, jobs: postgres.NewJobRepo(db), ten: postgres.NewTenantRepo(db)}
}

func (e *env) tenant(t *testing.T, name string, slots int) *domain.Tenant {
	t.Helper()
	tn, err := e.ten.CreateTenant(context.Background(), postgres.CreateTenantParams{
		Name: name + "-" + uuid.NewString()[:6], EnqueueRatePerSecond: 1000, EnqueueBurstCapacity: 1000,
		MaxPendingJobs: 1000, MaxConcurrentJobs: slots,
	})
	if err != nil {
		t.Fatal(err)
	}
	return tn
}

func (e *env) enqueue(t *testing.T, tn *domain.Tenant, taskType string, prio int16) uuid.UUID {
	t.Helper()
	r, err := e.jobs.EnqueueJob(context.Background(), postgres.EnqueueParams{
		TenantID: tn.ID, TaskType: taskType, QueueName: "default", Payload: []byte(`{}`),
		Priority: prio, MaxAttempts: 5, TimeoutSeconds: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	return r.Job.ID
}

// BUG 2: ClaimJob picked the globally best-priority job, then tried to lock one of that
// tenant's slots with SKIP LOCKED. If the slot was momentarily locked by another worker the
// claim returned "nothing to do" even though other tenants had runnable jobs, so workers idled.
func TestClaim_DoesNotIdleWhenTopTenantSlotIsContended(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	taskType := "reg.claim." + uuid.NewString()[:8]

	tenantA := e.tenant(t, "A-hot", 1)
	tenantB := e.tenant(t, "B-other", 1)
	e.enqueue(t, tenantA, taskType, 9) // top priority, but its only slot is about to be locked
	jobB := e.enqueue(t, tenantB, taskType, 5)

	// Simulate "another worker is mid-claim on tenant A's slot".
	tx, err := e.db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT 1 FROM tenant_execution_slots WHERE tenant_id=$1 FOR UPDATE`, tenantA.ID); err != nil {
		t.Fatal(err)
	}

	res, err := e.jobs.ClaimJob(ctx, uuid.New(), []string{"default"}, []string{taskType}, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res == nil {
		t.Fatalf("worker idled although tenant B had a runnable job (head-of-line blocking on tenant A's slot)")
	}
	if res.Job.ID != jobB {
		t.Fatalf("expected tenant B's job %s, got %s", jobB, res.Job.ID)
	}
}

// BUG 1: RecoverExpiredLeases did all recoveries in ONE transaction and ignored statement
// errors. In Postgres the first failed statement aborts the whole transaction, so one bad row
// rolled back every recovery in the batch, forever (a poison pill that stalls crash recovery).
func TestRecovery_PoisonJobDoesNotBlockHealthyJobs(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	taskType := "reg.recover." + uuid.NewString()[:8]
	tn := e.tenant(t, "recover", 4)

	e.enqueue(t, tn, taskType, 5)
	e.enqueue(t, tn, taskType, 5)
	worker := uuid.New()
	var claimed []uuid.UUID
	for i := 0; i < 2; i++ {
		r, err := e.jobs.ClaimJob(ctx, worker, []string{"default"}, []string{taskType}, 30*time.Second)
		if err != nil || r == nil {
			t.Fatalf("claim %d: %v %v", i, r, err)
		}
		claimed = append(claimed, r.Job.ID)
	}
	poison, healthy := claimed[0], claimed[1]

	// Both leases expired; the poison job's lease expired FIRST so it sorts first in the batch.
	mustExec(t, e.db, `UPDATE jobs SET lease_expires_at = clock_timestamp() - interval '10 seconds' WHERE id=$1`, poison)
	mustExec(t, e.db, `UPDATE jobs SET lease_expires_at = clock_timestamp() - interval '5 seconds' WHERE id=$1`, healthy)

	// Make any attempt to requeue the poison job fail inside Postgres.
	fn := "qf_poison_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	mustExec(t, e.db, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'poison row'; END $$ LANGUAGE plpgsql`, fn))
	mustExec(t, e.db, fmt.Sprintf(`CREATE TRIGGER %s BEFORE UPDATE ON jobs FOR EACH ROW WHEN (OLD.id = '%s'::uuid) EXECUTE FUNCTION %s()`, fn, poison, fn))
	t.Cleanup(func() {
		_, _ = e.db.Pool.Exec(context.Background(), fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON jobs`, fn))
		_, _ = e.db.Pool.Exec(context.Background(), fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, fn))
		_, _ = e.db.Pool.Exec(context.Background(), `UPDATE jobs SET status='queued', lease_token=NULL, worker_id=NULL, lease_expires_at=NULL WHERE id=$1`, poison)
	})

	n, _ := e.jobs.RecoverExpiredLeases(ctx, 100, domain.NewBackoffPolicy(time.Millisecond, 2*time.Millisecond, nil))
	if n < 1 {
		t.Fatalf("recovered %d jobs; the healthy job was blocked by the poison job", n)
	}

	var status string
	if err := e.db.Pool.QueryRow(ctx, `SELECT status FROM jobs WHERE id=$1`, healthy).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "queued" {
		t.Fatalf("healthy job status = %q, want queued (recovered)", status)
	}
}

func mustExec(t *testing.T, db *postgres.DB, sql string, args ...any) {
	t.Helper()
	if _, err := db.Pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

// BUG 4: ClaimJob never selected run_at, so Job.RunAt was the zero time and the worker's
// queue-wait metric observed time.Since(year 1) ~ 2000 years on every job.
func TestClaim_ReturnsRealRunAt(t *testing.T) {
	e := setup(t)
	taskType := "reg.runat." + uuid.NewString()[:8]
	tn := e.tenant(t, "runat", 1)
	e.enqueue(t, tn, taskType, 5)

	res, err := e.jobs.ClaimJob(context.Background(), uuid.New(), []string{"default"}, []string{taskType}, 30*time.Second)
	if err != nil || res == nil {
		t.Fatalf("claim: %v %v", res, err)
	}
	if res.Job.RunAt.IsZero() {
		t.Fatal("claimed job has zero RunAt; queue-wait metric would be garbage")
	}
	if wait := time.Since(res.Job.RunAt); wait < 0 || wait > time.Minute {
		t.Fatalf("implausible queue wait %v", wait)
	}
}

// BUG 6 (clock skew): enqueue stamped run_at/created_at and refilled rate-limit tokens using the
// API server's clock, while ClaimJob compares run_at with the database clock. An API clock ahead
// of the database hides new jobs until the database catches up; a clock behind runs them early;
// skewed token refill breaks rate limiting. Go clocks cannot be skewed from inside a test, so this
// test enforces the design rule structurally: queue logic in internal/postgres must never read
// the application clock (see ADR-014).
func TestNoApplicationClockInPostgresLayer(t *testing.T) {
	files, err := filepath.Glob("../../internal/postgres/*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("glob: %v (%d files)", err, len(files))
	}
	banned := regexp.MustCompile(`\btime\.(Now|Since|Until)\(`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if banned.MatchString(line) && !strings.Contains(line, "//nolint:appclock") {
				t.Errorf("%s:%d uses the application clock: %s", filepath.Base(f), i+1, strings.TrimSpace(line))
			}
		}
	}
}

// The enqueue path must stamp run_at from the database clock: a job enqueued "now" must be
// claimable immediately, and its returned timestamps must agree with the database.
func TestEnqueue_TimestampsComeFromDatabaseClock(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	taskType := "reg.dbclock." + uuid.NewString()[:8]
	tn := e.tenant(t, "dbclock", 1)

	r, err := e.jobs.EnqueueJob(ctx, postgres.EnqueueParams{
		TenantID: tn.ID, TaskType: taskType, QueueName: "default", Payload: []byte(`{}`),
		Priority: 5, MaxAttempts: 3, TimeoutSeconds: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	var dbRunAt, dbCreated, dbNow time.Time
	if err := e.db.Pool.QueryRow(ctx, `SELECT run_at, created_at, clock_timestamp() FROM jobs WHERE id=$1`, r.Job.ID).Scan(&dbRunAt, &dbCreated, &dbNow); err != nil {
		t.Fatal(err)
	}
	if !r.Job.RunAt.Equal(dbRunAt) || !r.Job.CreatedAt.Equal(dbCreated) {
		t.Fatalf("returned timestamps differ from stored: %v/%v vs %v/%v", r.Job.RunAt, r.Job.CreatedAt, dbRunAt, dbCreated)
	}
	if dbRunAt.After(dbNow) {
		t.Fatalf("run_at %v is in the database's future (%v): job would be invisible", dbRunAt, dbNow)
	}
	if res, err := e.jobs.ClaimJob(ctx, uuid.New(), []string{"default"}, []string{taskType}, 30*time.Second); err != nil || res == nil {
		t.Fatalf("freshly enqueued job must be claimable immediately: %v %v", res, err)
	} else if res.QueueWaitSeconds < 0 || res.QueueWaitSeconds > 5 {
		t.Fatalf("implausible DB-measured queue wait %v", res.QueueWaitSeconds)
	}
}

// bulkQueued inserts n queued jobs of one type directly (fast path for scale tests).
func (e *env) bulkQueued(t *testing.T, tn *domain.Tenant, taskType string, prio int, n int, delay string) {
	t.Helper()
	_, err := e.db.Pool.Exec(context.Background(), `
		INSERT INTO jobs (id, tenant_id, queue_name, task_type, payload, status, priority, run_at, created_at, updated_at, trace_id)
		SELECT gen_random_uuid(), $1, 'default', $2, '{}', 'queued', $3, clock_timestamp() + $5::interval, clock_timestamp(), clock_timestamp(), ''
		FROM generate_series(1, $4)`, tn.ID, taskType, prio, n, delay)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Pool.Exec(context.Background(), `ANALYZE jobs`); err != nil {
		t.Fatal(err)
	}
}

func medianClaimMs(t *testing.T, e *env, taskType string, tries int) float64 {
	t.Helper()
	var ds []float64
	for i := 0; i < tries; i++ {
		t0 := time.Now()
		res, err := e.jobs.ClaimJob(context.Background(), uuid.New(), []string{"default"}, []string{taskType}, time.Minute)
		d := float64(time.Since(t0).Microseconds()) / 1000
		if err != nil || res == nil {
			t.Fatalf("claim %d: %v %v", i, res, err)
		}
		ds = append(ds, d)
	}
	sort.Float64s(ds)
	return ds[len(ds)/2]
}

// SCALE BUG 1: claim cost was O(number of queued jobs of OTHER task types ahead in priority order)
// (measured 7.6 ms @10k, 132 ms @100k, 653 ms @400k). One worker pool's backlog slowed every other pool.
//
// The assertion is a RATIO (claim cost with the backlog vs without it) rather than an absolute time,
// so it is independent of the machine: with the bug the ratio is >> 10; a flat claim is ~1.
func TestClaim_CostIndependentOfForeignTaskTypeBacklog(t *testing.T) {
	e := setup(t)
	mine, foreign := "reg.mine."+uuid.NewString()[:6], "reg.foreign."+uuid.NewString()[:6]
	tn := e.tenant(t, "scale-foreign", 64)
	e.bulkQueued(t, tn, mine, 5, 200, "0 seconds")
	base := medianClaimMs(t, e, mine, 11)
	e.bulkQueued(t, tn, foreign, 9, 150000, "0 seconds") // higher priority, but not ours
	loaded := medianClaimMs(t, e, mine, 11)
	t.Logf("claim median: %.2f ms without backlog, %.2f ms with 150k foreign-type jobs ahead", base, loaded)
	if loaded > 5 && loaded > 8*base {
		t.Fatalf("claim cost grew %.1fx (%.2f -> %.2f ms) with an unrelated backlog: it must be flat", loaded/base, base, loaded)
	}
}

// SCALE BUG 2: same, for high-priority jobs that are DELAYED (run_at in the future): they sorted ahead
// of due jobs and every claim walked all of them.
func TestClaim_CostIndependentOfDelayedBacklog(t *testing.T) {
	e := setup(t)
	taskType := "reg.delayed." + uuid.NewString()[:6]
	tn := e.tenant(t, "scale-delayed", 64)
	e.bulkQueued(t, tn, taskType, 5, 200, "0 seconds")
	base := medianClaimMs(t, e, taskType, 11)
	e.bulkQueued(t, tn, taskType, 9, 150000, "1 day")
	loaded := medianClaimMs(t, e, taskType, 11)
	t.Logf("claim median: %.2f ms without backlog, %.2f ms with 150k delayed high-priority jobs ahead", base, loaded)
	if loaded > 5 && loaded > 8*base {
		t.Fatalf("claim cost grew %.1fx (%.2f -> %.2f ms) with a delayed backlog: it must be flat", loaded/base, base, loaded)
	}
}

// A tenant that has filled all its execution slots (and has a huge queue) must not stop other
// tenants' jobs from being claimed.
func TestClaim_SaturatedTenantDoesNotStarveOthers(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	taskType := "reg.noisy." + uuid.NewString()[:6]
	noisy, quiet := e.tenant(t, "noisy", 1), e.tenant(t, "quiet", 2)
	e.bulkQueued(t, noisy, taskType, 5, 5000, "0 seconds")
	time.Sleep(5 * time.Millisecond)
	for i := 0; i < 3; i++ {
		e.enqueue(t, quiet, taskType, 5) // newer than every noisy job
	}
	w := uuid.New()
	first, err := e.jobs.ClaimJobs(ctx, w, []string{"default"}, []string{taskType}, time.Minute, 4)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[uuid.UUID]int{}
	for _, c := range first {
		counts[c.Job.TenantID]++
	}
	if counts[noisy.ID] != 1 || counts[quiet.ID] != 2 {
		t.Fatalf("slot limits violated: noisy=%d (max 1) quiet=%d (max 2), total %d", counts[noisy.ID], counts[quiet.ID], len(first))
	}
	// Noisy tenant's single slot is now busy; the third quiet job must still be reachable
	// after completing one quiet job, without being blocked by the 4,999 noisy jobs ahead of it.
	var quietClaim *postgres.ClaimResult
	for _, c := range first {
		if c.Job.TenantID == quiet.ID {
			quietClaim = c
			break
		}
	}
	if err := e.jobs.CompleteJobSuccess(ctx, quiet.ID, quietClaim.Job.ID, quietClaim.LeaseToken, []byte(`{}`), 1); err != nil {
		t.Fatal(err)
	}
	next, err := e.jobs.ClaimJobs(ctx, w, []string{"default"}, []string{taskType}, time.Minute, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(next) != 1 || next[0].Job.TenantID != quiet.ID {
		t.Fatalf("expected exactly the remaining quiet job, got %d jobs (first tenant %v)", len(next), func() any {
			if len(next) > 0 {
				return next[0].Job.TenantID
			}
			return nil
		}())
	}
}

// Claim order must be: higher priority first, then oldest first, across batches and tenants.
func TestClaim_PriorityThenFIFOAcrossBatches(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	taskType := "reg.order." + uuid.NewString()[:6]
	a, b := e.tenant(t, "oa", 50), e.tenant(t, "ob", 50)
	rnd := rand.New(rand.NewSource(42))
	type meta struct {
		prio    int16
		created time.Time
	}
	enq := map[uuid.UUID]meta{}
	for i := 0; i < 120; i++ {
		tn := a
		if i%2 == 1 {
			tn = b
		}
		p := int16(rnd.Intn(10))
		id := e.enqueue(t, tn, taskType, p)
		var created time.Time
		_ = e.db.Pool.QueryRow(ctx, `SELECT created_at FROM jobs WHERE id=$1`, id).Scan(&created)
		enq[id] = meta{p, created}
	}
	var order []meta
	w := uuid.New()
	for len(order) < 120 {
		got, err := e.jobs.ClaimJobs(ctx, w, []string{"default"}, []string{taskType}, time.Minute, 1+rnd.Intn(7))
		if err != nil {
			t.Fatal(err)
		}
		if len(got) == 0 {
			t.Fatalf("ran dry after %d of 120 claims", len(order))
		}
		for _, c := range got { // within a batch the result must already be ordered
			order = append(order, enq[c.Job.ID])
			if err := e.jobs.CompleteJobSuccess(ctx, c.Job.TenantID, c.Job.ID, c.LeaseToken, []byte(`{}`), 1); err != nil {
				t.Fatal(err) // free the slot so the next claim can proceed
			}
		}
	}
	for i := 1; i < len(order); i++ {
		prev, cur := order[i-1], order[i]
		if cur.prio > prev.prio {
			t.Fatalf("claim %d: priority %d came after %d (higher priority must come first)", i, cur.prio, prev.prio)
		}
		if cur.prio == prev.prio && cur.created.Before(prev.created) {
			t.Fatalf("claim %d: same priority %d but a NEWER job was claimed before an older one", i, cur.prio)
		}
	}
}

// Concurrent workers hammering the same queue must never lease the same job twice and must never
// exceed a tenant's slot limit. This is the race that a sequential test cannot reach: a worker
// picks a candidate, another worker claims it, and the first must NOT claim it again after
// acquiring the row lock (the status re-check under the lock is what prevents that).
func TestClaim_ConcurrentWorkersNeverDoubleClaim(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	taskType := "reg.race." + uuid.NewString()[:6]
	tenants := []*domain.Tenant{e.tenant(t, "r1", 40), e.tenant(t, "r2", 40), e.tenant(t, "r3", 40)}
	const total = 600
	for i := 0; i < total; i++ {
		e.enqueue(t, tenants[i%3], taskType, int16(i%3))
	}
	var mu sync.Mutex
	seen := map[uuid.UUID]int{}
	perTenantRunning := map[uuid.UUID]int{}
	var wg sync.WaitGroup
	for w := 0; w < 12; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := uuid.New()
			for idle := 0; idle < 6; {
				got, err := e.jobs.ClaimJobs(ctx, id, []string{"default"}, []string{taskType}, time.Minute, 5)
				if err != nil {
					t.Errorf("claim: %v", err)
					return
				}
				if len(got) == 0 {
					idle++
					time.Sleep(5 * time.Millisecond)
					continue
				}
				idle = 0
				mu.Lock()
				for _, c := range got {
					seen[c.Job.ID]++
					perTenantRunning[c.Job.TenantID]++
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	dups := 0
	for _, n := range seen {
		if n > 1 {
			dups++
		}
	}
	if dups != 0 {
		t.Fatalf("%d jobs were leased more than once by concurrent workers", dups)
	}
	for _, tn := range tenants {
		if perTenantRunning[tn.ID] > 40 {
			t.Fatalf("tenant %s has %d jobs leased, exceeding its limit of 40", tn.Name, perTenantRunning[tn.ID])
		}
	}
	if len(seen) != 3*40 {
		t.Fatalf("leased %d distinct jobs, want exactly 120 (3 tenants x 40 slots, none completed)", len(seen))
	}
}
