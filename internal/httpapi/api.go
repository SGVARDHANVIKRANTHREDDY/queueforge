package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"queueforge/internal/auth"
	"queueforge/internal/config"
	"queueforge/internal/domain"
	"queueforge/internal/observability"
	"queueforge/internal/postgres"
)

type API struct {
	cfg          *config.Config
	db           *postgres.DB
	tenantRepo   *postgres.TenantRepo
	jobRepo      *postgres.JobRepo
	scheduleRepo *postgres.ScheduleRepo
	workerRepo   *postgres.WorkerRepo
	auditRepo    *postgres.AuditRepo
	metrics      *observability.Metrics
}

func NewAPI(
	cfg *config.Config,
	db *postgres.DB,
	tenantRepo *postgres.TenantRepo,
	jobRepo *postgres.JobRepo,
	scheduleRepo *postgres.ScheduleRepo,
	workerRepo *postgres.WorkerRepo,
	metrics *observability.Metrics,
) *API {
	return &API{
		cfg:          cfg,
		db:           db,
		tenantRepo:   tenantRepo,
		jobRepo:      jobRepo,
		scheduleRepo: scheduleRepo,
		workerRepo:   workerRepo,
		auditRepo:    postgres.NewAuditRepo(db),
		metrics:      metrics,
	}
}

func (a *API) Routes() http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	// RealIP rewrites r.RemoteAddr from client-supplied headers, so it is only
	// safe when the API is reachable solely through a trusted reverse proxy.
	// Installing it unconditionally let any client spoof its address and
	// defeat the failure limiter and the audit log's remote address.
	if a.cfg.TrustForwardedFor {
		r.Use(middleware.RealIP)
	}
	r.Use(middleware.Recoverer)
	r.Use(a.corsMiddleware)
	r.Use(a.metricsMiddleware)

	// Public liveness, readiness, and metrics
	r.Get("/healthz", a.handleHealthz)
	r.Get("/readyz", a.handleReadyz)

	// Static Dashboard
	r.Handle("/ui/*", http.StripPrefix("/ui", http.FileServer(http.Dir("web"))))
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui/", http.StatusFound)
	})

	// Protected v1 routes
	r.Route("/v1", func(v1 chi.Router) {
		if a.cfg.ClientRatePerSecond > 0 {
			v1.Use(a.clientRateLimitMiddleware(auth.NewClientRateLimiter(float64(a.cfg.ClientRatePerSecond), a.cfg.ClientRateBurst)))
		}
		v1.Use(auth.MiddlewareWithLimiter(a.tenantRepo, auth.DefaultFailureLimiter, a.cfg.TrustForwardedFor))
		v1.Use(a.auditMiddleware)

		// Jobs
		v1.Group(func(jobs chi.Router) {
			jobs.With(auth.RequirePermission("jobs:write")).Post("/jobs", a.handleEnqueueJob)
			jobs.With(auth.RequirePermission("jobs:write")).Post("/jobs/batch", a.handleEnqueueBatch)
			jobs.With(auth.RequirePermission("jobs:read")).Get("/jobs", a.handleListJobs)
			jobs.With(auth.RequirePermission("jobs:read")).Get("/jobs/{id}", a.handleGetJob)
			jobs.With(auth.RequirePermission("jobs:write")).Post("/jobs/{id}/cancel", a.handleCancelJob)
			jobs.With(auth.RequirePermission("deadletters:write")).Post("/jobs/{id}/replay", a.handleReplayJob)
		})

		// Dead-letters
		v1.Group(func(dl chi.Router) {
			dl.With(auth.RequirePermission("jobs:read")).Get("/dead-letters", a.handleListDeadLetters)
		})

		// Schedules
		v1.Group(func(sched chi.Router) {
			sched.With(auth.RequirePermission("schedules:write")).Post("/schedules", a.handleCreateSchedule)
			sched.With(auth.RequirePermission("schedules:read")).Get("/schedules", a.handleListSchedules)
			sched.With(auth.RequirePermission("schedules:read")).Get("/schedules/{id}", a.handleGetSchedule)
			sched.With(auth.RequirePermission("schedules:write")).Patch("/schedules/{id}", a.handlePatchSchedule)
			sched.With(auth.RequirePermission("schedules:write")).Delete("/schedules/{id}", a.handleDeleteSchedule)
		})

		// Queues & Diagnostics
		v1.Group(func(diag chi.Router) {
			diag.With(auth.RequirePermission("jobs:read")).Get("/queues/{queue}/stats", a.handleQueueStats)
			diag.With(auth.RequirePermission("jobs:read")).Get("/workers", a.handleListWorkers)
			diag.With(auth.RequirePermission("jobs:read")).Get("/tenant/limits", a.handleGetTenantLimits)
		})
	})

	return r
}

