package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/config"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/domain"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/observability"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/postgres"
	"github.com/google/uuid"
)

type Handler func(ctx context.Context, payload json.RawMessage) (json.RawMessage, error)

// safeCall runs a handler and converts a panic into an ordinary (retryable)
// error. Without this, one panicking handler would terminate the whole worker
// process and every other in-flight job with it; the job would then be retried
// on another worker and could crash that one too.
func safeCall(h Handler, ctx context.Context, payload json.RawMessage) (result json.RawMessage, err error) {
	defer func() {
		if p := recover(); p != nil {
			slog.Error("handler panicked", "panic", fmt.Sprint(p), "stack", string(debug.Stack()))
			result = nil
			err = fmt.Errorf("handler panicked: %v", p)
		}
	}()
	return h(ctx, payload)
}

type Worker struct {
	id            uuid.UUID
	processName   string
	version       string
	hostname      string
	cfg           *config.Config
	jobRepo       *postgres.JobRepo
	workerRepo    *postgres.WorkerRepo
	metrics       *observability.Metrics
	backoffPolicy *domain.BackoffPolicy

	handlersMu sync.RWMutex
	handlers   map[string]Handler

	queues   []string
	sem      chan struct{} // bounds in-flight jobs for this process
	wake     chan struct{} // "there may be work" signal (NOTIFY, or a job just finished)
	draining bool
	drainMu  sync.RWMutex
	activeWg sync.WaitGroup
	ctx      context.Context
	cancel   context.CancelFunc

	completer *batchCompleter // nil unless QF_BATCH_COMPLETION=true
}

func NewWorker(
	cfg *config.Config,
	jobRepo *postgres.JobRepo,
	workerRepo *postgres.WorkerRepo,
	metrics *observability.Metrics,
) *Worker {
	hostname, _ := os.Hostname()
	ctx, cancel := context.WithCancel(context.Background())

	return &Worker{
		id:            uuid.New(),
		processName:   "queueforge-worker",
		version:       "1.0.0",
		hostname:      hostname,
		cfg:           cfg,
		jobRepo:       jobRepo,
		workerRepo:    workerRepo,
		metrics:       metrics,
		backoffPolicy: domain.NewBackoffPolicy(cfg.RetryBaseDelay, cfg.RetryMaxDelay, nil),
		handlers:      make(map[string]Handler),
		queues:        cfg.AllowedQueues,
		sem:           make(chan struct{}, max(1, cfg.WorkerConcurrency)),
		wake:          make(chan struct{}, 1),
		ctx:           ctx,
		cancel:        cancel,
	}
}

func (w *Worker) ID() uuid.UUID {
	return w.id
}

func (w *Worker) Register(taskType string, handler Handler) error {
	w.handlersMu.Lock()
	defer w.handlersMu.Unlock()

	if taskType == "" {
		return fmt.Errorf("task type cannot be empty")
	}
	if _, exists := w.handlers[taskType]; exists {
		return fmt.Errorf("handler for task type %q is already registered", taskType)
	}

	w.handlers[taskType] = handler
	return nil
}

func (w *Worker) Start() {
	slog.Info("starting worker", "worker_id", w.id.String(), "hostname", w.hostname)

	if w.cfg.BatchCompletion {
		w.completer = newBatchCompleter(w.jobRepo, w.cfg.BatchCompletionMax)
	}

	// Heartbeat loop
	go w.heartbeatLoop()

	// Wake the claim loop the moment a job is enqueued (polling remains as the fallback).
	go w.jobRepo.ListenForJobs(w.ctx, w.wake)

	// Claim and execution loop
	go w.claimLoop()
}

func (w *Worker) Stop(timeout time.Duration) {
	slog.Info("stopping worker gracefully", "worker_id", w.id.String())
	defer func() {
		if w.completer != nil {
			w.completer.Stop()
		}
	}()

	w.drainMu.Lock()
	w.draining = true
	w.drainMu.Unlock()

	// Update registry with draining status immediately
	_ = w.workerRepo.Heartbeat(context.Background(), postgres.HeartbeatParams{
		WorkerID:    w.id,
		ProcessName: w.processName,
		Hostname:    w.hostname,
		Version:     w.version,
		Queues:      w.queues,
		TaskTypes:   w.getRegisteredTaskTypes(),
		Draining:    true,
	})

	w.cancel()

	done := make(chan struct{})
	go func() {
		w.activeWg.Wait()
		close(done)
	}()

	select {
	case <-done:
		slog.Info("all active worker jobs drained successfully", "worker_id", w.id.String())
	case <-time.After(timeout):
		slog.Warn("worker shutdown timeout reached; abandoned handlers will recover via lease expiry", "worker_id", w.id.String())
	}
}

