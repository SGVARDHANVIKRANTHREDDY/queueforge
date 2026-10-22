package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/config"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/domain"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/httpapi"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/observability"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/postgres"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/recovery"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/scheduler"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/testutil"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/worker"
	"github.com/google/uuid"
)

func setupTestEnvironment(t *testing.T) (*config.Config, *postgres.DB, *httpapi.API, *postgres.TenantRepo, *postgres.JobRepo, *postgres.ScheduleRepo, *postgres.WorkerRepo) {
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	// Private database per test: the scheduler, recovery and workers are global by design.
	testutil.IsolatedDatabase(t, cfg)

	ctx := context.Background()
	db, err := postgres.NewDB(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to connect to test db: %v", err)
	}

	tenantRepo := postgres.NewTenantRepo(db)
	jobRepo := postgres.NewJobRepo(db)
	scheduleRepo := postgres.NewScheduleRepo(db)
	workerRepo := postgres.NewWorkerRepo(db)
	metrics := observability.NewMetrics(nil)

	api := httpapi.NewAPI(cfg, db, tenantRepo, jobRepo, scheduleRepo, workerRepo, metrics)

	return cfg, db, api, tenantRepo, jobRepo, scheduleRepo, workerRepo
}

func createTestTenantAndKey(t *testing.T, tenantRepo *postgres.TenantRepo, name string) (*domain.Tenant, string) {
	ctx := context.Background()
	tenant, err := tenantRepo.CreateTenant(ctx, postgres.CreateTenantParams{
		Name:                 name,
		EnqueueRatePerSecond: 100,
		EnqueueBurstCapacity: 200,
		MaxPendingJobs:       1000,
		MaxConcurrentJobs:    4,
		MaxSchedules:         50,
	})
	if err != nil {
		t.Fatalf("failed to create tenant: %v", err)
	}

	keyRes, err := tenantRepo.CreateAPIKey(ctx, tenant.ID, "Test Key", []string{
		"jobs:read", "jobs:write", "schedules:read", "schedules:write", "deadletters:read", "deadletters:write", "metrics:read",
	}, nil)
	if err != nil {
		t.Fatalf("failed to create api key: %v", err)
	}

	return tenant, keyRes.RawSecret
}

// Stage 1 Test: Basic Enqueue, Retrieval, and Worker Execution
func TestStage1_VerticalSlice(t *testing.T) {
	cfg, db, api, tenantRepo, jobRepo, _, workerRepo := setupTestEnvironment(t)
	defer db.Close()

	tenant, secret := createTestTenantAndKey(t, tenantRepo, "Stage1 Tenant")
	server := httptest.NewServer(api.Routes())
	defer server.Close()

	// 1. Submit a job via POST /v1/jobs
	reqBody := []byte(`{
		"task_type": "echo",
		"queue": "default",
		"payload": {"hello": "world"},
		"priority": 7
	}`)
	req, err := http.NewRequest("POST", server.URL+"/v1/jobs", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to enqueue: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 202 Accepted, got %d: %s", resp.StatusCode, string(body))
	}

	var enqueueResp map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&enqueueResp)
	jobIDStr, ok := enqueueResp["id"].(string)
	if !ok || jobIDStr == "" {
		t.Fatalf("expected job id in response, got %v", enqueueResp)
	}
	jobID, _ := uuid.Parse(jobIDStr)

	// 2. Retrieve job status via GET /v1/jobs/{id}
	getReq, _ := http.NewRequest("GET", server.URL+"/v1/jobs/"+jobIDStr, nil)
	getReq.Header.Set("Authorization", "Bearer "+secret)
	getResp, err := http.DefaultClient.Do(getReq)
	if err != nil || getResp.StatusCode != http.StatusOK {
		t.Fatalf("failed to retrieve job: status %d", getResp.StatusCode)
	}
	var jobDTO map[string]any
	_ = json.NewDecoder(getResp.Body).Decode(&jobDTO)
	getResp.Body.Close()

	if jobDTO["status"] != "queued" {
		t.Fatalf("expected status 'queued', got %v", jobDTO["status"])
	}

	// 3. Worker claims and executes the job
	w := worker.NewWorker(cfg, jobRepo, workerRepo, nil)
	executed := make(chan bool, 1)
	err = w.Register("echo", func(ctx context.Context, payload json.RawMessage) (json.RawMessage, error) {
		executed <- true
		return payload, nil
	})
	if err != nil {
		t.Fatalf("failed to register handler: %v", err)
	}

	w.Start()
	defer w.Stop(5 * time.Second)

	select {
	case <-executed:
		// Handler was called
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for worker to execute job")
	}

	// Give a moment for completion transaction to commit
	time.Sleep(200 * time.Millisecond)

	finalJob, err := jobRepo.GetJob(context.Background(), tenant.ID, jobID)
	if err != nil {
		t.Fatalf("failed to get job after execution: %v", err)
	}
	if finalJob.Status != domain.StatusSucceeded {
		t.Fatalf("expected job to be 'succeeded', got %q", finalJob.Status)
	}
	if string(finalJob.Result) != `{"hello": "world"}` && string(finalJob.Result) != `{"hello":"world"}` {
		t.Fatalf("expected matching result, got %s", string(finalJob.Result))
	}
}

