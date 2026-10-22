package postgres

import (
	"context"
	"log/slog"
	"time"
)

// JobsChannel is the Postgres LISTEN/NOTIFY channel that EnqueueJob signals on commit.
const JobsChannel = "qf_jobs"

// ListenForJobs blocks until ctx is cancelled, sending a (non-blocking) signal on wake every time
// a job is enqueued. It holds one dedicated connection for LISTEN and transparently reconnects.
//
// NOTIFY is only an optimisation: it removes the average poll-interval/2 pickup latency. Workers
// must still poll, because notifications are best-effort (a worker may be disconnected when one
// is sent) and delayed/retried jobs become runnable with the passage of time, not an event.
func (r *JobRepo) ListenForJobs(ctx context.Context, wake chan<- struct{}) {
	for ctx.Err() == nil {
		if err := r.listenOnce(ctx, wake); err != nil && ctx.Err() == nil {
			slog.Warn("job listener disconnected; retrying", "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
	}
}

func (r *JobRepo) listenOnce(ctx context.Context, wake chan<- struct{}) error {
	conn, err := r.db.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "LISTEN "+JobsChannel); err != nil {
		return err
	}
	for {
		if _, err := conn.Conn().WaitForNotification(ctx); err != nil {
			return err
		}
		select {
		case wake <- struct{}{}:
		default: // a wake-up is already pending; coalesce
		}
	}
}