func (w *Worker) getRegisteredTaskTypes() []string {
	w.handlersMu.RLock()
	defer w.handlersMu.RUnlock()

	types := make([]string, 0, len(w.handlers))
	for k := range w.handlers {
		types = append(types, k)
	}
	return types
}

func (w *Worker) isDraining() bool {
	w.drainMu.RLock()
	defer w.drainMu.RUnlock()
	return w.draining
}

func (w *Worker) heartbeatLoop() {
	ticker := time.NewTicker(w.cfg.WorkerHeartbeatInterval)
	defer ticker.Stop()

	// Initial heartbeat
	_ = w.workerRepo.Heartbeat(w.ctx, postgres.HeartbeatParams{
		WorkerID:    w.id,
		ProcessName: w.processName,
		Hostname:    w.hostname,
		Version:     w.version,
		Queues:      w.queues,
		TaskTypes:   w.getRegisteredTaskTypes(),
		Draining:    w.isDraining(),
	})

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
			err := w.workerRepo.Heartbeat(w.ctx, postgres.HeartbeatParams{
				WorkerID:    w.id,
				ProcessName: w.processName,
				Hostname:    w.hostname,
				Version:     w.version,
				Queues:      w.queues,
				TaskTypes:   w.getRegisteredTaskTypes(),
				Draining:    w.isDraining(),
			})
			if err != nil {
				slog.Error("worker heartbeat error", "error", err, "worker_id", w.id.String())
			}
		}
	}
}

// claimLoop wakes on (a) a NOTIFY from an enqueue, (b) a job finishing locally (which frees a
// concurrency slot and a tenant slot), or (c) the poll ticker, which is the safety net for missed
// notifications and for delayed/retried jobs that simply become due. On every wake-up it keeps
// claiming until there is no more claimable work or this process is at its concurrency limit,
// instead of claiming a single job per tick (which capped each worker at 1000/poll_ms jobs/sec).
func (w *Worker) claimLoop() {
	ticker := time.NewTicker(w.cfg.ClaimPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
		case <-w.wake:
		}
		if w.isDraining() {
			return
		}
		for w.tryClaimAndExecute() {
		}
	}
}

// signalWake nudges the claim loop without ever blocking.
func (w *Worker) signalWake() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// tryClaimAndExecute claims up to min(free concurrency, ClaimBatchSize) jobs in one transaction
// and starts them. It reports whether it filled the whole batch (so the caller should immediately
// try again); a short batch means the queue ran dry for this worker.
func (w *Worker) tryClaimAndExecute() bool {
	taskTypes := w.getRegisteredTaskTypes()
	if len(taskTypes) == 0 {
		return false
	}

	// Reserve concurrency BEFORE taking leases, so we never hold a lease (and a tenant slot) for a
	// job we cannot start yet.
	want := w.cfg.ClaimBatchSize
	if want < 1 {
		want = 1
	}
	reserved := 0
	for reserved < want {
		select {
		case w.sem <- struct{}{}:
			reserved++
			continue
		default:
		}
		break
	}
	if reserved == 0 {
		return false
	}

	claimCtx, cancel := context.WithTimeout(w.ctx, 5*time.Second)
	defer cancel()

	claims, err := w.jobRepo.ClaimJobs(claimCtx, w.id, w.queues, taskTypes, w.cfg.DefaultLeaseDuration, reserved)
	for i := len(claims); i < reserved; i++ {
		<-w.sem // give back concurrency we reserved but did not use
	}
	if err != nil {
		if w.ctx.Err() == nil {
			slog.Error("error claiming jobs", "error", err)
		}
		return false
	}

	for _, claimRes := range claims {
		if w.metrics != nil {
			w.metrics.JobsClaimedTotal.WithLabelValues(claimRes.Job.QueueName).Inc()
			// Measured on the database clock, so worker clock skew cannot corrupt the metric.
			w.metrics.JobQueueWaitSeconds.WithLabelValues(claimRes.Job.QueueName).Observe(claimRes.QueueWaitSeconds)
		}
		claimRes := claimRes
		w.activeWg.Add(1)
		go func() {
			defer func() {
				<-w.sem
				w.activeWg.Done()
				w.signalWake() // a slot just freed up: there may be more work we can take
			}()
			w.executeJob(claimRes.Job, claimRes.LeaseToken)
		}()
	}
	return len(claims) == reserved
}