func (a *API) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowed := false
		if len(a.cfg.AllowedOrigins) == 1 && a.cfg.AllowedOrigins[0] == "*" {
			allowed = true
			w.Header().Set("Access-Control-Allow-Origin", "*")
		} else {
			for _, o := range a.cfg.AllowedOrigins {
				if o == origin {
					allowed = true
					w.Header().Set("Access-Control-Allow-Origin", origin)
					break
				}
			}
		}

		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, X-Request-ID")
		w.Header().Set("Access-Control-Expose-Headers", "Idempotent-Replay, X-Request-ID, Retry-After")

		if r.Method == http.MethodOptions {
			if allowed {
				w.WriteHeader(http.StatusNoContent)
			} else {
				w.WriteHeader(http.StatusForbidden)
			}
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (a *API) metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)

		duration := time.Since(start).Seconds()
		route := chi.RouteContext(r.Context()).RoutePattern()
		if route == "" {
			route = "unmatched"
		}
		statusClass := fmt.Sprintf("%dxx", ww.Status()/100)

		if a.metrics != nil {
			a.metrics.APIRequestsTotal.WithLabelValues(route, r.Method, statusClass).Inc()
			a.metrics.APIRequestDurationSeconds.WithLabelValues(route, r.Method).Observe(duration)
		}
	})
}

