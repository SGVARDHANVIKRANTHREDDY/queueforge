//go:build !windows

package chaos_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/config"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/postgres"
	"github.com/google/uuid"
)

// outageScenario selects which recovery mechanism the outage is supposed to exercise.
//
// commitBudget is QF_COMMIT_RETRY_BUDGET for the worker processes: how long a worker keeps retrying
// to record a finished job before giving up and leaving it to lease recovery. requireRetries
// asserts that at least one job was re-run; that is only meaningful when the commit budget is
// shorter than the outage, so the commit retry CANNOT absorb it.
type outageScenario struct {
	commitBudget   string
	requireRetries bool
}

// TestChaos_PostgresOutageMidWorkload stops the database while two real worker processes are
// draining a backlog, keeps it down for longer than the lease, then brings it back.
//
// It is opt-in because it needs permission to stop/start the Postgres server, and it would break
// any other test using that server, so run it alone:
//
//	QF_CHAOS_PG_STOP='docker stop qf-race-pg' QF_CHAOS_PG_START='docker start qf-race-pg' \
//	  go test -v -p 1 -timeout 20m -run PostgresOutage ./tests/chaos/
//
// Two scenarios, because there are two different recovery mechanisms:
//
//  1. short_outage_is_absorbed_by_commit_retry: the default 60s commit budget outlasts the 8s
//     outage, so workers simply retry recording finished jobs when the database returns and NO job
//     needs to run twice. Re-runs are therefore informational here, not a requirement. (An earlier
//     version of this test demanded at least one re-run; that encoded an accident of timing and
//     failed precisely because the commit-retry fix works.)
//  2. long_outage_exhausts_commit_budget_and_lease_recovery_takes_over: workers give up after 2s,
//     the 5s lease expires during the outage, and lease recovery must requeue the in-flight jobs.
//     Here re-runs ARE required, otherwise the fault injection did not bite and the test proved
//     nothing.
//
// Safety invariants asserted in both (these are the guarantees that matter):
//   - worker processes SURVIVE the outage (they log errors and keep retrying; they do not exit)
//   - no job is lost: every job reaches 'succeeded', none goes 'dead' because of infrastructure
//   - each succeeded job has exactly ONE succeeded attempt (no double completion)
//   - nothing leaks: zero busy execution slots, zero unfinished attempts, outstanding counter 0
func TestChaos_PostgresOutageMidWorkload(t *testing.T) {
	stopCmd, startCmd := os.Getenv("QF_CHAOS_PG_STOP"), os.Getenv("QF_CHAOS_PG_START")
	if stopCmd == "" || startCmd == "" {
		t.Skip("set QF_CHAOS_PG_STOP and QF_CHAOS_PG_START to run (see test comment)")
	}
	t.Run("short_outage_is_absorbed_by_commit_retry", func(t *testing.T) {
		runPostgresOutage(t, stopCmd, startCmd, outageScenario{commitBudget: "60s", requireRetries: false})
	})
	t.Run("long_outage_exhausts_commit_budget_and_lease_recovery_takes_over", func(t *testing.T) {
		runPostgresOutage(t, stopCmd, startCmd, outageScenario{commitBudget: "2s", requireRetries: true})
	})
}

