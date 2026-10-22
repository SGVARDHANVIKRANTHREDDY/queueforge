package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type JobStatus string

const (
	StatusQueued    JobStatus = "queued"
	StatusRunning   JobStatus = "running"
	StatusSucceeded JobStatus = "succeeded"
	StatusDead      JobStatus = "dead"
	StatusCancelled JobStatus = "cancelled"
)

type AttemptOutcome string

const (
	OutcomeSucceeded      AttemptOutcome = "succeeded"
	OutcomeRetryableError AttemptOutcome = "retryable_error"
	OutcomePermanentError AttemptOutcome = "permanent_error"
	OutcomeLeaseExpired   AttemptOutcome = "lease_expired"
	OutcomeCancelled      AttemptOutcome = "cancelled"
)

type MisfirePolicy string

const (
	MisfireSkip       MisfirePolicy = "skip"
	MisfireCatchUpOne MisfirePolicy = "catch_up_one"
)

type TenantStatus string

const (
	TenantActive    TenantStatus = "active"
	TenantSuspended TenantStatus = "suspended"
)

type Tenant struct {
	ID        uuid.UUID    `json:"id"`
	Name      string       `json:"name"`
	Status    TenantStatus `json:"status"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
}

type APIKey struct {
	ID            uuid.UUID  `json:"id"`
	TenantID      uuid.UUID  `json:"tenant_id"`
	Name          string     `json:"name"`
	SecretHash    []byte     `json:"-"`
	DisplayPrefix string     `json:"display_prefix"`
	Permissions   []string   `json:"permissions"`
	CreatedAt     time.Time  `json:"created_at"`
	LastUsedAt    *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	RevokedAt     *time.Time `json:"revoked_at,omitempty"`
}

func (k *APIKey) IsActive() bool {
	if k.RevokedAt != nil {
		return false
	}
	// Expiry is enforced by the database clock inside LookupAPIKeyBySecret (ADR-014); this server's
	// clock is deliberately not consulted here.
	return true
}

func (k *APIKey) HasPermission(perm string) bool {
	for _, p := range k.Permissions {
		if p == perm || p == "*" || p == "admin" {
			return true
		}
	}
	return false
}

type TenantLimits struct {
	TenantID             uuid.UUID `json:"tenant_id"`
	EnqueueRatePerSecond float64   `json:"enqueue_rate_per_second"`
	EnqueueBurstCapacity float64   `json:"enqueue_burst_capacity"`
	MaxPendingJobs       int       `json:"max_pending_jobs"`
	MaxConcurrentJobs    int       `json:"max_concurrent_jobs"`
	MaxSchedules         int       `json:"max_schedules"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type TenantRateBucket struct {
	TenantID     uuid.UUID `json:"tenant_id"`
	Tokens       float64   `json:"tokens"`
	LastRefillAt time.Time `json:"last_refill_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type TenantUsage struct {
	TenantID        uuid.UUID `json:"tenant_id"`
	OutstandingJobs int64     `json:"outstanding_jobs"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type TenantExecutionSlot struct {
	TenantID     uuid.UUID  `json:"tenant_id"`
	SlotNo       int        `json:"slot_no"`
	CurrentJobID *uuid.UUID `json:"current_job_id,omitempty"`
	LeaseToken   *uuid.UUID `json:"lease_token,omitempty"`
	Enabled      bool       `json:"enabled"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

type Job struct {
	ID                      uuid.UUID       `json:"id"`
	TenantID                uuid.UUID       `json:"tenant_id"`
	QueueName               string          `json:"queue_name"`
	TaskType                string          `json:"task_type"`
	Payload                 json.RawMessage `json:"payload"`
	Result                  json.RawMessage `json:"result,omitempty"`
	Status                  JobStatus       `json:"status"`
	Priority                int16           `json:"priority"`
	RunAt                   time.Time       `json:"run_at"`
	CreatedAt               time.Time       `json:"created_at"`
	UpdatedAt               time.Time       `json:"updated_at"`
	StartedAt               *time.Time      `json:"started_at,omitempty"`
	CompletedAt             *time.Time      `json:"completed_at,omitempty"`
	AttemptsStarted         int             `json:"attempts_started"`
	MaxAttempts             int             `json:"max_attempts"`
	TimeoutSeconds          int             `json:"timeout_seconds"`
	LeaseToken              *uuid.UUID      `json:"lease_token,omitempty"`
	WorkerID                *uuid.UUID      `json:"worker_id,omitempty"`
	LeaseExpiresAt          *time.Time      `json:"lease_expires_at,omitempty"`
	CancellationRequestedAt *time.Time      `json:"cancellation_requested_at,omitempty"`
	LastErrorCode           *string         `json:"last_error_code,omitempty"`
	LastErrorMessage        *string         `json:"last_error_message,omitempty"`
	LastErrorAt             *time.Time      `json:"last_error_at,omitempty"`
	ScheduleID              *uuid.UUID      `json:"schedule_id,omitempty"`
	ScheduledFor            *time.Time      `json:"scheduled_for,omitempty"`
	TraceID                 string          `json:"trace_id,omitempty"`
	ReplayGeneration        int             `json:"replay_generation"`
}

type JobAttempt struct {
	ID               uuid.UUID       `json:"id"`
	JobID            uuid.UUID       `json:"job_id"`
	TenantID         uuid.UUID       `json:"tenant_id"`
	ReplayGeneration int             `json:"replay_generation"`
	AttemptNumber    int             `json:"attempt_number"`
	WorkerID         *uuid.UUID      `json:"worker_id,omitempty"`
	LeaseToken       uuid.UUID       `json:"lease_token"`
	StartedAt        time.Time       `json:"started_at"`
	FinishedAt       *time.Time      `json:"finished_at,omitempty"`
	Outcome          *AttemptOutcome `json:"outcome,omitempty"`
	ErrorCode        *string         `json:"error_code,omitempty"`
	ErrorMessage     *string         `json:"error_message,omitempty"`
	DurationMs       *int64          `json:"duration_ms,omitempty"`
}

type IdempotencyKey struct {
	TenantID       uuid.UUID `json:"tenant_id"`
	IdempotencyKey string    `json:"idempotency_key"`
	RequestHash    []byte    `json:"request_hash"`
	JobID          uuid.UUID `json:"job_id"`
	CreatedAt      time.Time `json:"created_at"`
	ExpiresAt      time.Time `json:"expires_at"`
}

type WorkerInstance struct {
	ID          uuid.UUID       `json:"id"`
	ProcessName string          `json:"process_name"`
	Hostname    string          `json:"hostname"`
	Version     string          `json:"version"`
	Queues      []string        `json:"queues"`
	TaskTypes   []string        `json:"task_types"`
	StartedAt   time.Time       `json:"started_at"`
	LastSeenAt  time.Time       `json:"last_seen_at"`
	Draining    bool            `json:"draining"`
	Metadata    json.RawMessage `json:"metadata"`
}

type JobSchedule struct {
	ID               uuid.UUID       `json:"id"`
	TenantID         uuid.UUID       `json:"tenant_id"`
	Name             string          `json:"name"`
	QueueName        string          `json:"queue_name"`
	TaskType         string          `json:"task_type"`
	Payload          json.RawMessage `json:"payload"`
	CronExpression   string          `json:"cron_expression"`
	Timezone         string          `json:"timezone"`
	Priority         int16           `json:"priority"`
	MaxAttempts      int             `json:"max_attempts"`
	TimeoutSeconds   int             `json:"timeout_seconds"`
	MisfirePolicy    MisfirePolicy   `json:"misfire_policy"`
	NextRunAt        time.Time       `json:"next_run_at"`
	LastScheduledFor *time.Time      `json:"last_scheduled_for,omitempty"`
	PendingCatchUpAt *time.Time      `json:"pending_catch_up_at,omitempty"`
	LastSkippedAt    *time.Time      `json:"last_skipped_at,omitempty"`
	Paused           bool            `json:"paused"`
	DisabledAt       *time.Time      `json:"disabled_at,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

type AuditEvent struct {
	ID            uuid.UUID       `json:"id"`
	TenantID      uuid.UUID       `json:"tenant_id"`
	JobID         uuid.UUID       `json:"job_id"`
	ActorAPIKeyID *uuid.UUID      `json:"actor_api_key_id,omitempty"`
	EventType     string          `json:"event_type"`
	Reason        *string         `json:"reason,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	Metadata      json.RawMessage `json:"metadata"`
}
