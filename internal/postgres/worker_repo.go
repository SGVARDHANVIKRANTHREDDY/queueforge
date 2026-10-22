package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/domain"
	"github.com/google/uuid"
)

type WorkerRepo struct {
	db *DB
}

func NewWorkerRepo(db *DB) *WorkerRepo {
	return &WorkerRepo{db: db}
}

type HeartbeatParams struct {
	WorkerID    uuid.UUID
	ProcessName string
	Hostname    string
	Version     string
	Queues      []string
	TaskTypes   []string
	Draining    bool
	Metadata    json.RawMessage
}

func (r *WorkerRepo) Heartbeat(ctx context.Context, p HeartbeatParams) error {
	if p.Metadata == nil {
		p.Metadata = []byte("{}")
	}

	_, err := r.db.Pool.Exec(ctx, `
		INSERT INTO worker_instances (
			id, process_name, hostname, version, queues, task_types,
			started_at, last_seen_at, draining, metadata
		) VALUES (
			$1, $2, $3, $4, $5, $6, clock_timestamp(), clock_timestamp(), $7, $8
		)
		ON CONFLICT (id) DO UPDATE
		SET last_seen_at = clock_timestamp(),
		    draining = EXCLUDED.draining,
		    metadata = EXCLUDED.metadata
	`, p.WorkerID, p.ProcessName, p.Hostname, p.Version, p.Queues, p.TaskTypes, p.Draining, p.Metadata)

	if err != nil {
		return fmt.Errorf("failed to record worker heartbeat: %w", err)
	}
	return nil
}

func (r *WorkerRepo) ListActiveWorkers(ctx context.Context, maxAge time.Duration) ([]domain.WorkerInstance, error) {
	rows, err := r.db.Pool.Query(ctx, `
		SELECT id, process_name, hostname, version, queues, task_types,
		       started_at, last_seen_at, draining, metadata
		FROM worker_instances
		WHERE last_seen_at >= clock_timestamp() - $1::interval
		ORDER BY last_seen_at DESC
	`, fmt.Sprintf("%d seconds", int(maxAge.Seconds())))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var workers []domain.WorkerInstance
	for rows.Next() {
		var w domain.WorkerInstance
		if err := rows.Scan(
			&w.ID, &w.ProcessName, &w.Hostname, &w.Version, &w.Queues, &w.TaskTypes,
			&w.StartedAt, &w.LastSeenAt, &w.Draining, &w.Metadata,
		); err != nil {
			return nil, err
		}
		workers = append(workers, w)
	}
	return workers, rows.Err()
}
