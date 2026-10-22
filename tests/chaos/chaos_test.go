package chaos_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/config"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/domain"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/postgres"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/recovery"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/testutil"
	"github.com/google/uuid"
)

func setupChaosEnv(t *testing.T) (*config.Config, *postgres.DB, *postgres.TenantRepo, *postgres.JobRepo, *postgres.ScheduleRepo, *postgres.WorkerRepo, *domain.Tenant) {
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	testutil.IsolatedDatabase(t, cfg)
	ctx := context.Background()
	db, err := postgres.NewDB(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to connect to db: %v", err)
	}

	tenantRepo := postgres.NewTenantRepo(db)
	jobRepo := postgres.NewJobRepo(db)
	scheduleRepo := postgres.NewScheduleRepo(db)
	workerRepo := postgres.NewWorkerRepo(db)

	tenant, err := tenantRepo.CreateTenant(ctx, postgres.CreateTenantParams{
		Name:                 "Chaos Test Tenant",
		EnqueueRatePerSecond: 200,
		EnqueueBurstCapacity: 400,
		MaxPendingJobs:       2000,
		MaxConcurrentJobs:    6,
	})
	if err != nil {
		t.Fatalf("failed to create tenant: %v", err)
	}

	return cfg, db, tenantRepo, jobRepo, scheduleRepo, workerRepo, tenant
}

// Chaos Test 1: Worker crash mid-execution followed by lease expiry and recovery
func TestChaos_WorkerCrashAndLeaseRecovery(t *testing.T) {
	cfg, db, _, jobRepo, _, _, tenant := setupChaosEnv(t)
	defer db.Close()

	// Enqueue job with unique task type
	uniqueTaskType := "crash.task." + uuid.New().String()[:8]
	enqRes, err := jobRepo.EnqueueJob(context.Background(), postgres.EnqueueParams{
		TenantID:        tenant.ID,
		TaskType:        uniqueTaskType,
		QueueName:       "default",
		Payload:         []byte(`{"attempt": 1}`),
		Priority:        5,
		MaxAttempts:     5,
		TimeoutSeconds:  30,
		IsExplicitRunAt: false,
	})
	if err != nil {
		t.Fatalf("failed to enqueue: %v", err)
	}

	// Worker claims the job and crashes immediately (process killed before any completion commit)
	workerID := uuid.New()
	claimRes, err := jobRepo.ClaimJob(context.Background(), workerID, []string{"default"}, []string{uniqueTaskType}, 10*time.Second)
	if err != nil || claimRes == nil {
		t.Fatalf("failed to claim job: %v", err)
	}
	_ = enqRes

	targetJobID := claimRes.Job.ID
	targetTenantID := claimRes.Job.TenantID

	// Verify job is currently in 'running' state with worker 1 lease
	claimedJob, err := jobRepo.GetJob(context.Background(), targetTenantID, targetJobID)
	if err != nil {
		t.Fatalf("failed to get claimed job: %v", err)
	}
	if claimedJob.Status != domain.StatusRunning {
		t.Fatalf("expected job to be running, got %v", claimedJob.Status)
	}

	// Backdate lease expiration in PostgreSQL to simulate elapsed lease duration
	_, err = db.Pool.Exec(context.Background(), `
		UPDATE jobs
		SET lease_expires_at = clock_timestamp() - INTERVAL '5 seconds'
		WHERE id = $1
	`, targetJobID)
	if err != nil {
		t.Fatalf("failed to backdate lease: %v", err)
	}

	// Run recovery loop
	recLoop := recovery.NewRecoveryLoop(cfg, jobRepo, nil)
	recoveredCount, err := jobRepo.RecoverExpiredLeases(context.Background(), 10, domain.NewBackoffPolicy(10*time.Millisecond, 20*time.Millisecond, nil))
	if err != nil || recoveredCount < 1 {
		t.Fatalf("expected at least 1 job recovered, got %d, err: %v", recoveredCount, err)
	}
	_ = recLoop

	// Verify job was requeued
	requeuedJob, err := jobRepo.GetJob(context.Background(), targetTenantID, targetJobID)
	if err != nil {
		t.Fatalf("failed to get requeued job: %v", err)
	}
	if requeuedJob.Status != domain.StatusQueued {
		t.Fatalf("expected job to be 'queued' after recovery, got %q", requeuedJob.Status)
	}
	if requeuedJob.LeaseToken != nil {
		t.Fatalf("expected lease_token to be cleared after recovery")
	}

	// Verify attempt history has outcome 'lease_expired'
	attempts, err := jobRepo.GetJobAttempts(context.Background(), targetTenantID, targetJobID)
	if err != nil || len(attempts) == 0 {
		t.Fatalf("expected attempt record, got: %v", attempts)
	}
	if *attempts[0].Outcome != domain.OutcomeLeaseExpired {
		t.Fatalf("expected attempt outcome 'lease_expired', got %v", attempts[0].Outcome)
	}
}

