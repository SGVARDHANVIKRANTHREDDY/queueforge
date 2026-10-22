package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"queueforge/internal/auth"
	"queueforge/internal/domain"
	"queueforge/internal/postgres"
)

// maxBatchJobs bounds one batch request. 100 jobs commit in a single transaction.
const maxBatchJobs = 100

// BatchEnqueueRequest is the body of POST /v1/jobs/batch.
type BatchEnqueueRequest struct {
	Jobs []EnqueueRequest `json:"jobs"`
}

// handleEnqueueBatch accepts up to maxBatchJobs jobs and enqueues them atomically (all or none).
//
// Idempotency: send one Idempotency-Key header for the whole batch. Job i is stored under the key
// "<header>#<i>", so retrying the identical batch returns the original jobs instead of creating
// duplicates, and reusing the key with a different batch is a 409.
func (a *API) handleEnqueueBatch(w http.ResponseWriter, r *http.Request) {
	principal, _ := auth.GetPrincipal(r.Context())

	r.Body = http.MaxBytesReader(w, r.Body, a.cfg.MaxRequestBytes)
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, r, http.StatusRequestEntityTooLarge, "payload_too_large", "Request body exceeds maximum size")
		return
	}
	var req BatchEnqueueRequest
	decoder := json.NewDecoder(strings.NewReader(string(bodyBytes)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_json", fmt.Sprintf("Malformed request: %v", err))
		return
	}
	if len(req.Jobs) == 0 || len(req.Jobs) > maxBatchJobs {
		writeError(w, r, http.StatusBadRequest, "invalid_batch_size", fmt.Sprintf("jobs must contain between 1 and %d entries", maxBatchJobs))
		return
	}

	batchKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(batchKey) > 190 {
		writeError(w, r, http.StatusBadRequest, "invalid_idempotency_key", "Idempotency-Key header cannot exceed 190 characters")
		return
	}
	traceID := domain.ResolveTraceID(r.Header.Get("traceparent"), r.Header.Get("X-Request-ID"))

	params := make([]postgres.EnqueueParams, 0, len(req.Jobs))
	for i := range req.Jobs {
		p, code, msg := a.batchItemParams(&req.Jobs[i], principal.TenantID, traceID)
		if code != "" {
			status := http.StatusBadRequest
			if code == "payload_too_large" {
				status = http.StatusRequestEntityTooLarge
			}
			writeError(w, r, status, code, fmt.Sprintf("jobs[%d]: %s", i, msg))
			return
		}
		if batchKey != "" {
			p.IdempotencyKey = batchKey + "#" + strconv.Itoa(i)
		}
		params = append(params, p)
	}

	results, failed, err := a.jobRepo.EnqueueBatch(r.Context(), params)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrIdempotencyConflict):
			writeError(w, r, http.StatusConflict, "idempotency_key_reused", "The idempotency key was already used for a different request.")
		case errors.Is(err, domain.ErrRateLimited):
			if failed != nil && failed.RetryAfterHint > 0 {
				w.Header().Set("Retry-After", strconv.Itoa(int(failed.RetryAfterHint.Seconds())+1))
			}
			if a.metrics != nil {
				a.metrics.TenantRateLimitRejectionsTotal.WithLabelValues("rate_bucket_empty").Inc()
			}
			writeError(w, r, http.StatusTooManyRequests, "rate_limit_exceeded", "Tenant enqueue rate limit exceeded; no job in the batch was accepted")
		case errors.Is(err, domain.ErrQuotaExceeded):
			if a.metrics != nil {
				a.metrics.TenantRateLimitRejectionsTotal.WithLabelValues("pending_quota_exceeded").Inc()
			}
			writeError(w, r, http.StatusTooManyRequests, "pending_quota_exceeded", "Tenant maximum pending jobs quota reached; no job in the batch was accepted")
		default:
			slog.Error("batch enqueue failed", "error", err, "tenant_id", principal.TenantID)
			writeError(w, r, http.StatusInternalServerError, "enqueue_failed", "Failed to enqueue batch")
		}
		return
	}

	out := make([]map[string]any, 0, len(results))
	allReplay := true
	for i, res := range results {
		if !res.IdempotentReplay {
			allReplay = false
			if a.metrics != nil {
				a.metrics.JobsEnqueuedTotal.WithLabelValues(params[i].QueueName).Inc()
			}
		}
		out = append(out, map[string]any{
			"id":         res.Job.ID.String(),
			"status":     res.Job.Status,
			"created_at": res.Job.CreatedAt.Format(time.RFC3339Nano),
			"run_at":     res.Job.RunAt.Format(time.RFC3339Nano),
			"status_url": fmt.Sprintf("/v1/jobs/%s", res.Job.ID.String()),
			"trace_id":   res.Job.TraceID,
		})
	}
	slog.Info("batch enqueued", "tenant_id", principal.TenantID, "count", len(results), "idempotent_replay", allReplay)
	if allReplay {
		w.Header().Set("Idempotent-Replay", "true")
		writeJSON(w, http.StatusOK, map[string]any{"jobs": out})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"jobs": out})
}

