package load_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/config"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/domain"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/postgres"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/testutil"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/worker"
	"github.com/google/uuid"
)

// These benchmarks are deliberately honest:
//   - handlers are no-ops, so any throughput ceiling is the QUEUE's, not the handler's
//   - every worker has its own connection pool, like separate processes would
//   - every job's payload index is checked so duplicates / losses fail the run
//
// Tune with QF_BENCH_JOBS, QF_BENCH_WORKERS, QF_BENCH_TENANTS, QF_BENCH_IDLE_SAMPLES.
// Results are printed as "BENCH_RESULT key=value" lines so they can be scraped.

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func pct(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	i := int(float64(len(sorted)-1) * p)
	return sorted[i]
}

func newDB(t *testing.T, cfg *config.Config, maxConns int) *postgres.DB {
	t.Helper()
	c := *cfg
	c.DatabaseMaxConns = maxConns
	c.DatabaseMinConns = 1
	db, err := postgres.NewDB(context.Background(), &c)
	if err != nil {
		t.Fatalf("db connect: %v", err)
	}
	return db
}

func makeTenants(t *testing.T, repo *postgres.TenantRepo, n, concurrent int) []*domain.Tenant {
	t.Helper()
	out := make([]*domain.Tenant, 0, n)
	for i := 0; i < n; i++ {
		tn, err := repo.CreateTenant(context.Background(), postgres.CreateTenantParams{
			Name:                 fmt.Sprintf("bench-%d-%s", i, uuid.NewString()[:6]),
			EnqueueRatePerSecond: 1_000_000,
			EnqueueBurstCapacity: 1_000_000,
			MaxPendingJobs:       1_000_000,
			MaxConcurrentJobs:    concurrent,
		})
		if err != nil {
			t.Fatalf("create tenant: %v", err)
		}
		out = append(out, tn)
	}
	return out
}

