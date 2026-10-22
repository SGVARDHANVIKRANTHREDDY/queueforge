// Package property_test is a model-based randomized test of the job state machine.
//
// It drives random sequences of operations (enqueue, claim, complete, fail, cancel, renew, lease
// expiry + recovery, plus deliberately STALE completions) against a real PostgreSQL database and,
// after EVERY step, compares the database with an independent in-memory model and checks the
// system-wide invariants. A failure prints the seed and the operation trace so it can be replayed
// exactly: QF_PROP_SEED=<n> go test -run TestStateMachine ./tests/property/
package property_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/config"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/domain"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/postgres"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/testutil"
	"github.com/google/uuid"
)

type mJob struct {
	id       uuid.UUID
	tenant   int
	status   domain.JobStatus
	lease    uuid.UUID // current lease while running
	attempts int
	max      int
	stale    []uuid.UUID // leases that used to be valid; must now be rejected
}

type harness struct {
	t        *testing.T
	ctx      context.Context
	db       *postgres.DB
	jobs     *postgres.JobRepo
	rnd      *rand.Rand
	tenants  []*domain.Tenant
	maxConc  []int
	taskType string
	model    map[uuid.UUID]*mJob
	order    []uuid.UUID
	trace    []string
	backoff  *domain.BackoffPolicy
}

func envInt(k string, d int) int {
	if v, err := strconv.Atoi(os.Getenv(k)); err == nil {
		return v
	}
	return d
}

func TestStateMachine(t *testing.T) {
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
	defer db.Close()

	seeds, steps := envInt("QF_PROP_SEEDS", 25), envInt("QF_PROP_STEPS", 200)
	first := int64(envInt("QF_PROP_SEED", 1))
	if os.Getenv("QF_PROP_SEED") != "" {
		seeds = 1
	}
	for s := int64(0); s < int64(seeds); s++ {
		seed := first + s
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) { runSeed(t, ctx, db, seed, steps) })
	}
}

func runSeed(t *testing.T, ctx context.Context, db *postgres.DB, seed int64, steps int) {
	h := &harness{t: t, ctx: ctx, db: db, jobs: postgres.NewJobRepo(db), rnd: rand.New(rand.NewSource(seed)),
		taskType: fmt.Sprintf("prop.%d", seed), model: map[uuid.UUID]*mJob{},
		backoff: domain.NewBackoffPolicy(time.Millisecond, 2*time.Millisecond, rand.New(rand.NewSource(seed)))}
	tr := postgres.NewTenantRepo(db)
	for i := 0; i < 3; i++ {
		conc := 1 + h.rnd.Intn(3)
		tn, err := tr.CreateTenant(ctx, postgres.CreateTenantParams{
			Name: fmt.Sprintf("prop-%d-%d-%s", seed, i, uuid.NewString()[:4]), EnqueueRatePerSecond: 1e6,
			EnqueueBurstCapacity: 1e6, MaxPendingJobs: 100000, MaxConcurrentJobs: conc,
		})
		if err != nil {
			t.Fatal(err)
		}
		h.tenants, h.maxConc = append(h.tenants, tn), append(h.maxConc, conc)
	}
	defer func() {
		if t.Failed() {
			t.Logf("seed=%d trace (last 40 ops):\n  %s", seed, strings.Join(h.trace[max(0, len(h.trace)-40):], "\n  "))
			t.Logf("replay with: QF_PROP_SEED=%d go test -run TestStateMachine ./tests/property/", seed)
		}
	}()
	for i := 0; i < steps && !t.Failed(); i++ {
		h.step(i)
		h.check(i)
	}
}

func (h *harness) log(f string, a ...any) { h.trace = append(h.trace, fmt.Sprintf(f, a...)) }

func (h *harness) running() []*mJob {
	var out []*mJob
	for _, id := range h.order {
		if j := h.model[id]; j.status == domain.StatusRunning {
			out = append(out, j)
		}
	}
	return out
}
func (h *harness) pick(js []*mJob) *mJob {
	if len(js) == 0 {
		return nil
	}
	return js[h.rnd.Intn(len(js))]
}
func (h *harness) withStatus(s domain.JobStatus) []*mJob {
	var out []*mJob
	for _, id := range h.order {
		if j := h.model[id]; j.status == s {
			out = append(out, j)
		}
	}
	return out
}

