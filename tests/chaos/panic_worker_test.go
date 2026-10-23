//go:build !windows

package chaos_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/config"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/postgres"
	"github.com/google/uuid"
)

// syncBuffer is a goroutine-safe sink for a child process's output.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// buildTestWorkerBinary compiles tests/chaos/testworker (the misbehaving-handlers worker).
func buildTestWorkerBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "qf-testworker")
	build := exec.Command("go", "build", "-o", bin, "./tests/chaos/testworker")
	build.Dir = "../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build testworker: %v\n%s", err, out)
	}
	return bin
}

// TestChaos_PanickingHandlersDoNotKillWorkerProcesses runs REAL worker processes against a mix of
// healthy jobs and jobs whose handler panics. Before the safeCall fix, the first panic terminated
// the whole worker process (Go does not contain a panic in a goroutine), taking every other job in
// flight on that worker with it, and the poison job then crashed the next worker that claimed it.
//
// It asserts, end to end:
//   - both worker processes stay alive for the whole run
//   - every healthy job succeeds (exactly one succeeded attempt each)
//   - every panicking job is retried to max_attempts and ends 'dead' with a "panicked" error
//   - a job that fails with a credential in its error text is stored REDACTED
//   - the panics were logged with a stack, and nothing leaks (slots, counters, open attempts)
func TestChaos_PanickingHandlersDoNotKillWorkerProcesses(t *testing.T) {
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

	const panicAttempts = 3
	const (
		nPanic = 30
		nOK    = 120
		nSlow  = 6
		nLeak  = 3
	)
	tenant, err := postgres.NewTenantRepo(db).CreateTenant(ctx, postgres.CreateTenantParams{
		Name: "panic-" + uuid.NewString()[:6], EnqueueRatePerSecond: 100000, EnqueueBurstCapacity: 100000,
		MaxPendingJobs: 100000, MaxConcurrentJobs: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	enqueue := func(taskType, payload string, maxAttempts int) {
		t.Helper()
		if _, err := jobs.EnqueueJob(ctx, postgres.EnqueueParams{
			TenantID: tenant.ID, TaskType: taskType, QueueName: "default",
			Payload: []byte(payload), Priority: 5, MaxAttempts: maxAttempts, TimeoutSeconds: 60,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < nPanic; i++ {
		enqueue("panic", `{}`, panicAttempts)
	}
	for i := 0; i < nOK; i++ {
		enqueue("ok", `{}`, 3)
	}
	for i := 0; i < nSlow; i++ {
		enqueue("slow", `{"ms":1500}`, 3)
	}
	for i := 0; i < nLeak; i++ {
		enqueue("leak", `{}`, 3)
	}

	bin := buildTestWorkerBinary(t)
	env := append(os.Environ(),
		"QF_DATABASE_URL="+privateURL,
		"QF_DEFAULT_LEASE_DURATION=5s", "QF_LEASE_RENEW_INTERVAL=1s", "QF_RECOVERY_INTERVAL=1s",
		"QF_CLAIM_POLL_INTERVAL=100ms", "QF_RETRY_BASE_DELAY=100ms", "QF_RETRY_MAX_DELAY=200ms",
		"QF_WORKER_HEARTBEAT_INTERVAL=1s",
	)
	var procs []*exec.Cmd
	var outs []*syncBuffer
	for i := 0; i < 2; i++ {
		c := exec.Command(bin)
		c.Env = env
		out := &syncBuffer{}
		c.Stdout, c.Stderr = out, out
		c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		procs = append(procs, c)
		outs = append(outs, out)
	}
	exited := make([]chan struct{}, len(procs))
	for i, c := range procs {
		exited[i] = make(chan struct{})
		go func(c *exec.Cmd, ch chan struct{}) { _ = c.Wait(); close(ch) }(c, exited[i])
	}
	defer func() {
		for _, c := range procs {
			_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		}
	}()
	assertAlive := func(when string) {
		t.Helper()
		for i, ch := range exited {
			select {
			case <-ch:
				t.Fatalf("worker process %d DIED %s; a handler panic must never take the process down.\n--- its output (tail) ---\n%s", i, when, tail(outs[i].String(), 3000))
			default:
			}
		}
	}

	count := func(q string) int {
		var n int
		if err := db.Pool.QueryRow(ctx, q, tenant.ID).Scan(&n); err != nil {
			return -1
		}
		return n
	}
	waitFor(t, 120*time.Second, "healthy jobs to succeed and poison jobs to end dead", func() bool {
		assertAlive("while draining the mixed workload")
		return count(`SELECT count(*) FROM jobs WHERE tenant_id=$1 AND status='succeeded'`) == nOK+nSlow &&
			count(`SELECT count(*) FROM jobs WHERE tenant_id=$1 AND status='dead'`) == nPanic+nLeak
	})
	assertAlive("after the workload finished")

	// Healthy jobs: exactly one succeeded attempt each.
	if n := count(`SELECT count(*) FROM jobs j WHERE j.tenant_id=$1 AND j.status='succeeded' AND (SELECT count(*) FROM job_attempts a WHERE a.job_id=j.id AND a.outcome='succeeded') <> 1`); n != 0 {
		t.Fatalf("%d succeeded jobs do not have exactly one succeeded attempt", n)
	}

	// Panicking jobs: retried to max attempts, all attempts closed, error text says why.
	if n := count(`SELECT count(*) FROM jobs WHERE tenant_id=$1 AND task_type='panic' AND status='dead' AND attempts_started=3 AND last_error_message LIKE '%panicked%'`); n != nPanic {
		t.Fatalf("want %d panic jobs dead after exactly %d attempts with a 'panicked' error, got %d", nPanic, panicAttempts, n)
	}
	if n := count(`SELECT count(*) FROM job_attempts a JOIN jobs j ON j.id=a.job_id WHERE j.tenant_id=$1 AND j.task_type='panic'`); n != nPanic*panicAttempts {
		t.Fatalf("want %d attempt rows for the panic jobs, got %d", nPanic*panicAttempts, n)
	}

	// Credential in an error message must be redacted in BOTH places it is stored.
	if n := count(`SELECT count(*) FROM jobs WHERE tenant_id=$1 AND task_type='leak' AND status='dead' AND last_error_message LIKE '%[REDACTED]%' AND last_error_message NOT LIKE '%hunter2secret%'`); n != nLeak {
		t.Fatalf("want %d leak jobs with a redacted last_error_message, got %d", nLeak, n)
	}
	if n := count(`SELECT count(*) FROM job_attempts WHERE tenant_id=$1 AND error_message LIKE '%hunter2secret%'`); n != 0 {
		t.Fatalf("%d attempt rows still contain the credential", n)
	}

	// Nothing leaked.
	busy := count(`SELECT count(*) FROM tenant_execution_slots WHERE tenant_id=$1 AND current_job_id IS NOT NULL`)
	unfinished := count(`SELECT count(*) FROM job_attempts WHERE tenant_id=$1 AND finished_at IS NULL`)
	outstanding := count(`SELECT outstanding_jobs FROM tenant_usage WHERE tenant_id=$1`)
	if busy != 0 || unfinished != 0 || outstanding != 0 {
		t.Fatalf("leak: busy_slots=%d unfinished_attempts=%d outstanding=%d", busy, unfinished, outstanding)
	}

	// Stop the workers gracefully (they must still respond to SIGTERM), then inspect their logs.
	for _, c := range procs {
		_ = c.Process.Signal(syscall.SIGTERM)
	}
	for i, ch := range exited {
		select {
		case <-ch:
		case <-time.After(45 * time.Second):
			t.Fatalf("worker process %d did not exit after SIGTERM", i)
		}
	}
	var logged int
	for _, o := range outs {
		logged += bytes.Count([]byte(o.String()), []byte("handler panicked"))
	}
	if logged < nPanic*panicAttempts {
		t.Fatalf("expected at least %d 'handler panicked' log lines (one per panic, with a stack), got %d", nPanic*panicAttempts, logged)
	}
	t.Logf("CHAOS_RESULT panic_jobs=%d attempts=%d panics_logged=%d healthy_jobs=%d workers_survived=2", nPanic, nPanic*panicAttempts, logged, nOK+nSlow)
}

// tail returns at most the last n bytes of s.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