// TestBenchmark_DrainThroughput measures how fast N workers can drain a pre-filled backlog
// of no-op jobs, and verifies every job ran exactly once.
func TestBenchmark_DrainThroughput(t *testing.T) {
	if testing.Short() {
		t.Skip("load benchmark")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("QF_BENCH_SHARED_DB") == "" {
		testutil.IsolatedDatabase(t, cfg)
	}
	nJobs := envInt("QF_BENCH_JOBS", 2000)
	nWorkers := envInt("QF_BENCH_WORKERS", 4)
	nTenants := envInt("QF_BENCH_TENANTS", 4)
	taskType := "bench.noop." + uuid.NewString()[:8]

	adminDB := newDB(t, cfg, 32)
	defer adminDB.Close()
	jobRepo := postgres.NewJobRepo(adminDB)
	tenants := makeTenants(t, postgres.NewTenantRepo(adminDB), nTenants, 32)

	// ---- Phase 1: enqueue ----
	lat := make([]time.Duration, nJobs)
	var next atomic.Int64
	var failed atomic.Int64
	var wg sync.WaitGroup
	enqStart := time.Now()
	for c := 0; c < 16; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= nJobs {
					return
				}
				t0 := time.Now()
				_, err := jobRepo.EnqueueJob(context.Background(), postgres.EnqueueParams{
					TenantID:       tenants[i%nTenants].ID,
					TaskType:       taskType,
					QueueName:      "default",
					Payload:        []byte(fmt.Sprintf(`{"idx":%d}`, i)),
					Priority:       5,
					MaxAttempts:    3,
					TimeoutSeconds: 30,
				})
				lat[i] = time.Since(t0)
				if err != nil {
					failed.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	enqDur := time.Since(enqStart)
	if failed.Load() != 0 {
		t.Fatalf("%d enqueues failed", failed.Load())
	}
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	t.Logf("BENCH_RESULT enqueue_jobs=%d", nJobs)
	t.Logf("BENCH_RESULT enqueue_per_sec=%.0f", float64(nJobs)/enqDur.Seconds())
	t.Logf("BENCH_RESULT enqueue_p50_ms=%.2f", float64(pct(lat, .50).Microseconds())/1000)
	t.Logf("BENCH_RESULT enqueue_p95_ms=%.2f", float64(pct(lat, .95).Microseconds())/1000)
	t.Logf("BENCH_RESULT enqueue_p99_ms=%.2f", float64(pct(lat, .99).Microseconds())/1000)

	// A freshly created database has NO planner statistics until autovacuum's analyze runs (about a
	// minute later), so the planner would cost the 2,000-row backlog as nearly empty and pick
	// seq scans. A real deployment is long-lived and analysed; emulate that, unless disabled
	// (QF_BENCH_ANALYZE=0) to measure the cold-statistics behaviour.
	if os.Getenv("QF_BENCH_ANALYZE") != "0" {
		if _, err := adminDB.Pool.Exec(context.Background(), `ANALYZE`); err != nil {
			t.Fatalf("analyze: %v", err)
		}
	}

	// ---- Phase 2: drain with N workers ----
	seen := make([]atomic.Int32, nJobs)
	var done atomic.Int64
	handler := func(ctx context.Context, payload json.RawMessage) (json.RawMessage, error) {
		var p struct {
			Idx int `json:"idx"`
		}
		if err := json.Unmarshal(payload, &p); err == nil && p.Idx >= 0 && p.Idx < nJobs {
			seen[p.Idx].Add(1)
		}
		done.Add(1)
		return payload, nil
	}

	workers := make([]*worker.Worker, 0, nWorkers)
	dbs := make([]*postgres.DB, 0, nWorkers)
	for i := 0; i < nWorkers; i++ {
		db := newDB(t, cfg, 10)
		dbs = append(dbs, db)
		w := worker.NewWorker(cfg, postgres.NewJobRepo(db), postgres.NewWorkerRepo(db), nil)
		if err := w.Register(taskType, handler); err != nil {
			t.Fatal(err)
		}
		workers = append(workers, w)
	}
	start := time.Now()
	for _, w := range workers {
		w.Start()
	}
	deadline := time.After(time.Duration(envInt("QF_BENCH_TIMEOUT_SEC", 240)) * time.Second)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
wait:
	for {
		select {
		case <-deadline:
			t.Fatalf("timeout: only %d/%d jobs ran", done.Load(), nJobs)
		case <-tick.C:
			if done.Load() >= int64(nJobs) {
				break wait
			}
		}
	}
	drain := time.Since(start)
	for _, w := range workers {
		w.Stop(5 * time.Second)
	}
	for _, db := range dbs {
		db.Close()
	}

	// ---- Correctness: exactly once, nothing leaked ----
	dups, missing := 0, 0
	for i := range seen {
		switch c := seen[i].Load(); {
		case c == 0:
			missing++
		case c > 1:
			dups++
		}
	}
	var succeeded int
	_ = adminDB.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM jobs WHERE task_type=$1 AND status='succeeded'`, taskType).Scan(&succeeded)
	var leaked int
	_ = adminDB.Pool.QueryRow(context.Background(), `
		SELECT count(*) FROM tenant_execution_slots s
		WHERE current_job_id IS NOT NULL AND tenant_id = ANY($1::uuid[])`, tenantIDs(tenants)).Scan(&leaked)

	t.Logf("BENCH_RESULT workers=%d", nWorkers)
	t.Logf("BENCH_RESULT drain_seconds=%.2f", drain.Seconds())
	t.Logf("BENCH_RESULT drain_jobs_per_sec=%.0f", float64(nJobs)/drain.Seconds())
	t.Logf("BENCH_RESULT duplicates=%d missing=%d leaked_slots=%d succeeded_in_db=%d", dups, missing, leaked, succeeded)
	if dups != 0 || missing != 0 || leaked != 0 || succeeded != nJobs {
		t.Fatalf("correctness violation: dups=%d missing=%d leaked=%d succeeded=%d/%d", dups, missing, leaked, succeeded, nJobs)
	}
}

func tenantIDs(ts []*domain.Tenant) []uuid.UUID {
	ids := make([]uuid.UUID, len(ts))
	for i, t := range ts {
		ids[i] = t.ID
	}
	return ids
}

// TestBenchmark_IdleLatency measures enqueue -> succeeded latency on an otherwise idle system.
// This is what a user feels: "I submitted a job, how long until it ran?"
func TestBenchmark_IdleLatency(t *testing.T) {
	if testing.Short() {
		t.Skip("load benchmark")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	testutil.IsolatedDatabase(t, cfg)
	samples := envInt("QF_BENCH_IDLE_SAMPLES", 150)
	taskType := "bench.idle." + uuid.NewString()[:8]

	adminDB := newDB(t, cfg, 8)
	defer adminDB.Close()
	jobRepo := postgres.NewJobRepo(adminDB)
	tenant := makeTenants(t, postgres.NewTenantRepo(adminDB), 1, 8)[0]

	var workers []*worker.Worker
	var dbs []*postgres.DB
	for i := 0; i < 2; i++ {
		db := newDB(t, cfg, 6)
		dbs = append(dbs, db)
		w := worker.NewWorker(cfg, postgres.NewJobRepo(db), postgres.NewWorkerRepo(db), nil)
		_ = w.Register(taskType, func(ctx context.Context, p json.RawMessage) (json.RawMessage, error) { return p, nil })
		w.Start()
		workers = append(workers, w)
	}
	time.Sleep(500 * time.Millisecond) // let workers settle into steady state

	lat := make([]time.Duration, 0, samples)
	for i := 0; i < samples; i++ {
		t0 := time.Now()
		res, err := jobRepo.EnqueueJob(context.Background(), postgres.EnqueueParams{
			TenantID: tenant.ID, TaskType: taskType, QueueName: "default",
			Payload: []byte(`{}`), Priority: 5, MaxAttempts: 3, TimeoutSeconds: 30,
		})
		if err != nil {
			t.Fatal(err)
		}
		for {
			j, err := jobRepo.GetJob(context.Background(), tenant.ID, res.Job.ID)
			if err == nil && j.Status == domain.StatusSucceeded {
				break
			}
			if time.Since(t0) > 10*time.Second {
				t.Fatalf("job %s not finished after 10s", res.Job.ID)
			}
			time.Sleep(time.Millisecond)
		}
		lat = append(lat, time.Since(t0))
		time.Sleep(time.Duration(10+i%40) * time.Millisecond) // desynchronise from poll ticks
	}
	for _, w := range workers {
		w.Stop(3 * time.Second)
	}
	for _, db := range dbs {
		db.Close()
	}
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	t.Logf("BENCH_RESULT idle_samples=%d", samples)
	t.Logf("BENCH_RESULT idle_e2e_p50_ms=%.1f", float64(pct(lat, .50).Microseconds())/1000)
	t.Logf("BENCH_RESULT idle_e2e_p95_ms=%.1f", float64(pct(lat, .95).Microseconds())/1000)
	t.Logf("BENCH_RESULT idle_e2e_p99_ms=%.1f", float64(pct(lat, .99).Microseconds())/1000)
}