func (a *API) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *API) handleReadyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := a.db.Ping(ctx); err != nil {
		writeError(w, r, http.StatusServiceUnavailable, "database_unavailable", "Database ping failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

type EnqueueRequest struct {
	TaskType       string          `json:"task_type"`
	Queue          string          `json:"queue"`
	Payload        json.RawMessage `json:"payload"`
	Priority       *int16          `json:"priority"`
	RunAt          *time.Time      `json:"run_at"`
	MaxAttempts    *int            `json:"max_attempts"`
	TimeoutSeconds *int            `json:"timeout_seconds"`
}

func (a *API) handleEnqueueJob(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.GetPrincipal(r.Context())

	// Read body with limit
	r.Body = http.MaxBytesReader(w, r.Body, a.cfg.MaxRequestBytes)
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, r, http.StatusRequestEntityTooLarge, "payload_too_large", "Request body exceeds maximum size")
		return
	}

	var req EnqueueRequest
	decoder := json.NewDecoder(strings.NewReader(string(bodyBytes)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_json", fmt.Sprintf("Malformed request: %v", err))
		return
	}

	if req.TaskType == "" || len(req.TaskType) > 128 {
		writeError(w, r, http.StatusBadRequest, "invalid_task_type", "task_type is required and must be <= 128 characters")
		return
	}

	queue := "default"
	if req.Queue != "" {
		queue = req.Queue
	}
	if !a.cfg.IsQueueAllowed(queue) {
		writeError(w, r, http.StatusBadRequest, "invalid_queue", fmt.Sprintf("Queue %q is not in the allowed list", queue))
		return
	}

	if len(req.Payload) == 0 {
		req.Payload = []byte("{}")
	}
	if int64(len(req.Payload)) > a.cfg.MaxPayloadBytes {
		writeError(w, r, http.StatusRequestEntityTooLarge, "payload_too_large", "Job payload exceeds maximum size")
		return
	}

	priority := int16(5)
	if req.Priority != nil {
		if *req.Priority < 0 || *req.Priority > 9 {
			writeError(w, r, http.StatusBadRequest, "invalid_priority", "priority must be between 0 and 9")
			return
		}
		priority = *req.Priority
	}

	maxAttempts := a.cfg.DefaultMaxAttempts
	if req.MaxAttempts != nil {
		if *req.MaxAttempts < 1 || *req.MaxAttempts > 10 {
			writeError(w, r, http.StatusBadRequest, "invalid_max_attempts", "max_attempts must be between 1 and 10")
			return
		}
		maxAttempts = *req.MaxAttempts
	}

	timeoutSec := a.cfg.DefaultJobTimeoutSeconds
	if req.TimeoutSeconds != nil {
		if *req.TimeoutSeconds < 1 || *req.TimeoutSeconds > a.cfg.MaxJobTimeoutSeconds {
			writeError(w, r, http.StatusBadRequest, "invalid_timeout_seconds", fmt.Sprintf("timeout_seconds must be between 1 and %d", a.cfg.MaxJobTimeoutSeconds))
			return
		}
		timeoutSec = *req.TimeoutSeconds
	}

	var runAt time.Time
	isExplicitRunAt := false
	if req.RunAt != nil {
		runAt = req.RunAt.UTC()
		isExplicitRunAt = true
		if runAt.After(time.Now().UTC().Add(a.cfg.MaxScheduleHorizon)) {
			writeError(w, r, http.StatusBadRequest, "run_at_beyond_horizon", "run_at cannot exceed the maximum future scheduling horizon")
			return
		}
	} else {
		runAt = time.Now().UTC()
	}

	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(idempotencyKey) > 200 {
		writeError(w, r, http.StatusBadRequest, "invalid_idempotency_key", "Idempotency-Key header cannot exceed 200 characters")
		return
	}

	traceID := domain.ResolveTraceID(r.Header.Get("traceparent"), r.Header.Get("X-Request-ID"))
	res, err := a.jobRepo.EnqueueJob(r.Context(), postgres.EnqueueParams{
		TraceID:         traceID,
		TenantID:        principal.TenantID,
		TaskType:        req.TaskType,
		QueueName:       queue,
		Payload:         req.Payload,
		Priority:        priority,
		RunAt:           runAt,
		IsExplicitRunAt: isExplicitRunAt,
		MaxAttempts:     maxAttempts,
		TimeoutSeconds:  timeoutSec,
		IdempotencyKey:  idempotencyKey,
		IdempotencyTTL:  a.cfg.IdempotencyTTL,
	})

	if err != nil {
		if errors.Is(err, domain.ErrIdempotencyConflict) {
			writeError(w, r, http.StatusConflict, "idempotency_key_reused", "The idempotency key was already used for a different request.")
			return
		}
		if errors.Is(err, domain.ErrRateLimited) {
			if res != nil && res.RetryAfterHint > 0 {
				w.Header().Set("Retry-After", strconv.Itoa(int(res.RetryAfterHint.Seconds())+1))
			}
			if a.metrics != nil {
				a.metrics.TenantRateLimitRejectionsTotal.WithLabelValues("rate_bucket_empty").Inc()
			}
			writeError(w, r, http.StatusTooManyRequests, "rate_limit_exceeded", "Tenant enqueue rate limit exceeded")
			return
		}
		if errors.Is(err, domain.ErrQuotaExceeded) {
			if a.metrics != nil {
				a.metrics.TenantRateLimitRejectionsTotal.WithLabelValues("pending_quota_exceeded").Inc()
			}
			writeError(w, r, http.StatusTooManyRequests, "pending_quota_exceeded", "Tenant maximum pending jobs quota reached")
			return
		}
		slog.Error("enqueue failed", "error", err, "tenant_id", principal.TenantID)
		writeError(w, r, http.StatusInternalServerError, "enqueue_failed", "Failed to enqueue job")
		return
	}

	if a.metrics != nil && !res.IdempotentReplay {
		a.metrics.JobsEnqueuedTotal.WithLabelValues(queue).Inc()
	}

	responseDTO := map[string]any{
		"id":         res.Job.ID.String(),
		"status":     res.Job.Status,
		"created_at": res.Job.CreatedAt.Format(time.RFC3339Nano),
		"run_at":     res.Job.RunAt.Format(time.RFC3339Nano),
		"status_url": fmt.Sprintf("/v1/jobs/%s", res.Job.ID.String()),
		"trace_id":   res.Job.TraceID,
	}
	w.Header().Set("X-Trace-Id", res.Job.TraceID)
	slog.Info("job enqueued", "job_id", res.Job.ID, "trace_id", res.Job.TraceID, "tenant_id", principal.TenantID, "queue", queue, "idempotent_replay", res.IdempotentReplay)

	if res.IdempotentReplay {
		w.Header().Set("Idempotent-Replay", "true")
		writeJSON(w, http.StatusOK, responseDTO)
	} else {
		writeJSON(w, http.StatusAccepted, responseDTO)
	}
}

func (a *API) handleGetJob(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.GetPrincipal(r.Context())
	jobIDStr := chi.URLParam(r, "id")
	jobID, err := uuid.Parse(jobIDStr)
	if err != nil {
		writeError(w, r, http.StatusNotFound, "not_found", "Job not found")
		return
	}

	job, err := a.jobRepo.GetJob(r.Context(), principal.TenantID, jobID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "not_found", "Job not found")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal_error", "Failed to retrieve job")
		return
	}

	attempts, _ := a.jobRepo.GetJobAttempts(r.Context(), principal.TenantID, jobID)

	responseDTO := map[string]any{
		"id":                        job.ID.String(),
		"queue":                     job.QueueName,
		"task_type":                 job.TaskType,
		"payload":                   job.Payload,
		"result":                    job.Result,
		"status":                    job.Status,
		"priority":                  job.Priority,
		"run_at":                    job.RunAt.Format(time.RFC3339Nano),
		"created_at":                job.CreatedAt.Format(time.RFC3339Nano),
		"updated_at":                job.UpdatedAt.Format(time.RFC3339Nano),
		"started_at":                job.StartedAt,
		"completed_at":              job.CompletedAt,
		"attempts_started":          job.AttemptsStarted,
		"max_attempts":              job.MaxAttempts,
		"timeout_seconds":           job.TimeoutSeconds,
		"cancellation_requested_at": job.CancellationRequestedAt,
		"last_error_code":           job.LastErrorCode,
		"last_error_message":        job.LastErrorMessage,
		"last_error_at":             job.LastErrorAt,
		"schedule_id":               job.ScheduleID,
		"scheduled_for":             job.ScheduledFor,
		"replay_generation":         job.ReplayGeneration,
		"trace_id":                  job.TraceID,
		"attempts":                  attempts,
	}

	writeJSON(w, http.StatusOK, responseDTO)
}

