package postgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type TenantRepo struct {
	db *DB
	// touching de-duplicates last_used_at updates: at most one in flight per API key.
	touching sync.Map
}

func NewTenantRepo(db *DB) *TenantRepo {
	return &TenantRepo{db: db}
}

type CreateTenantParams struct {
	ID                   uuid.UUID
	Name                 string
	EnqueueRatePerSecond float64
	EnqueueBurstCapacity float64
	MaxPendingJobs       int
	MaxConcurrentJobs    int
	MaxSchedules         int
}

func (r *TenantRepo) CreateTenant(ctx context.Context, p CreateTenantParams) (*domain.Tenant, error) {
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	if p.EnqueueRatePerSecond <= 0 {
		p.EnqueueRatePerSecond = 60
	}
	if p.EnqueueBurstCapacity <= 0 {
		p.EnqueueBurstCapacity = 120
	}
	if p.MaxPendingJobs <= 0 {
		p.MaxPendingJobs = 10000
	}
	if p.MaxConcurrentJobs <= 0 {
		p.MaxConcurrentJobs = 4
	}
	if p.MaxSchedules <= 0 {
		p.MaxSchedules = 100
	}

	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	now, err := dbNow(ctx, tx)
	if err != nil {
		return nil, err
	}
	tenant := &domain.Tenant{
		ID:        p.ID,
		Name:      p.Name,
		Status:    domain.TenantActive,
		CreatedAt: now,
		UpdatedAt: now,
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO tenants (id, name, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)
	`, tenant.ID, tenant.Name, tenant.Status, tenant.CreatedAt, tenant.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to insert tenant: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO tenant_limits (
			tenant_id, enqueue_rate_per_second, enqueue_burst_capacity,
			max_pending_jobs, max_concurrent_jobs, max_schedules, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, tenant.ID, p.EnqueueRatePerSecond, p.EnqueueBurstCapacity,
		p.MaxPendingJobs, p.MaxConcurrentJobs, p.MaxSchedules, now)
	if err != nil {
		return nil, fmt.Errorf("failed to insert tenant limits: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO tenant_rate_buckets (tenant_id, tokens, last_refill_at, updated_at)
		VALUES ($1, $2, clock_timestamp(), $3)
	`, tenant.ID, p.EnqueueBurstCapacity, now)
	if err != nil {
		return nil, fmt.Errorf("failed to insert tenant rate bucket: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO tenant_usage (tenant_id, outstanding_jobs, updated_at)
		VALUES ($1, 0, $2)
	`, tenant.ID, now)
	if err != nil {
		return nil, fmt.Errorf("failed to insert tenant usage: %w", err)
	}

	for slotNo := 0; slotNo < p.MaxConcurrentJobs; slotNo++ {
		_, err = tx.Exec(ctx, `
			INSERT INTO tenant_execution_slots (tenant_id, slot_no, current_job_id, lease_token, enabled, updated_at)
			VALUES ($1, $2, NULL, NULL, true, $3)
		`, tenant.ID, slotNo, now)
		if err != nil {
			return nil, fmt.Errorf("failed to insert execution slot %d: %w", slotNo, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit tenant creation: %w", err)
	}

	return tenant, nil
}

func (r *TenantRepo) GetTenant(ctx context.Context, id uuid.UUID) (*domain.Tenant, error) {
	row := r.db.Pool.QueryRow(ctx, `
		SELECT id, name, status, created_at, updated_at
		FROM tenants
		WHERE id = $1
	`, id)

	var t domain.Tenant
	err := row.Scan(&t.ID, &t.Name, &t.Status, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return &t, nil
}

func (r *TenantRepo) ListTenants(ctx context.Context) ([]domain.Tenant, error) {
	rows, err := r.db.Pool.Query(ctx, `
		SELECT id, name, status, created_at, updated_at
		FROM tenants
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tenants []domain.Tenant
	for rows.Next() {
		var t domain.Tenant
		if err := rows.Scan(&t.ID, &t.Name, &t.Status, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		tenants = append(tenants, t)
	}
	return tenants, rows.Err()
}

func (r *TenantRepo) GetLimits(ctx context.Context, tenantID uuid.UUID) (*domain.TenantLimits, error) {
	row := r.db.Pool.QueryRow(ctx, `
		SELECT tenant_id, enqueue_rate_per_second, enqueue_burst_capacity,
		       max_pending_jobs, max_concurrent_jobs, max_schedules, updated_at
		FROM tenant_limits
		WHERE tenant_id = $1
	`, tenantID)

	var l domain.TenantLimits
	err := row.Scan(&l.TenantID, &l.EnqueueRatePerSecond, &l.EnqueueBurstCapacity,
		&l.MaxPendingJobs, &l.MaxConcurrentJobs, &l.MaxSchedules, &l.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return &l, nil
}

type APIKeyCreationResult struct {
	APIKey    *domain.APIKey
	RawSecret string
}

func GenerateSecureToken() (string, []byte, string, error) {
	rawBytes := make([]byte, 32) // 256 bits of entropy
	if _, err := rand.Read(rawBytes); err != nil {
		return "", nil, "", fmt.Errorf("failed to generate random bytes: %w", err)
	}
	secretStr := "qf_live_" + base64.RawURLEncoding.EncodeToString(rawBytes)
	hash := sha256.Sum256([]byte(secretStr))
	prefix := secretStr[:12] + "..."
	return secretStr, hash[:], prefix, nil
}

func HashSecret(secretStr string) []byte {
	h := sha256.Sum256([]byte(secretStr))
	return h[:]
}

func (r *TenantRepo) CreateAPIKey(ctx context.Context, tenantID uuid.UUID, name string, permissions []string, expiresAt *time.Time) (*APIKeyCreationResult, error) {
	if len(permissions) == 0 {
		permissions = []string{"jobs:read", "jobs:write"}
	}

	rawSecret, secretHash, displayPrefix, err := GenerateSecureToken()
	if err != nil {
		return nil, err
	}

	keyID := uuid.New()
	now, err := dbNow(ctx, r.db.Pool)
	if err != nil {
		return nil, err
	}
	key := &domain.APIKey{
		ID:            keyID,
		TenantID:      tenantID,
		Name:          name,
		SecretHash:    secretHash,
		DisplayPrefix: displayPrefix,
		Permissions:   permissions,
		CreatedAt:     now,
		ExpiresAt:     expiresAt,
	}

	_, err = r.db.Pool.Exec(ctx, `
		INSERT INTO api_keys (
			id, tenant_id, name, secret_hash, display_prefix,
			permissions, created_at, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, key.ID, key.TenantID, key.Name, key.SecretHash, key.DisplayPrefix,
		key.Permissions, key.CreatedAt, key.ExpiresAt)
	if err != nil {
		return nil, fmt.Errorf("failed to insert API key: %w", err)
	}

	return &APIKeyCreationResult{
		APIKey:    key,
		RawSecret: rawSecret,
	}, nil
}

func (r *TenantRepo) LookupAPIKeyBySecret(ctx context.Context, rawSecret string) (*domain.APIKey, *domain.Tenant, error) {
	hash := HashSecret(rawSecret)

	// Expiry is decided by the DATABASE clock (see ADR-014), not by this server's clock.
	// touch_due says whether last_used_at is stale enough to be worth rewriting.
	row := r.db.Pool.QueryRow(ctx, `
		SELECT k.id, k.tenant_id, k.name, k.display_prefix, k.permissions,
		       k.created_at, k.last_used_at, k.expires_at, k.revoked_at,
		       t.id, t.name, t.status, t.created_at, t.updated_at,
		       (k.expires_at IS NOT NULL AND k.expires_at <= clock_timestamp()) AS expired,
		       (k.last_used_at IS NULL OR k.last_used_at < clock_timestamp() - interval '1 minute') AS touch_due
		FROM api_keys k
		JOIN tenants t ON t.id = k.tenant_id
		WHERE k.secret_hash = $1
	`, hash)

	var k domain.APIKey
	var t domain.Tenant
	var expired, touchDue bool
	err := row.Scan(
		&k.ID, &k.TenantID, &k.Name, &k.DisplayPrefix, &k.Permissions,
		&k.CreatedAt, &k.LastUsedAt, &k.ExpiresAt, &k.RevokedAt,
		&t.ID, &t.Name, &t.Status, &t.CreatedAt, &t.UpdatedAt,
		&expired, &touchDue,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, domain.ErrNotFound
		}
		return nil, nil, err
	}
	if expired || k.RevokedAt != nil {
		return nil, nil, domain.ErrNotFound // indistinguishable from an unknown key
	}

	// Record usage at most once a minute per key. Updating on EVERY request meant a goroutine and
	// a write to the same hot row for each call: write amplification an attacker with any valid
	// key could use to hammer the database. Only one update per key may be in flight.
	if touchDue {
		if _, busy := r.touching.LoadOrStore(k.ID, struct{}{}); !busy {
			go func() {
				defer r.touching.Delete(k.ID)
				bgCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				_, _ = r.db.Pool.Exec(bgCtx, `
					UPDATE api_keys SET last_used_at = clock_timestamp()
					WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < clock_timestamp() - interval '1 minute')
				`, k.ID)
			}()
		}
	}

	return &k, &t, nil
}

func (r *TenantRepo) RevokeAPIKey(ctx context.Context, keyID uuid.UUID) error {
	tag, err := r.db.Pool.Exec(ctx, `
		UPDATE api_keys
		SET revoked_at = clock_timestamp()
		WHERE id = $1 AND revoked_at IS NULL
	`, keyID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *TenantRepo) ReconcileUsage(ctx context.Context, tenantID uuid.UUID) (int64, error) {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var actualNonterminal int64
	err = tx.QueryRow(ctx, `
		SELECT count(*)
		FROM jobs
		WHERE tenant_id = $1 AND status IN ('queued', 'running')
	`, tenantID).Scan(&actualNonterminal)
	if err != nil {
		return 0, err
	}

	_, err = tx.Exec(ctx, `
		UPDATE tenant_usage
		SET outstanding_jobs = $2, updated_at = clock_timestamp()
		WHERE tenant_id = $1
	`, tenantID, actualNonterminal)
	if err != nil {
		return 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}

	return actualNonterminal, nil
}

// CreateAPIKeyExpiringIn creates a key that expires d after now, where "now" is the DATABASE
// clock (see ADR-014), so expiry is unaffected by the operator's own clock.
func (r *TenantRepo) CreateAPIKeyExpiringIn(ctx context.Context, tenantID uuid.UUID, name string, permissions []string, d time.Duration) (*APIKeyCreationResult, error) {
	now, err := dbNow(ctx, r.db.Pool)
	if err != nil {
		return nil, err
	}
	exp := now.Add(d)
	return r.CreateAPIKey(ctx, tenantID, name, permissions, &exp)
}