// batchItemParams validates one batch entry with the same rules as POST /v1/jobs. It returns a
// non-empty error code when the entry is invalid.
func (a *API) batchItemParams(req *EnqueueRequest, tenantID uuid.UUID, traceID string) (postgres.EnqueueParams, string, string) {
	var zero postgres.EnqueueParams
	if req.TaskType == "" || len(req.TaskType) > 128 {
		return zero, "invalid_task_type", "task_type is required and must be <= 128 characters"
	}
	queue := "default"
	if req.Queue != "" {
		queue = req.Queue
	}
	if !a.cfg.IsQueueAllowed(queue) {
		return zero, "invalid_queue", fmt.Sprintf("queue %q is not in the allowed list", queue)
	}
	if len(req.Payload) == 0 {
		req.Payload = []byte("{}")
	}
	if int64(len(req.Payload)) > a.cfg.MaxPayloadBytes {
		return zero, "payload_too_large", "job payload exceeds maximum size"
	}
	priority := int16(5)
	if req.Priority != nil {
		if *req.Priority < 0 || *req.Priority > 9 {
			return zero, "invalid_priority", "priority must be between 0 and 9"
		}
		priority = *req.Priority
	}
	maxAttempts := a.cfg.DefaultMaxAttempts
	if req.MaxAttempts != nil {
		if *req.MaxAttempts < 1 || *req.MaxAttempts > 10 {
			return zero, "invalid_max_attempts", "max_attempts must be between 1 and 10"
		}
		maxAttempts = *req.MaxAttempts
	}
	timeoutSec := a.cfg.DefaultJobTimeoutSeconds
	if req.TimeoutSeconds != nil {
		if *req.TimeoutSeconds < 1 || *req.TimeoutSeconds > a.cfg.MaxJobTimeoutSeconds {
			return zero, "invalid_timeout_seconds", fmt.Sprintf("timeout_seconds must be between 1 and %d", a.cfg.MaxJobTimeoutSeconds)
		}
		timeoutSec = *req.TimeoutSeconds
	}
	var runAt time.Time
	explicit := false
	if req.RunAt != nil {
		runAt = req.RunAt.UTC()
		explicit = true
		if runAt.After(time.Now().UTC().Add(a.cfg.MaxScheduleHorizon)) {
			return zero, "run_at_beyond_horizon", "run_at cannot exceed the maximum future scheduling horizon"
		}
	}
	return postgres.EnqueueParams{
		TraceID: traceID, TenantID: tenantID, TaskType: req.TaskType, QueueName: queue,
		Payload: req.Payload, Priority: priority, RunAt: runAt, IsExplicitRunAt: explicit,
		MaxAttempts: maxAttempts, TimeoutSeconds: timeoutSec, IdempotencyTTL: a.cfg.IdempotencyTTL,
	}, "", ""
}