func (h *harness) step(i int) {
	t := h.t
	r := h.rnd.Intn(100)
	switch {
	case r < 28: // enqueue
		ti, ma := h.rnd.Intn(3), 1+h.rnd.Intn(3)
		res, err := h.jobs.EnqueueJob(h.ctx, postgres.EnqueueParams{TenantID: h.tenants[ti].ID, TaskType: h.taskType,
			QueueName: "default", Payload: []byte(`{}`), Priority: int16(h.rnd.Intn(10)), MaxAttempts: ma, TimeoutSeconds: 30})
		if err != nil {
			t.Fatalf("step %d enqueue: %v", i, err)
		}
		h.model[res.Job.ID] = &mJob{id: res.Job.ID, tenant: ti, status: domain.StatusQueued, max: ma}
		h.order = append(h.order, res.Job.ID)
		h.log("%d enqueue tenant=%d max_attempts=%d -> %s", i, ti, ma, res.Job.ID.String()[:8])

	case r < 48: // claim a batch
		n := 1 + h.rnd.Intn(3)
		got, err := h.jobs.ClaimJobs(h.ctx, uuid.New(), []string{"default"}, []string{h.taskType}, time.Minute, n)
		if err != nil {
			t.Fatalf("step %d claim: %v", i, err)
		}
		if len(got) > n {
			t.Fatalf("step %d: claimed %d > requested %d", i, len(got), n)
		}
		for _, c := range got {
			m := h.model[c.Job.ID]
			if m == nil || m.status != domain.StatusQueued {
				t.Fatalf("step %d: claimed job %s that the model says is %v", i, c.Job.ID, m)
			}
			m.status, m.lease, m.attempts = domain.StatusRunning, c.LeaseToken, m.attempts+1
		}
		h.log("%d claim n=%d -> %d jobs", i, n, len(got))

	case r < 58: // complete successfully with the valid lease
		if m := h.pick(h.running()); m != nil {
			if err := h.jobs.CompleteJobSuccess(h.ctx, h.tenants[m.tenant].ID, m.id, m.lease, []byte(`{"ok":1}`), 5); err != nil {
				t.Fatalf("step %d complete: %v", i, err)
			}
			m.stale, m.status = append(m.stale, m.lease), domain.StatusSucceeded
			h.log("%d success %s", i, m.id.String()[:8])
		}

	case r < 72: // fail (retryable or permanent) with the valid lease
		if m := h.pick(h.running()); m != nil {
			var e error = errors.New("boom")
			perm := h.rnd.Intn(4) == 0
			if perm {
				e = domain.NewPermanentError(e)
			}
			if err := h.jobs.CompleteJobFailure(h.ctx, h.tenants[m.tenant].ID, m.id, m.lease, e, h.backoff, 5); err != nil {
				t.Fatalf("step %d fail: %v", i, err)
			}
			m.stale = append(m.stale, m.lease)
			if perm || m.attempts >= m.max {
				m.status = domain.StatusDead
			} else {
				m.status = domain.StatusQueued
			}
			h.log("%d fail perm=%v %s -> %s", i, perm, m.id.String()[:8], m.status)
		}

	case r < 80: // STALE completion must be rejected and change nothing
		// The dangerous case is a stale worker waking up AFTER another worker re-claimed the job:
		// the job is running again under a NEW lease. Prefer those jobs; fall back to any job that
		// has ever had a lease. (An earlier version picked uniformly and never exercised the
		// dangerous case: a mutation that removed the lease check went undetected.)
		var dangerous, any []*mJob
		for _, id := range h.order {
			if j := h.model[id]; len(j.stale) > 0 {
				any = append(any, j)
				if j.status == domain.StatusRunning {
					dangerous = append(dangerous, j)
				}
			}
		}
		cand := dangerous
		if len(cand) == 0 {
			cand = any
		}
		if m := h.pick(cand); m != nil {
			tok := m.stale[h.rnd.Intn(len(m.stale))]
			var err error
			if h.rnd.Intn(2) == 0 {
				err = h.jobs.CompleteJobSuccess(h.ctx, h.tenants[m.tenant].ID, m.id, tok, []byte(`{"stale":1}`), 1)
			} else {
				err = h.jobs.CompleteJobFailure(h.ctx, h.tenants[m.tenant].ID, m.id, tok, errors.New("stale"), h.backoff, 1)
			}
			if !errors.Is(err, domain.ErrLeaseLost) {
				t.Fatalf("step %d: stale lease completion on %s returned %v, want ErrLeaseLost", i, m.id, err)
			}
			h.log("%d stale-completion rejected %s", i, m.id.String()[:8])
		}

	case r < 88: // lease expires and crash recovery runs
		if m := h.pick(h.running()); m != nil {
			if _, err := h.db.Pool.Exec(h.ctx, `UPDATE jobs SET lease_expires_at = clock_timestamp() - interval '1 second' WHERE id=$1`, m.id); err != nil {
				t.Fatal(err)
			}
			if _, err := h.jobs.RecoverExpiredLeases(h.ctx, 100, h.backoff); err != nil {
				t.Fatalf("step %d recover: %v", i, err)
			}
			m.stale = append(m.stale, m.lease)
			if m.attempts >= m.max {
				m.status = domain.StatusDead
			} else {
				m.status = domain.StatusQueued
			}
			h.log("%d expire+recover %s -> %s", i, m.id.String()[:8], m.status)
		}

	case r < 94: // cancel a queued job
		if m := h.pick(h.withStatus(domain.StatusQueued)); m != nil {
			if _, err := h.jobs.CancelJob(h.ctx, h.tenants[m.tenant].ID, m.id); err != nil {
				t.Fatalf("step %d cancel: %v", i, err)
			}
			m.status = domain.StatusCancelled
			h.log("%d cancel %s", i, m.id.String()[:8])
		}

	default: // renew: valid lease works, stale lease is rejected
		if m := h.pick(h.running()); m != nil {
			if _, err := h.jobs.RenewLease(h.ctx, m.id, m.lease, time.Minute); err != nil {
				t.Fatalf("step %d renew valid lease: %v", i, err)
			}
			h.log("%d renew %s", i, m.id.String()[:8])
		}
	}
}