func (w *Worker) executeJob(job *domain.Job, leaseToken uuid.UUID) {
	w.handlersMu.RLock()
	handler, exists := w.handlers[job.TaskType]
	w.handlersMu.RUnlock()

	if !exists {
		// Defensive check: this shouldn't happen because we filtered by supported task types
		slog.Error("handler not found for claimed job", "task_type", job.TaskType, "job_id", job.ID)
		_ = w.jobRepo.CompleteJobFailure(context.Background(), job.TenantID, job.ID, leaseToken,
			domain.NewPermanentError(fmt.Errorf("unsupported task type %q", job.TaskType)), w.backoffPolicy, 0)
		return
	}

	log := slog.With("job_id", job.ID, "trace_id", job.TraceID, "task_type", job.TaskType, "attempt", job.AttemptsStarted, "worker_id", w.id)
	log.Info("job started")

	// Execution context with timeout
	timeout := time.Duration(job.TimeoutSeconds) * time.Second
	execCtx, cancelExec := context.WithTimeout(context.Background(), timeout)
	defer cancelExec()

	// Cancellation signal tracker for cooperative cancellation
	cancelFlag := false
	var cancelMu sync.Mutex

	// Lease renewal ticker
	renewTicker := time.NewTicker(w.cfg.LeaseRenewInterval)
	defer renewTicker.Stop()

	renewDone := make(chan struct{})
	defer close(renewDone)

	go func() {
		for {
			select {
			case <-renewDone:
				return
			case <-renewTicker.C:
				renewCtx, renewCancel := context.WithTimeout(context.Background(), 3*time.Second)
				res, err := w.jobRepo.RenewLease(renewCtx, job.ID, leaseToken, w.cfg.DefaultLeaseDuration)
				renewCancel()

				if err != nil {
					if errors.Is(err, domain.ErrLeaseLost) {
						slog.Warn("lease lost during execution, aborting handler", "job_id", job.ID)
						cancelExec()
						return
					}
					slog.Error("error renewing lease", "error", err, "job_id", job.ID)
				} else if res != nil && res.CancellationRequested {
					slog.Info("cancellation requested for running job, cancelling context", "job_id", job.ID)
					cancelMu.Lock()
					cancelFlag = true
					cancelMu.Unlock()
					cancelExec()
				}
			}
		}
	}()

	start := time.Now()
	result, handlerErr := safeCall(handler, execCtx, job.Payload)
	duration := time.Since(start)
	durationMs := duration.Milliseconds()

	cancelMu.Lock()
	wasCancelled := cancelFlag
	cancelMu.Unlock()
	log.Info("job handler finished", "duration_ms", durationMs, "failed", handlerErr != nil)

	if w.metrics != nil {
		w.metrics.JobExecutionDurationSeconds.WithLabelValues(job.QueueName).Observe(duration.Seconds())
	}

	if wasCancelled && handlerErr != nil {
		// Cooperative cancellation acknowledgement
		errCommit := w.commitWithRetry(job, "cancellation", func(ctx context.Context) error {
			return w.jobRepo.CompleteJobCancelled(ctx, job.TenantID, job.ID, leaseToken, durationMs)
		})
		if errCommit != nil && !errors.Is(errCommit, domain.ErrLeaseLost) {
			slog.Error("failed to record job cancellation", "error", errCommit, "job_id", job.ID)
		}
		if w.metrics != nil {
			w.metrics.JobsCompletedTotal.WithLabelValues(job.QueueName, "cancelled").Inc()
			w.metrics.JobAttemptsTotal.WithLabelValues(job.QueueName, "cancelled").Inc()
		}
		return
	}

	if handlerErr == nil {
		// Success
		errCommit := w.commitWithRetry(job, "success", func(ctx context.Context) error {
			if w.completer != nil {
				return w.completer.Submit(ctx, successItem{tenantID: job.TenantID, jobID: job.ID, leaseToken: leaseToken, result: result, durationMs: durationMs})
			}
			return w.jobRepo.CompleteJobSuccess(ctx, job.TenantID, job.ID, leaseToken, result, durationMs)
		})
		if errCommit != nil {
			if errors.Is(errCommit, domain.ErrLeaseLost) {
				slog.Warn("lease lost before success commit could be recorded", "job_id", job.ID)
			} else {
				slog.Error("failed to record job success", "error", errCommit, "job_id", job.ID)
			}
		} else {
			if w.metrics != nil {
				w.metrics.JobsCompletedTotal.WithLabelValues(job.QueueName, "succeeded").Inc()
				w.metrics.JobAttemptsTotal.WithLabelValues(job.QueueName, "succeeded").Inc()
			}
		}
	} else {
		// Error
		slog.Warn("job handler returned error", "error", handlerErr, "job_id", job.ID)
		errCommit := w.commitWithRetry(job, "failure", func(ctx context.Context) error {
			return w.jobRepo.CompleteJobFailure(ctx, job.TenantID, job.ID, leaseToken, handlerErr, w.backoffPolicy, durationMs)
		})
		if errCommit != nil && !errors.Is(errCommit, domain.ErrLeaseLost) {
			slog.Error("failed to record job failure", "error", errCommit, "job_id", job.ID)
		}
		if w.metrics != nil {
			w.metrics.JobAttemptsTotal.WithLabelValues(job.QueueName, "failed").Inc()
			// Count what actually happened to the job, and only if that outcome was recorded.
			// (JobsCompletedTotal used to be incremented only for success and cancellation, so
			// dead-lettered jobs were invisible to metrics and alerts; retries were never counted.)
			if errCommit == nil {
				var perm domain.PermanentError
				permanent := errors.As(handlerErr, &perm)
				if permanent || job.AttemptsStarted >= job.MaxAttempts {
					w.metrics.JobsCompletedTotal.WithLabelValues(job.QueueName, "dead").Inc()
				} else {
					class := "retryable"
					if errors.Is(handlerErr, context.DeadlineExceeded) {
						class = "timeout"
					}
					w.metrics.JobRetriesTotal.WithLabelValues(job.QueueName, class).Inc()
				}
			}
		}
	}
}

