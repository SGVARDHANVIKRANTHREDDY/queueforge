package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// AuditEntry is one control-plane action.
type AuditEntry struct {
	ID         int64           `json:"id"`
	OccurredAt time.Time       `json:"occurred_at"`
	TenantID   *uuid.UUID      `json:"tenant_id,omitempty"`
	ActorType  string          `json:"actor_type"` // "api_key" | "operator"
	ActorID    string          `json:"actor_id"`
	Action     string          `json:"action"`
	TargetID   string          `json:"target_id,omitempty"`
	StatusCode int             `json:"status_code,omitempty"`
	RequestID  string          `json:"request_id,omitempty"`
	RemoteAddr string          `json:"remote_addr,omitempty"`
	Details    json.RawMessage `json:"details,omitempty"`
}

type AuditRepo struct{ db *DB }

func NewAuditRepo(db *DB) *AuditRepo { return &AuditRepo{db: db} }

// Record appends an entry. The timestamp is assigned by the database clock.
func (r *AuditRepo) Record(ctx context.Context, e AuditEntry) error {
	details := e.Details
	if len(details) == 0 {
		details = json.RawMessage(`{}`)
	}
	_, err := r.db.Pool.Exec(ctx, `
		INSERT INTO audit_log (tenant_id, actor_type, actor_id, action, target_id, status_code, request_id, remote_addr, details)
		VALUES ($1, $2, $3, $4, NULLIF($5,''), NULLIF($6,0), NULLIF($7,''), NULLIF($8,''), $9)
	`, e.TenantID, e.ActorType, e.ActorID, e.Action, e.TargetID, e.StatusCode, e.RequestID, e.RemoteAddr, details)
	return err
}

// List returns a tenant's most recent entries, newest first. Always tenant-scoped.
func (r *AuditRepo) List(ctx context.Context, tenantID uuid.UUID, limit int) ([]AuditEntry, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := r.db.Pool.Query(ctx, `
		SELECT id, occurred_at, tenant_id, actor_type, actor_id, action, COALESCE(target_id,''),
		       COALESCE(status_code,0), COALESCE(request_id,''), COALESCE(remote_addr,''), details
		FROM audit_log WHERE tenant_id = $1 ORDER BY occurred_at DESC, id DESC LIMIT $2
	`, tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.OccurredAt, &e.TenantID, &e.ActorType, &e.ActorID, &e.Action, &e.TargetID,
			&e.StatusCode, &e.RequestID, &e.RemoteAddr, &e.Details); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