// check compares the database with the model and verifies the system-wide invariants.
func (h *harness) check(step int) {
	t := h.t
	fail := func(f string, a ...any) { t.Errorf("after step %d: "+f, append([]any{step}, a...)...) }

	rows, err := h.db.Pool.Query(h.ctx, `SELECT id, status, attempts_started FROM jobs WHERE task_type=$1`, h.taskType)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for rows.Next() {
		var id uuid.UUID
		var st string
		var att int
		if err := rows.Scan(&id, &st, &att); err != nil {
			t.Fatal(err)
		}
		seen++
		m := h.model[id]
		if m == nil {
			fail("db has unknown job %s", id)
			continue
		}
		if string(m.status) != st {
			fail("job %s: db status %q, model %q", id.String()[:8], st, m.status)
		}
		if m.attempts != att {
			fail("job %s: db attempts_started %d, model %d", id.String()[:8], att, m.attempts)
		}
	}
	rows.Close()
	if seen != len(h.model) {
		fail("db has %d jobs, model has %d", seen, len(h.model))
	}

	for i, tn := range h.tenants {
		var nonTerminal, runningN int
		for _, m := range h.model {
			if m.tenant == i && (m.status == domain.StatusQueued || m.status == domain.StatusRunning) {
				nonTerminal++
				if m.status == domain.StatusRunning {
					runningN++
				}
			}
		}
		var outstanding int64
		var busy, orphan int
		_ = h.db.Pool.QueryRow(h.ctx, `SELECT outstanding_jobs FROM tenant_usage WHERE tenant_id=$1`, tn.ID).Scan(&outstanding)
		_ = h.db.Pool.QueryRow(h.ctx, `SELECT count(*) FROM tenant_execution_slots WHERE tenant_id=$1 AND current_job_id IS NOT NULL`, tn.ID).Scan(&busy)
		_ = h.db.Pool.QueryRow(h.ctx, `
			SELECT count(*) FROM tenant_execution_slots s
			WHERE s.tenant_id=$1 AND s.current_job_id IS NOT NULL
			  AND NOT EXISTS (SELECT 1 FROM jobs j WHERE j.id=s.current_job_id AND j.status='running' AND j.lease_token=s.lease_token)`, tn.ID).Scan(&orphan)
		if int(outstanding) != nonTerminal {
			fail("tenant %d: outstanding_jobs=%d but %d jobs are queued/running", i, outstanding, nonTerminal)
		}
		if busy != runningN {
			fail("tenant %d: %d busy slots but %d running jobs", i, busy, runningN)
		}
		if busy > h.maxConc[i] {
			fail("tenant %d: %d busy slots exceeds max_concurrent_jobs=%d", i, busy, h.maxConc[i])
		}
		if orphan != 0 {
			fail("tenant %d: %d busy slots do not point at a running job holding that lease", i, orphan)
		}
	}

	var open, attemptRows, startedTotal int
	_ = h.db.Pool.QueryRow(h.ctx, `SELECT count(*) FROM job_attempts a JOIN jobs j ON j.id=a.job_id WHERE j.task_type=$1 AND a.finished_at IS NULL`, h.taskType).Scan(&open)
	_ = h.db.Pool.QueryRow(h.ctx, `SELECT count(*) FROM job_attempts a JOIN jobs j ON j.id=a.job_id WHERE j.task_type=$1`, h.taskType).Scan(&attemptRows)
	_ = h.db.Pool.QueryRow(h.ctx, `SELECT COALESCE(sum(attempts_started),0) FROM jobs WHERE task_type=$1`, h.taskType).Scan(&startedTotal)
	wantOpen := len(h.running())
	if open != wantOpen {
		fail("%d unfinished attempts but %d running jobs", open, wantOpen)
	}
	if attemptRows != startedTotal {
		fail("%d attempt rows but sum(attempts_started)=%d", attemptRows, startedTotal)
	}
}