// commitWithRetry records the outcome of a finished attempt, retrying transient failures.
//
// Why: the handler has ALREADY run. If recording the result fails because of a brief network
// fault, giving up (as an earlier version did) means the lease expires and the whole job is
// executed again, which for a long job wastes minutes of work and repeats its side effects. In the
// network-fault chaos test this was 16 wasted executions out of 256.
//
// The lease renewer keeps running until executeJob returns, so the lease stays valid while we
// retry. ErrLeaseLost is final (someone else owns the job now). After a retry, ErrLeaseLost can
// also mean "my earlier attempt actually committed and only the reply was lost", so it is reported
// at info level rather than as a failure.
func (w *Worker) commitWithRetry(job *domain.Job, what string, fn func(ctx context.Context) error) error {
	budget := w.cfg.CommitRetryBudget
	if budget <= 0 {
		budget = 60 * time.Second
	}
	deadline := time.Now().Add(budget)
	delay := 250 * time.Millisecond
	for attempt := 1; ; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := fn(ctx)
		cancel()
		if err == nil {
			if attempt > 1 {
				slog.Info("recorded job outcome after retry", "job_id", job.ID, "outcome", what, "attempts", attempt)
			}
			return nil
		}
		if errors.Is(err, domain.ErrLeaseLost) {
			if attempt > 1 {
				slog.Info("lease lost after a retried commit; the earlier attempt may have committed", "job_id", job.ID, "outcome", what)
			}
			return err
		}
		if time.Now().Add(delay).After(deadline) {
			return fmt.Errorf("giving up recording %s after %d attempts: %w", what, attempt, err)
		}
		slog.Warn("could not record job outcome; will retry", "job_id", job.ID, "outcome", what, "attempt", attempt, "retry_in", delay, "error", err)
		time.Sleep(delay)
		if delay < 5*time.Second {
			delay *= 2
		}
	}
}