func (a *API) handleListJobs(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.GetPrincipal(r.Context())

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil {
			limit = parsed
		}
	}

	var status *domain.JobStatus
	if s := r.URL.Query().Get("status"); s != "" {
		st := domain.JobStatus(s)
		status = &st
	}

	var queue *string
	if q := r.URL.Query().Get("queue"); q != "" {
		queue = &q
	}

	var taskType *string
	if t := r.URL.Query().Get("task_type"); t != "" {
		taskType = &t
	}

	var cursor *time.Time
	if c := r.URL.Query().Get("cursor"); c != "" {
		if parsedTime, err := time.Parse(time.RFC3339Nano, c); err == nil {
			cursor = &parsedTime
		}
	}

	jobs, err := a.jobRepo.ListJobs(r.Context(), postgres.ListJobsFilter{
		TenantID: principal.TenantID,
		Status:   status,
		Queue:    queue,
		TaskType: taskType,
		Limit:    limit,
		Cursor:   cursor,
	})
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "query_failed", "Failed to list jobs")
		return
	}

	var nextCursor *string
	if len(jobs) == limit {
		lastTime := jobs[len(jobs)-1].CreatedAt.Format(time.RFC3339Nano)
		nextCursor = &lastTime
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"jobs":        jobs,
		"next_cursor": nextCursor,
	})
}

func (a *API) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.GetPrincipal(r.Context())
	jobIDStr := chi.URLParam(r, "id")
	jobID, err := uuid.Parse(jobIDStr)
	if err != nil {
		writeError(w, r, http.StatusNotFound, "not_found", "Job not found")
		return
	}

	job, err := a.jobRepo.CancelJob(r.Context(), principal.TenantID, jobID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "not_found", "Job not found")
			return
		}
		if errors.Is(err, domain.ErrInvalidState) {
			writeError(w, r, http.StatusConflict, "cannot_cancel", fmt.Sprintf("Job is in terminal state %q and cannot be cancelled", job.Status))
			return
		}
		writeError(w, r, http.StatusInternalServerError, "cancel_failed", "Failed to cancel job")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":                        job.ID.String(),
		"status":                    job.Status,
		"cancellation_requested_at": job.CancellationRequestedAt,
	})
}

