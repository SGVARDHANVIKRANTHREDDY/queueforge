// Package outbox is the PUBLIC way to enqueue a QueueForge job inside YOUR OWN PostgreSQL
// transaction (the transactional-outbox pattern). Use it when your application and QueueForge share
// one PostgreSQL database:
//
//	tx, _ := pool.Begin(ctx)
//	defer tx.Rollback(ctx)
//	_, _ = tx.Exec(ctx, `INSERT INTO orders ...`)                       // your business write
//	job, err := enq.EnqueueTx(ctx, tx, outbox.Params{TenantID: tid, TaskType: "email.send", Payload: p})
//	if err != nil { return err }                                        // quota/rate-limit rejections are errors
//	return tx.Commit(ctx)                                               // order AND job commit together
//
// The job exists if and only if your transaction commits. A rate-limit, quota or idempotency
// rejection undoes only the enqueue (it runs inside a savepoint) and leaves your transaction usable.
// Workers are notified when you commit. Over HTTP this is not possible; use the SDKs for that.
package outbox

import (
	"context"
	"encoding/json"
	"time"

	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/domain"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Errors returned by EnqueueTx (compare with errors.Is).
var (
	ErrRateLimited         = domain.ErrRateLimited
	ErrQuotaExceeded       = domain.ErrQuotaExceeded
	ErrIdempotencyConflict = domain.ErrIdempotencyConflict
)

// Params describes the job. TenantID and TaskType are required; everything else has a default.
type Params struct {
	// TenantID: required.
	TenantID uuid.UUID
	// TaskType: required.
	TaskType string
	// Payload: default {}.
	Payload json.RawMessage
	// Queue: default "default".
	Queue string
	// Priority: 0-9, default 5 unless PriorityIsSet.
	Priority int16
	// PriorityIsSet: set true to use priority 0.
	PriorityIsSet bool
	// MaxAttempts: default 5.
	MaxAttempts int
	// TimeoutSeconds: default 300.
	TimeoutSeconds int
	// RunAt: zero means as soon as possible.
	RunAt time.Time
	// IdempotencyKey: optional; same key and same body returns the original job.
	IdempotencyKey string
	// IdempotencyTTL: default 24h.
	IdempotencyTTL time.Duration
}

// Job is the result of an enqueue.
type Job struct {
	ID       uuid.UUID
	Status   string
	Replayed bool // true when an identical earlier request (same idempotency key) was returned
}

// Enqueuer enqueues jobs using your connection pool; it does not open its own connections.
type Enqueuer struct{ repo *postgres.JobRepo }

// New returns an Enqueuer. The pool must point at the QueueForge database (migrations applied).
func New(pool *pgxpool.Pool) *Enqueuer {
	return &Enqueuer{repo: postgres.NewJobRepo(&postgres.DB{Pool: pool})}
}

// EnqueueTx enqueues a job on tx, which you own and must commit.
func (e *Enqueuer) EnqueueTx(ctx context.Context, tx pgx.Tx, p Params) (*Job, error) {
	payload := p.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	queue := p.Queue
	if queue == "" {
		queue = "default"
	}
	priority := p.Priority
	if priority == 0 && !p.PriorityIsSet {
		priority = 5
	}
	maxAttempts := p.MaxAttempts
	if maxAttempts == 0 {
		maxAttempts = 5
	}
	timeout := p.TimeoutSeconds
	if timeout == 0 {
		timeout = 300
	}
	ttl := p.IdempotencyTTL
	if ttl == 0 {
		ttl = 24 * time.Hour
	}
	res, err := e.repo.EnqueueJobTx(ctx, tx, postgres.EnqueueParams{
		TenantID: p.TenantID, TaskType: p.TaskType, QueueName: queue, Payload: payload,
		Priority: priority, RunAt: p.RunAt, IsExplicitRunAt: !p.RunAt.IsZero(),
		MaxAttempts: maxAttempts, TimeoutSeconds: timeout,
		IdempotencyKey: p.IdempotencyKey, IdempotencyTTL: ttl,
	})
	if err != nil {
		return nil, err
	}
	return &Job{ID: res.Job.ID, Status: string(res.Job.Status), Replayed: res.IdempotentReplay}, nil
}
