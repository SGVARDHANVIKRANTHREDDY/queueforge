package chaos_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"queueforge/internal/config"
	"queueforge/internal/domain"
	"queueforge/internal/postgres"
)

// TestChaos_RealKill9_WorkerProcessRecovery is the genuine article: it builds the real worker
// binary, runs it as a separate OS process, SIGKILLs it while it is in the middle of a job (no
// graceful shutdown, no deferred cleanup, no chance to release anything), then starts a second
// worker process and verifies that the job is recovered and completes exactly once.
//
// Unlike TestChaos_WorkerCrashAndLeaseRecovery (which backdates a lease in SQL), nothing here is
// simulated.
func TestChaos_RealKill9_WorkerProcessRecovery(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns processes")
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

	tenant, err := postgres.NewTenantRepo(db).CreateTenant(ctx, postgres.CreateTenantParams{
		Name: "kill9-" + uuid.NewString()[:6], EnqueueRatePerSecond: 100, EnqueueBurstCapacity: 100,
		MaxPendingJobs: 100, MaxConcurrentJobs: 2,
	})
	if err != nil {
		t.Fatal(err)
	}

	bin := buildWorkerBinary(t)

	// Short lease so the test is quick; renew interval well under the lease.
	env := append(os.Environ(),
		"QF_DATABASE_URL="+privateURL,
		"QF_DEFAULT_LEASE_DURATION=3s",
		"QF_LEASE_RENEW_INTERVAL=1s",
		"QF_RECOVERY_INTERVAL=1s",
		"QF_CLAIM_POLL_INTERVAL=100ms",
		"QF_RETRY_BASE_DELAY=100ms",
		"QF_RETRY_MAX_DELAY=200ms",
	)
	start := func() *exec.Cmd {
		c := exec.Command(bin)
		c.Env = env
		c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		return c
	}

	// Worker A takes the job and is killed mid-flight.
	a := start()
	defer func() { _ = syscall.Kill(-a.Process.Pid, syscall.SIGKILL); _, _ = a.Process.Wait() }()

	res, err := jobs.EnqueueJob(ctx, postgres.EnqueueParams{
		TenantID: tenant.ID, TaskType: "test.sleep", QueueName: "default",
		Payload: []byte(`{"ms":600000}`), Priority: 9, MaxAttempts: 5, TimeoutSeconds: 900,
	})
	if err != nil {
		t.Fatal(err)
	}
	jobID := res.Job.ID

	waitFor(t, 20*time.Second, "job to be running on worker A", func() bool {
		j, err := jobs.GetJob(ctx, tenant.ID, jobID)
		return err == nil && j.Status == domain.StatusRunning
	})

	killedAt := time.Now()
	if err := syscall.Kill(-a.Process.Pid, syscall.SIGKILL); err != nil { // SIGKILL: no cleanup possible
		t.Fatal(err)
	}
	_, _ = a.Process.Wait()

	// The job must still look "running" (the dead worker could not have said otherwise)...
	j, _ := jobs.GetJob(ctx, tenant.ID, jobID)
	if j.Status != domain.StatusRunning {
		t.Fatalf("expected job to still be running right after SIGKILL, got %s", j.Status)
	}

	// ...and the "fixed deploy" for the replacement worker: make the retry finish instantly.
	if _, err := db.Pool.Exec(ctx, `UPDATE jobs SET payload='{"ms":0}' WHERE id=$1`, jobID); err != nil {
		t.Fatal(err)
	}

	// Worker B (a different process) must recover the lease and finish the job.
	b := start()
	defer func() { _ = b.Process.Signal(syscall.SIGTERM); _, _ = b.Process.Wait() }()

	waitFor(t, 45*time.Second, "job to succeed on worker B", func() bool {
		j, err := jobs.GetJob(ctx, tenant.ID, jobID)
		return err == nil && j.Status == domain.StatusSucceeded
	})
	recovery := time.Since(killedAt)
	t.Logf("CHAOS_RESULT kill9_to_succeeded_seconds=%.1f (lease=3s)", recovery.Seconds())

	attempts, err := jobs.GetJobAttempts(ctx, tenant.ID, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 {
		t.Fatalf("expected exactly 2 attempts (killed + recovered), got %d", len(attempts))
	}
	if attempts[0].Outcome == nil || *attempts[0].Outcome != domain.OutcomeLeaseExpired {
		t.Fatalf("attempt 1 outcome = %v, want lease_expired", attempts[0].Outcome)
	}
	if attempts[1].Outcome == nil || *attempts[1].Outcome != domain.OutcomeSucceeded {
		t.Fatalf("attempt 2 outcome = %v, want succeeded", attempts[1].Outcome)
	}
	if attempts[0].WorkerID == nil || attempts[1].WorkerID == nil || *attempts[0].WorkerID == *attempts[1].WorkerID {
		t.Fatalf("expected the retry to run on a different worker process")
	}

	// Nothing leaked: slot released, quota counter back to zero.
	var busy int
	var outstanding int64
	_ = db.Pool.QueryRow(ctx, `SELECT count(*) FROM tenant_execution_slots WHERE tenant_id=$1 AND current_job_id IS NOT NULL`, tenant.ID).Scan(&busy)
	_ = db.Pool.QueryRow(ctx, `SELECT outstanding_jobs FROM tenant_usage WHERE tenant_id=$1`, tenant.ID).Scan(&outstanding)
	if busy != 0 || outstanding != 0 {
		t.Fatalf("leak after recovery: busy_slots=%d outstanding=%d", busy, outstanding)
	}

	var result json.RawMessage
	_ = db.Pool.QueryRow(ctx, `SELECT result FROM jobs WHERE id=$1`, jobID).Scan(&result)
	t.Logf("final result: %s", result)
}

func waitFor(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out after %v waiting for %s", d, what)
}