func runPostgresOutage(t *testing.T, stopCmd, startCmd string, sc outageScenario) {
	t.Helper()
	sh := func(c string) {
		t.Helper()
		if out, err := exec.Command("sh", "-c", c).CombinedOutput(); err != nil {
			t.Fatalf("%q failed: %v\n%s", c, err, out)
		}
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	privateURL := privateDatabase(t, cfg)
	cfg.DatabaseURL = privateURL
	db, err := postgres.NewDB(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	jobs := postgres.NewJobRepo(db)

	const total = 400
	tenant, err := postgres.NewTenantRepo(db).CreateTenant(ctx, postgres.CreateTenantParams{
		Name: "outage-" + uuid.NewString()[:6], EnqueueRatePerSecond: 100000, EnqueueBurstCapacity: 100000,
		MaxPendingJobs: 100000, MaxConcurrentJobs: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < total; i++ {
		if _, err := jobs.EnqueueJob(ctx, postgres.EnqueueParams{
			TenantID: tenant.ID, TaskType: "test.sleep", QueueName: "default",
			Payload: []byte(`{"ms":500}`), Priority: 5, MaxAttempts: 8, TimeoutSeconds: 60,
		}); err != nil {
			t.Fatal(err)
		}
	}

	bin := buildWorkerBinary(t)
	env := append(os.Environ(),
		"QF_DATABASE_URL="+privateURL,
		"QF_DEFAULT_LEASE_DURATION=5s", "QF_LEASE_RENEW_INTERVAL=1s", "QF_RECOVERY_INTERVAL=1s",
		"QF_CLAIM_POLL_INTERVAL=100ms", "QF_RETRY_BASE_DELAY=200ms", "QF_RETRY_MAX_DELAY=1s",
		"QF_WORKER_HEARTBEAT_INTERVAL=1s",
		"QF_COMMIT_RETRY_BUDGET="+sc.commitBudget,
	)
	var procs []*exec.Cmd
	for i := 0; i < 2; i++ {
		c := exec.Command(bin)
		c.Env = env
		c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		procs = append(procs, c)
	}
	exited := make([]chan struct{}, len(procs))
	for i, c := range procs {
		exited[i] = make(chan struct{})
		go func(c *exec.Cmd, ch chan struct{}) { _, _ = c.Process.Wait(); close(ch) }(c, exited[i])
	}
	defer func() {
		for _, c := range procs {
			_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		}
	}()
	// Whatever happens, leave Postgres running for whoever comes next (including the next subtest).
	defer func() { _ = exec.Command("sh", "-c", startCmd).Run() }()

	count := func(q string) int {
		var n int
		if err := db.Pool.QueryRow(ctx, q, tenant.ID).Scan(&n); err != nil {
			return -1
		}
		return n
	}
	succeeded := func() int {
		return count(`SELECT count(*) FROM jobs WHERE tenant_id=$1 AND status='succeeded'`)
	}

	waitFor(t, 30*time.Second, "some jobs to complete before the outage", func() bool { return succeeded() >= 40 })
	before := succeeded()

	t.Logf("scenario commit_budget=%s: stopping Postgres with %d/%d jobs done", sc.commitBudget, before, total)
	sh(stopCmd)
	outageStart := time.Now()
	time.Sleep(8 * time.Second) // longer than the 5s lease: every in-flight lease expires during the outage

	for i, ch := range exited {
		select {
		case <-ch:
			t.Fatalf("worker process %d EXITED during the Postgres outage; it must survive and retry", i)
		default:
		}
	}

	sh(startCmd)
	waitFor(t, 30*time.Second, "Postgres to accept connections again", func() bool { return db.Ping(ctx) == nil })
	t.Logf("CHAOS_RESULT outage_seconds=%.1f", time.Since(outageStart).Seconds())
	upAt := time.Now()

	// Guard against a vacuous pass: the outage only counts if work was still outstanding.
	if atUp := succeeded(); atUp >= total {
		t.Fatalf("invalid test: all %d jobs had already finished before the outage took effect", total)
	} else {
		t.Logf("CHAOS_RESULT done_shortly_after_db_returned=%d/%d", atUp, total)
	}

	waitFor(t, 90*time.Second, "all jobs to succeed after the outage", func() bool { return succeeded() == total })
	t.Logf("CHAOS_RESULT recovery_to_all_done_seconds=%.1f", time.Since(upAt).Seconds())

	for i, ch := range exited {
		select {
		case <-ch:
			t.Fatalf("worker process %d died after the outage", i)
		default:
		}
	}

	dead := count(`SELECT count(*) FROM jobs WHERE tenant_id=$1 AND status='dead'`)
	retried := count(`SELECT count(*) FROM jobs WHERE tenant_id=$1 AND attempts_started>1`)
	busy := count(`SELECT count(*) FROM tenant_execution_slots WHERE tenant_id=$1 AND current_job_id IS NOT NULL`)
	unfinished := count(`SELECT count(*) FROM job_attempts WHERE tenant_id=$1 AND finished_at IS NULL`)
	outstanding := count(`SELECT outstanding_jobs FROM tenant_usage WHERE tenant_id=$1`)
	doubleSuccess := count(`SELECT count(*) FROM (SELECT job_id FROM job_attempts WHERE tenant_id=$1 AND outcome='succeeded' GROUP BY job_id HAVING count(*) > 1) x`)
	notExactlyOne := count(`SELECT count(*) FROM jobs j WHERE j.tenant_id=$1 AND j.status='succeeded' AND (SELECT count(*) FROM job_attempts a WHERE a.job_id=j.id AND a.outcome='succeeded') <> 1`)
	t.Logf("CHAOS_RESULT scenario_commit_budget=%s jobs_rerun=%d dead=%d busy_slots=%d unfinished_attempts=%d outstanding=%d double_success=%d not_exactly_one_success=%d",
		sc.commitBudget, retried, dead, busy, unfinished, outstanding, doubleSuccess, notExactlyOne)

	if dead != 0 {
		t.Fatalf("%d jobs went dead because of an infrastructure outage (they should have been retried)", dead)
	}
	if busy != 0 || outstanding != 0 || unfinished != 0 {
		t.Fatal(fmt.Sprintf("leak after outage: busy_slots=%d outstanding=%d unfinished_attempts=%d", busy, outstanding, unfinished))
	}
	if doubleSuccess != 0 || notExactlyOne != 0 {
		t.Fatalf("completion integrity violated: jobs with >1 succeeded attempt=%d, succeeded jobs without exactly one succeeded attempt=%d", doubleSuccess, notExactlyOne)
	}
	if sc.requireRetries && retried == 0 {
		t.Fatal("invalid test: with a 2s commit budget, an 8s outage and a 5s lease, in-flight jobs must have been recovered and re-run, but none was. The fault injection did not bite")
	}
}
