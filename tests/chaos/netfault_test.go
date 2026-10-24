//go:build !windows

package chaos_test

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/config"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/postgres"
	"github.com/google/uuid"
)

// TestChaos_NetworkFaultsBetweenWorkersAndPostgres runs two real worker processes whose database
// traffic goes through a fault-injecting proxy, then subjects them to: added latency, repeated
// connection resets, and a black hole longer than the lease. The test drives the verification
// connection straight to Postgres (not through the proxy).
//
// Asserted: every job still succeeds, nothing goes dead, no slot or quota leaks, worker processes
// survive, and every successful job has exactly one successful attempt. Reported: how many
// EXECUTIONS were wasted (re-runs of work that had already finished because the worker could not
// record the result), the number that matters for long jobs.
func TestChaos_NetworkFaultsBetweenWorkersAndPostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns processes and takes ~40s")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	realURL := privateDatabase(t, cfg)
	cfg.DatabaseURL = realURL
	db, err := postgres.NewDB(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	jobs := postgres.NewJobRepo(db)

	u, _ := parseHostPort(realURL)
	proxy := newFaultProxy(t, u)

	const total = 240
	tenant, err := postgres.NewTenantRepo(db).CreateTenant(ctx, postgres.CreateTenantParams{
		Name: "netfault-" + uuid.NewString()[:6], EnqueueRatePerSecond: 1e5, EnqueueBurstCapacity: 1e5,
		MaxPendingJobs: 1e5, MaxConcurrentJobs: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < total; i++ {
		if _, err := jobs.EnqueueJob(ctx, postgres.EnqueueParams{TenantID: tenant.ID, TaskType: "test.sleep",
			QueueName: "default", Payload: []byte(`{"ms":400}`), Priority: 5, MaxAttempts: 10, TimeoutSeconds: 60}); err != nil {
			t.Fatal(err)
		}
	}

	bin := buildWorkerBinary(t)
	env := append(os.Environ(),
		"QF_DATABASE_URL="+proxy.RewriteURL(realURL),
		"QF_DEFAULT_LEASE_DURATION=5s", "QF_LEASE_RENEW_INTERVAL=1s", "QF_RECOVERY_INTERVAL=1s",
		"QF_CLAIM_POLL_INTERVAL=100ms", "QF_RETRY_BASE_DELAY=200ms", "QF_RETRY_MAX_DELAY=1s",
		"QF_WORKER_HEARTBEAT_INTERVAL=1s", "QF_METRICS_ADDR=127.0.0.1:0",
	)
	var procs []*exec.Cmd
	var exited []chan struct{}
	for i := 0; i < 2; i++ {
		c := exec.Command(bin)
		c.Env = env
		c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		ch := make(chan struct{})
		go func(c *exec.Cmd) { _, _ = c.Process.Wait(); close(ch) }(c)
		procs, exited = append(procs, c), append(exited, ch)
	}
	defer func() {
		for _, c := range procs {
			_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		}
	}()

	succeeded := func() int {
		var n int
		_ = db.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE tenant_id=$1 AND status='succeeded'`, tenant.ID).Scan(&n)
		return n
	}
	waitFor(t, 30*time.Second, "workers to start processing", func() bool { return succeeded() >= 20 })

	t.Log("fault 1: +250ms latency for 4s")
	proxy.SetDelay(250 * time.Millisecond)
	time.Sleep(4 * time.Second)
	proxy.SetDelay(0)

	t.Log("fault 2: connection resets x4")
	for i := 0; i < 4; i++ {
		proxy.ResetAll()
		time.Sleep(1200 * time.Millisecond)
	}

	t.Log("fault 3: black hole for 8s (longer than the 5s lease)")
	proxy.SetBlackhole(true)
	time.Sleep(8 * time.Second)
	proxy.SetBlackhole(false)
	proxy.ResetAll() // connections that lived through the black hole are in an unknown state
	atEnd := succeeded()
	t.Logf("CHAOS_RESULT done_when_faults_ended=%d/%d", atEnd, total)
	if atEnd >= total {
		t.Fatal("invalid test: all jobs finished before the faults ended")
	}

	waitFor(t, 90*time.Second, "all jobs to succeed after the faults", func() bool { return succeeded() == total })

	for i, ch := range exited {
		select {
		case <-ch:
			t.Fatalf("worker process %d died during network faults", i)
		default:
		}
	}
	var dead, busy, unfinished, doubleSuccess, notExactlyOne int
	var outstanding int64
	var started int64
	_ = db.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE tenant_id=$1 AND status='dead'`, tenant.ID).Scan(&dead)
	_ = db.Pool.QueryRow(ctx, `SELECT count(*) FROM tenant_execution_slots WHERE tenant_id=$1 AND current_job_id IS NOT NULL`, tenant.ID).Scan(&busy)
	_ = db.Pool.QueryRow(ctx, `SELECT outstanding_jobs FROM tenant_usage WHERE tenant_id=$1`, tenant.ID).Scan(&outstanding)
	_ = db.Pool.QueryRow(ctx, `SELECT COALESCE(sum(attempts_started),0) FROM jobs WHERE tenant_id=$1`, tenant.ID).Scan(&started)
	_ = db.Pool.QueryRow(ctx, `SELECT count(*) FROM job_attempts WHERE tenant_id=$1 AND finished_at IS NULL`, tenant.ID).Scan(&unfinished)
	_ = db.Pool.QueryRow(ctx, `SELECT count(*) FROM (SELECT job_id FROM job_attempts WHERE tenant_id=$1 AND outcome='succeeded' GROUP BY job_id HAVING count(*) > 1) x`, tenant.ID).Scan(&doubleSuccess)
	_ = db.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs j WHERE j.tenant_id=$1 AND j.status='succeeded' AND (SELECT count(*) FROM job_attempts a WHERE a.job_id=j.id AND a.outcome='succeeded') <> 1`, tenant.ID).Scan(&notExactlyOne)
	wasted := started - total
	t.Logf("CHAOS_RESULT netfault total_jobs=%d executions=%d wasted_executions=%d dead=%d busy_slots=%d outstanding=%d unfinished_attempts=%d double_success=%d not_exactly_one_success=%d",
		total, started, wasted, dead, busy, outstanding, unfinished, doubleSuccess, notExactlyOne)
	if dead != 0 || busy != 0 || outstanding != 0 || unfinished != 0 || doubleSuccess != 0 || notExactlyOne != 0 {
		t.Fatalf("dead=%d busy=%d outstanding=%d unfinished_attempts=%d double_success=%d not_exactly_one_success=%d",
			dead, busy, outstanding, unfinished, doubleSuccess, notExactlyOne)
	}
	// A partition longer than the lease can duplicate at most the tenant's concurrent work plus a
	// small scheduling margin. The measured best case was 3-7, but 11 runs ranged from 3 to 16,
	// so the bound must reflect the configured tenant concurrency rather than the best case.
	const tenantMaxConcurrentJobs = 16
	const wastedExecutionMargin = 4
	const wastedExecutionLimit = tenantMaxConcurrentJobs + wastedExecutionMargin
	if wasted > wastedExecutionLimit {
		t.Fatalf("wasted executions %d exceeds the at-least-once bound %d (tenant concurrency %d + margin %d)",
			wasted, wastedExecutionLimit, tenantMaxConcurrentJobs, wastedExecutionMargin)
	}
}

func parseHostPort(dbURL string) (string, error) {
	pu, err := urlParse(dbURL)
	return pu, err
}