// Chaos Test 2: Stale worker fencing (late completion attempt rejected with lease lost)
func TestChaos_StaleWorkerFencing(t *testing.T) {
	_, db, _, jobRepo, _, _, tenant := setupChaosEnv(t)
	defer db.Close()

	res, err := jobRepo.EnqueueJob(context.Background(), postgres.EnqueueParams{
		TenantID:        tenant.ID,
		TaskType:        "fencing.task",
		QueueName:       "default",
		Payload:         []byte(`{}`),
		Priority:        5,
		MaxAttempts:     3,
		TimeoutSeconds:  30,
		IsExplicitRunAt: false,
	})
	if err != nil {
		t.Fatalf("failed to enqueue: %v", err)
	}

	// Worker 1 claims job
	claim1, err := jobRepo.ClaimJob(context.Background(), uuid.New(), []string{"default"}, []string{"fencing.task"}, 10*time.Second)
	if err != nil || claim1 == nil {
		t.Fatalf("failed to claim: %v", err)
	}

	oldToken := claim1.LeaseToken

	// Simulate recovery: lease expires and recovery runs
	_, _ = db.Pool.Exec(context.Background(), `
		UPDATE jobs
		SET lease_expires_at = clock_timestamp() - INTERVAL '5 seconds'
		WHERE id = $1
	`, res.Job.ID)

	_, err = jobRepo.RecoverExpiredLeases(context.Background(), 10, domain.NewBackoffPolicy(1*time.Millisecond, 5*time.Millisecond, nil))
	if err != nil {
		t.Fatalf("recovery failed: %v", err)
	}

	// Now a second worker claims the job with a fresh lease token!
	claim2, err := jobRepo.ClaimJob(context.Background(), uuid.New(), []string{"default"}, []string{"fencing.task"}, 60*time.Second)
	if err != nil || claim2 == nil {
		t.Fatalf("second claim failed: %v", err)
	}
	if claim2.LeaseToken == oldToken {
		t.Fatalf("new claim should have distinct lease token!")
	}

	// Now old worker 1 wakes up and tries to commit success using its oldToken
	staleErr := jobRepo.CompleteJobSuccess(context.Background(), tenant.ID, res.Job.ID, oldToken, []byte(`{"stale":"true"}`), 100)
	if !errors.Is(staleErr, domain.ErrLeaseLost) {
		t.Fatalf("expected ErrLeaseLost when stale worker attempts completion, got: %v", staleErr)
	}
}

// Chaos Test 3: Multiple concurrent schedulers cannot generate duplicate occurrences
func TestChaos_ConcurrentSchedulersOccurrenceUniqueness(t *testing.T) {
	_, db, _, _, scheduleRepo, _, tenant := setupChaosEnv(t)
	defer db.Close()

	sched, err := scheduleRepo.CreateSchedule(context.Background(), postgres.CreateScheduleParams{
		TenantID:       tenant.ID,
		Name:           "Chaos Schedule " + uuid.New().String()[:8],
		QueueName:      "default",
		TaskType:       "echo",
		Payload:        []byte(`{"recurring": true}`),
		CronExpression: "0 0 * * *",
		Timezone:       "UTC",
		MisfirePolicy:  domain.MisfireSkip,
	})
	if err != nil {
		t.Fatalf("failed to create schedule: %v", err)
	}

	// Backdate next_run_at so it is due
	dueTime := time.Now().UTC().Add(-10 * time.Second)
	_, _ = db.Pool.Exec(context.Background(), `
		UPDATE job_schedules
		SET next_run_at = $2
		WHERE id = $1
	`, sched.ID, dueTime)

	// Run 5 scheduler instances simultaneously in parallel goroutines
	var wg sync.WaitGroup
	generatedCounts := make([]int, 5)

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			cnt, _ := scheduleRepo.ProcessDueSchedules(context.Background(), 10, 30*time.Second)
			generatedCounts[idx] = cnt
		}(i)
	}
	wg.Wait()

	totalGenerated := 0
	for _, c := range generatedCounts {
		totalGenerated += c
	}

	if totalGenerated != 1 {
		t.Fatalf("expected exactly 1 occurrence generated across all 5 schedulers, got: %d", totalGenerated)
	}
}