func (a *API) handleReplayJob(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.GetPrincipal(r.Context())
	jobIDStr := chi.URLParam(r, "id")
	jobID, err := uuid.Parse(jobIDStr)
	if err != nil {
		writeError(w, r, http.StatusNotFound, "not_found", "Job not found")
		return
	}

	var body struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Reason == "" {
		body.Reason = "Manual operator replay"
	}

	job, err := a.jobRepo.ReplayDeadJob(r.Context(), principal.TenantID, jobID, &principal.APIKeyID, body.Reason)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "not_found", "Job not found")
			return
		}
		if errors.Is(err, domain.ErrInvalidState) {
			writeError(w, r, http.StatusConflict, "cannot_replay", "Only jobs in 'dead' state can be replayed")
			return
		}
		if errors.Is(err, domain.ErrQuotaExceeded) {
			writeError(w, r, http.StatusTooManyRequests, "pending_quota_exceeded", "Cannot replay: tenant outstanding job quota exceeded")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "replay_failed", fmt.Sprintf("Failed to replay job: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":                job.ID.String(),
		"status":            job.Status,
		"replay_generation": job.ReplayGeneration,
		"trace_id":          job.TraceID,
		"message":           "Job successfully replayed to queued state",
	})
}

func (a *API) handleListDeadLetters(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.GetPrincipal(r.Context())
	deadStatus := domain.StatusDead

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil {
			limit = parsed
		}
	}

	var queue *string
	if q := r.URL.Query().Get("queue"); q != "" {
		queue = &q
	}

	jobs, err := a.jobRepo.ListJobs(r.Context(), postgres.ListJobsFilter{
		TenantID: principal.TenantID,
		Status:   &deadStatus,
		Queue:    queue,
		Limit:    limit,
	})
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "query_failed", "Failed to list dead letters")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"dead_letters": jobs})
}

type CreateScheduleRequest struct {
	Name           string          `json:"name"`
	Queue          string          `json:"queue"`
	TaskType       string          `json:"task_type"`
	Payload        json.RawMessage `json:"payload"`
	CronExpression string          `json:"cron_expression"`
	Timezone       string          `json:"timezone"`
	Priority       *int16          `json:"priority"`
	MaxAttempts    *int            `json:"max_attempts"`
	TimeoutSeconds *int            `json:"timeout_seconds"`
	MisfirePolicy  string          `json:"misfire_policy"`
}

func (a *API) handleCreateSchedule(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.GetPrincipal(r.Context())

	var req CreateScheduleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_json", "Malformed JSON body")
		return
	}

	if req.Name == "" || len(req.Name) > 128 {
		writeError(w, r, http.StatusBadRequest, "invalid_name", "Schedule name required and must be <= 128 chars")
		return
	}
	if req.TaskType == "" {
		writeError(w, r, http.StatusBadRequest, "invalid_task_type", "task_type is required")
		return
	}
	if req.CronExpression == "" {
		writeError(w, r, http.StatusBadRequest, "invalid_cron_expression", "cron_expression is required")
		return
	}
	if req.Timezone == "" {
		req.Timezone = "UTC"
	}

	queue := "default"
	if req.Queue != "" {
		queue = req.Queue
	}
	if !a.cfg.IsQueueAllowed(queue) {
		writeError(w, r, http.StatusBadRequest, "invalid_queue", "Queue is not allowed")
		return
	}

	misfirePolicy := domain.MisfirePolicy(req.MisfirePolicy)
	if misfirePolicy == "" {
		misfirePolicy = domain.MisfireSkip
	}
	if misfirePolicy != domain.MisfireSkip && misfirePolicy != domain.MisfireCatchUpOne {
		writeError(w, r, http.StatusBadRequest, "invalid_misfire_policy", "misfire_policy must be 'skip' or 'catch_up_one'")
		return
	}

	priority := int16(5)
	if req.Priority != nil {
		priority = *req.Priority
	}
	maxAttempts := a.cfg.DefaultMaxAttempts
	if req.MaxAttempts != nil {
		maxAttempts = *req.MaxAttempts
	}
	timeoutSec := a.cfg.DefaultJobTimeoutSeconds
	if req.TimeoutSeconds != nil {
		timeoutSec = *req.TimeoutSeconds
	}

	sched, err := a.scheduleRepo.CreateSchedule(r.Context(), postgres.CreateScheduleParams{
		TenantID:       principal.TenantID,
		Name:           req.Name,
		QueueName:      queue,
		TaskType:       req.TaskType,
		Payload:        req.Payload,
		CronExpression: req.CronExpression,
		Timezone:       req.Timezone,
		Priority:       priority,
		MaxAttempts:    maxAttempts,
		TimeoutSeconds: timeoutSec,
		MisfirePolicy:  misfirePolicy,
	})
	if err != nil {
		if errors.Is(err, domain.ErrQuotaExceeded) {
			writeError(w, r, http.StatusTooManyRequests, "schedule_quota_exceeded", "Maximum active schedules quota exceeded for this tenant")
			return
		}
		writeError(w, r, http.StatusBadRequest, "schedule_creation_failed", err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, sched)
}