// Stage 2 Test: Idempotency, Concurrent Conflicts, Retries, Lease Recovery, and Replay
func TestStage2_IdempotencyAndRecovery(t *testing.T) {
	cfg, db, api, tenantRepo, jobRepo, _, workerRepo := setupTestEnvironment(t)
	defer db.Close()

	tenant, secret := createTestTenantAndKey(t, tenantRepo, "Stage2 Tenant")
	server := httptest.NewServer(api.Routes())
	defer server.Close()

	idempotencyKey := "idem_" + uuid.New().String()

	// 1. First submission with idempotency key
	postBody1 := `{"task_type":"echo","payload":{"test":"stage2"},"priority":5}`
	req1, _ := http.NewRequest("POST", server.URL+"/v1/jobs", bytes.NewReader([]byte(postBody1)))
	req1.Header.Set("Authorization", "Bearer "+secret)
	req1.Header.Set("Idempotency-Key", idempotencyKey)
	req1.Header.Set("Content-Type", "application/json")

	resp1, err := http.DefaultClient.Do(req1)
	if err != nil || resp1.StatusCode != http.StatusAccepted {
		t.Fatalf("first enqueue failed: %v, status %d", err, resp1.StatusCode)
	}
	var res1 map[string]any
	_ = json.NewDecoder(resp1.Body).Decode(&res1)
	resp1.Body.Close()
	originalID := res1["id"].(string)

	// 2. Exact repeat submission with same key returns 200 OK and Idempotent-Replay header
	req2, _ := http.NewRequest("POST", server.URL+"/v1/jobs", bytes.NewReader([]byte(postBody1)))
	req2.Header.Set("Authorization", "Bearer "+secret)
	req2.Header.Set("Idempotency-Key", idempotencyKey)
	req2.Header.Set("Content-Type", "application/json")

	resp2, err := http.DefaultClient.Do(req2)
	if err != nil || resp2.StatusCode != http.StatusOK {
		t.Fatalf("repeat enqueue expected 200 OK, got %d", resp2.StatusCode)
	}
	if resp2.Header.Get("Idempotent-Replay") != "true" {
		t.Fatalf("expected Idempotent-Replay header to be 'true'")
	}
	var res2 map[string]any
	_ = json.NewDecoder(resp2.Body).Decode(&res2)
	resp2.Body.Close()
	if res2["id"] != originalID {
		t.Fatalf("expected same job ID %s, got %s", originalID, res2["id"])
	}

	// 3. Reusing key with different semantic request returns 409 Conflict
	postBodyDiff := `{"task_type":"echo","payload":{"test":"DIFFERENT_PAYLOAD"},"priority":5}`
	req3, _ := http.NewRequest("POST", server.URL+"/v1/jobs", bytes.NewReader([]byte(postBodyDiff)))
	req3.Header.Set("Authorization", "Bearer "+secret)
	req3.Header.Set("Idempotency-Key", idempotencyKey)
	req3.Header.Set("Content-Type", "application/json")

	resp3, err := http.DefaultClient.Do(req3)
	if err != nil || resp3.StatusCode != http.StatusConflict {
		t.Fatalf("conflicting key reuse expected 409 Conflict, got %d", resp3.StatusCode)
	}
	resp3.Body.Close()

	// 4. Test crash recovery: worker claims job, stops renewing, lease expires, recovery loop recovers it
	jobID, _ := uuid.Parse(originalID)
	// Manually set job lease to expired
	_, err = db.Pool.Exec(context.Background(), `
		UPDATE jobs
		SET status = 'running',
		    lease_token = gen_random_uuid(),
		    worker_id = gen_random_uuid(),
		    attempts_started = 1,
		    lease_expires_at = clock_timestamp() - INTERVAL '10 seconds'
		WHERE id = $1
	`, jobID)
	if err != nil {
		t.Fatalf("failed to simulate expired lease: %v", err)
	}

	// Also insert attempt row for the simulated attempt
	_, _ = db.Pool.Exec(context.Background(), `
		INSERT INTO job_attempts (
			id, job_id, tenant_id, replay_generation, attempt_number,
			worker_id, lease_token, started_at
		) SELECT gen_random_uuid(), id, tenant_id, replay_generation, attempts_started,
		         worker_id, lease_token, clock_timestamp() - INTERVAL '15 seconds'
		  FROM jobs WHERE id = $1
	`, jobID)

	recLoop := recovery.NewRecoveryLoop(cfg, jobRepo, nil)
	recCount, err := jobRepo.RecoverExpiredLeases(context.Background(), 10, domain.NewBackoffPolicy(10*time.Millisecond, 50*time.Millisecond, nil))
	if err != nil || recCount == 0 {
		t.Fatalf("expected at least 1 job recovered, got %d, err: %v", recCount, err)
	}
	_ = recLoop

	recoveredJob, err := jobRepo.GetJob(context.Background(), tenant.ID, jobID)
	if err != nil {
		t.Fatalf("failed to get recovered job: %v", err)
	}
	if recoveredJob.Status != domain.StatusQueued {
		t.Fatalf("expected recovered job to be 'queued', got %q", recoveredJob.Status)
	}

	// 5. Test permanent error moves directly to dead state
	failRes, err := jobRepo.EnqueueJob(context.Background(), postgres.EnqueueParams{
		TenantID:        tenant.ID,
		TaskType:        "test.fail",
		QueueName:       "default",
		Payload:         []byte(`{"permanent": true, "message": "bad argument"}`),
		Priority:        5,
		MaxAttempts:     5,
		TimeoutSeconds:  30,
		IsExplicitRunAt: false,
	})
	if err != nil {
		t.Fatalf("failed to enqueue fail job: %v", err)
	}

	w := worker.NewWorker(cfg, jobRepo, workerRepo, nil)
	_ = w.Register("test.fail", func(ctx context.Context, payload json.RawMessage) (json.RawMessage, error) {
		return nil, domain.NewPermanentError(errors.New("bad argument"))
	})
	w.Start()
	time.Sleep(1 * time.Second)
	w.Stop(2 * time.Second)

	deadJob, err := jobRepo.GetJob(context.Background(), tenant.ID, failRes.Job.ID)
	if err != nil {
		t.Fatalf("failed to get dead job: %v", err)
	}
	if deadJob.Status != domain.StatusDead {
		t.Fatalf("expected permanent error job to be 'dead', got %q", deadJob.Status)
	}

	// 6. Test manual replay of dead job
	replayedJob, err := jobRepo.ReplayDeadJob(context.Background(), tenant.ID, deadJob.ID, nil, "Operator replay test")
	if err != nil {
		t.Fatalf("failed to replay dead job: %v", err)
	}
	if replayedJob.Status != domain.StatusQueued {
		t.Fatalf("expected replayed job to be 'queued', got %q", replayedJob.Status)
	}
	if replayedJob.ReplayGeneration != 1 {
		t.Fatalf("expected replay_generation = 1, got %d", replayedJob.ReplayGeneration)
	}
}

