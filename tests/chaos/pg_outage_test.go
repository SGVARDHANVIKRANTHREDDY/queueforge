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

// TestChaos_PostgresOutageMidWorkload stops the database while two real worker processes are
// draining a backlog, keeps it down for longer than the lease, then brings it back.
//
// It is opt-in because it needs permission to stop/start the Postgres server, and it would break
// any other test using that server, so run it alone:
//
//	QF_CHAOS_PG_STOP='service postgresql stop' QF_CHAOS_PG_START='service postgresql start' \
//	  go test -v -p 1 -run PostgresOutage ./tests/chaos/
//
// Expected behaviour (what the test asserts):
//   - worker processes SURVIVE the outage (they log errors and keep retrying; they do not exit)
//   - no job is lost: after recovery every job reaches 'succeeded'
//   - nothing leaks: zero busy execution slots, outstanding-job counter back to zero
//   - jobs that were mid-flight when the DB vanished are retried (at-least-once), not dropped
func TestChaos_PostgresOutageMidWorkload(t *testing.T) {
	stopCmd, startCmd := os.Getenv("QF_CHAOS_PG_STOP"), os.Getenv("QF_CHAOS_PG_START")
	if stopCmd == "" || startCmd == "" {
		t.Skip("set QF_CHAOS_PG_STOP and QF_CHAOS_PG_START to run (see test comment)")
	}
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
	// Whatever happens, leave Postgres running for whoever comes next.
	defer func() { _ = exec.Command("sh", "-c", startCmd).Run() }()

	succeeded := func() int {
		var n int
		if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE tenant_id=$1 AND status='succeeded'`, tenant.ID).Scan(&n); err != nil {
			return -1
		}
		return n
	}

	waitFor(t, 30*time.Second, "some jobs to complete before the outage", func() bool { return succeeded() >= 40 })
	before := succeeded()

	t.Logf("stopping Postgres with %d/%d jobs done", before, total)
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
		t.Logf("CHAOS_RESULT done_when_db_returned=%d/%d", atUp, total)
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

	var dead, retried, busy int
	var outstanding int64
	_ = db.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE tenant_id=$1 AND status='dead'`, tenant.ID).Scan(&dead)
	_ = db.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE tenant_id=$1 AND attempts_started>1`, tenant.ID).Scan(&retried)
	_ = db.Pool.QueryRow(ctx, `SELECT count(*) FROM tenant_execution_slots WHERE tenant_id=$1 AND current_job_id IS NOT NULL`, tenant.ID).Scan(&busy)
	_ = db.Pool.QueryRow(ctx, `SELECT outstanding_jobs FROM tenant_usage WHERE tenant_id=$1`, tenant.ID).Scan(&outstanding)
	t.Logf("CHAOS_RESULT jobs_retried_due_to_outage=%d dead=%d busy_slots=%d outstanding=%d", retried, dead, busy, outstanding)

	if retried == 0 {
		t.Fatal("no job was retried: jobs in flight when the database vanished should have lost their lease and been retried")
	}
	if dead != 0 {
		t.Fatalf("%d jobs went dead because of an infrastructure outage (they should have been retried)", dead)
	}
	if busy != 0 || outstanding != 0 {
		t.Fatal(fmt.Sprintf("leak after outage: busy_slots=%d outstanding=%d", busy, outstanding))
	}
}