func (a *API) handleListSchedules(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.GetPrincipal(r.Context())
	schedules, err := a.scheduleRepo.ListSchedules(r.Context(), principal.TenantID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "query_failed", "Failed to list schedules")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schedules": schedules})
}

func (a *API) handleGetSchedule(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.GetPrincipal(r.Context())
	schedID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, r, http.StatusNotFound, "not_found", "Schedule not found")
		return
	}

	sched, err := a.scheduleRepo.GetSchedule(r.Context(), principal.TenantID, schedID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "not_found", "Schedule not found")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal_error", "Failed to retrieve schedule")
		return
	}
	writeJSON(w, http.StatusOK, sched)
}

func (a *API) handlePatchSchedule(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.GetPrincipal(r.Context())
	schedID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, r, http.StatusNotFound, "not_found", "Schedule not found")
		return
	}

	var body struct {
		Paused *bool `json:"paused"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Paused == nil {
		writeError(w, r, http.StatusBadRequest, "invalid_input", "Expected JSON with boolean 'paused' field")
		return
	}

	sched, err := a.scheduleRepo.SetSchedulePaused(r.Context(), principal.TenantID, schedID, *body.Paused)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "not_found", "Schedule not found")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "update_failed", "Failed to update schedule")
		return
	}

	writeJSON(w, http.StatusOK, sched)
}

func (a *API) handleDeleteSchedule(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.GetPrincipal(r.Context())
	schedID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, r, http.StatusNotFound, "not_found", "Schedule not found")
		return
	}

	if err := a.scheduleRepo.DisableSchedule(r.Context(), principal.TenantID, schedID); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "not_found", "Schedule not found")
			return
		}
		writeError(w, r, http.StatusInternalServerError, "delete_failed", "Failed to disable schedule")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "Schedule disabled"})
}

func (a *API) handleQueueStats(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.GetPrincipal(r.Context())
	queueName := chi.URLParam(r, "queue")
	if !a.cfg.IsQueueAllowed(queueName) {
		writeError(w, r, http.StatusNotFound, "invalid_queue", "Queue not found in allowlist")
		return
	}

	stats, err := a.jobRepo.GetQueueStats(r.Context(), principal.TenantID, queueName)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "query_failed", "Failed to get queue stats")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"queue": queueName,
		"stats": stats,
	})
}

// handleListWorkers lists live workers. Workers are SHARED infrastructure, so what a tenant may
// see is limited: ordinary tenants get liveness information only (id, queues, timestamps,
// draining). Hostnames, versions, process names, handled task types (which can reveal other
// tenants' job names) and metadata require the workers:read permission (or admin).
func (a *API) handleListWorkers(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.GetPrincipal(r.Context())
	workers, err := a.workerRepo.ListActiveWorkers(r.Context(), 1*time.Minute)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "query_failed", "Failed to list active workers")
		return
	}
	if principal == nil || !principal.HasPermission("workers:read") {
		for i := range workers {
			workers[i].ProcessName, workers[i].Hostname, workers[i].Version = "", "", ""
			workers[i].TaskTypes, workers[i].Metadata = nil, nil
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"workers": workers})
}

func (a *API) handleGetTenantLimits(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.GetPrincipal(r.Context())
	limits, err := a.tenantRepo.GetLimits(r.Context(), principal.TenantID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "query_failed", "Failed to get limits")
		return
	}
	writeJSON(w, http.StatusOK, limits)
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	reqID := middleware.GetReqID(r.Context())
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"code":       code,
			"message":    message,
			"request_id": reqID,
		},
	})
}