// Stage 3 Test: Delayed Jobs, Cron Scheduling, Rate Limits, and Tenant Quotas
func TestStage3_SchedulesQuotasAndDelayedJobs(t *testing.T) {
	cfg, db, api, tenantRepo, jobRepo, scheduleRepo, _ := setupTestEnvironment(t)
	defer db.Close()

	tenant, _ := createTestTenantAndKey(t, tenantRepo, "Stage3 Tenant")
	server := httptest.NewServer(api.Routes())
	defer server.Close()

	// 1. Delayed job: run_at in future
	futureTime := time.Now().UTC().Add(1 * time.Hour)
	delayedRes, err := jobRepo.EnqueueJob(context.Background(), postgres.EnqueueParams{
		TenantID:        tenant.ID,
		TaskType:        "echo",
		QueueName:       "default",
		Payload:         []byte(`{"delayed": true}`),
		Priority:        9,
		RunAt:           futureTime,
		IsExplicitRunAt: true,
		MaxAttempts:     5,
		TimeoutSeconds:  30,
	})
	if err != nil {
		t.Fatalf("failed to enqueue delayed job: %v", err)
	}

	// Worker attempt to claim should return nil because run_at > now()
	claimRes, err := jobRepo.ClaimJob(context.Background(), uuid.New(), []string{"default"}, []string{"echo"}, 60*time.Second)
	if err != nil {
		t.Fatalf("claim failed: %v", err)
	}
	if claimRes != nil && claimRes.Job.ID == delayedRes.Job.ID {
		t.Fatalf("delayed job should NOT have been claimed before due time!")
	}

	// 2. Cron schedule creation and processing
	sched, err := scheduleRepo.CreateSchedule(context.Background(), postgres.CreateScheduleParams{
		TenantID:       tenant.ID,
		Name:           "Hourly Report",
		QueueName:      "default",
		TaskType:       "report.generate",
		Payload:        []byte(`{"type":"hourly"}`),
		CronExpression: "0 * * * *",
		Timezone:       "UTC",
		Priority:       5,
		MisfirePolicy:  domain.MisfireCatchUpOne,
	})
	if err != nil {
		t.Fatalf("failed to create schedule: %v", err)
	}

	// Simulate due schedule by backdating next_run_at
	_, _ = db.Pool.Exec(context.Background(), `
		UPDATE job_schedules
		SET next_run_at = clock_timestamp() - INTERVAL '1 minute'
		WHERE id = $1
	`, sched.ID)

	schedLoop := scheduler.NewScheduler(cfg, scheduleRepo, nil)
	count, err := scheduleRepo.ProcessDueSchedules(context.Background(), 10, 30*time.Second)
	if err != nil || count == 0 {
		t.Fatalf("expected schedule occurrence to be generated, got count %d, err: %v", count, err)
	}
	_ = schedLoop

	// Verify occurrence uniqueness: running ProcessDueSchedules again does not duplicate
	count2, err := scheduleRepo.ProcessDueSchedules(context.Background(), 10, 30*time.Second)
	if err != nil || count2 != 0 {
		t.Fatalf("duplicate occurrence created! Expected 0, got %d", count2)
	}

	// 3. Test rate limiting: token bucket depletion
	// Deplete rate bucket to 0 tokens
	_, _ = db.Pool.Exec(context.Background(), `
		UPDATE tenant_rate_buckets
		SET tokens = 0.2, last_refill_at = clock_timestamp()
		WHERE tenant_id = $1
	`, tenant.ID)

	_, errRate := jobRepo.EnqueueJob(context.Background(), postgres.EnqueueParams{
		TenantID:        tenant.ID,
		TaskType:        "echo",
		QueueName:       "default",
		Payload:         []byte(`{}`),
		Priority:        5,
		MaxAttempts:     5,
		TimeoutSeconds:  30,
		IsExplicitRunAt: false,
	})
	if !errors.Is(errRate, domain.ErrRateLimited) {
		t.Fatalf("expected ErrRateLimited when tokens depleted, got: %v", errRate)
	}

	// 4. Test pending jobs quota: max_pending_jobs ceiling
	_, _ = db.Pool.Exec(context.Background(), `
		UPDATE tenant_rate_buckets SET tokens = 100 WHERE tenant_id = $1
	`, tenant.ID)
	_, _ = db.Pool.Exec(context.Background(), `
		UPDATE tenant_usage SET outstanding_jobs = 1000 WHERE tenant_id = $1
	`, tenant.ID)
	_, _ = db.Pool.Exec(context.Background(), `
		UPDATE tenant_limits SET max_pending_jobs = 1000 WHERE tenant_id = $1
	`, tenant.ID)

	_, errQuota := jobRepo.EnqueueJob(context.Background(), postgres.EnqueueParams{
		TenantID:        tenant.ID,
		TaskType:        "echo",
		QueueName:       "default",
		Payload:         []byte(`{}`),
		Priority:        5,
		MaxAttempts:     5,
		TimeoutSeconds:  30,
		IsExplicitRunAt: false,
	})
	if !errors.Is(errQuota, domain.ErrQuotaExceeded) {
		t.Fatalf("expected ErrQuotaExceeded when at ceiling, got: %v", errQuota)
	}

	// 5. Test cross-tenant isolation: Tenant B cannot access Tenant A's jobs
	tenantB, secretB := createTestTenantAndKey(t, tenantRepo, "Tenant B")
	_ = tenantB
	getCrossReq, _ := http.NewRequest("GET", server.URL+"/v1/jobs/"+delayedRes.Job.ID.String(), nil)
	getCrossReq.Header.Set("Authorization", "Bearer "+secretB)
	crossResp, err := http.DefaultClient.Do(getCrossReq)
	if err != nil || crossResp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-tenant access should return 404, got status: %d", crossResp.StatusCode)
	}
	crossResp.Body.Close()
}
