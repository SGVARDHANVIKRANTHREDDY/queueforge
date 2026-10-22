package security_test

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/postgres"
	"github.com/google/uuid"
)

func (g *rig) postBatch(key, ip, idemKey string, n int) (int, map[string]any) {
	g.t.Helper()
	jobs := make([]map[string]any, n)
	for i := range jobs {
		jobs[i] = map[string]any{"task_type": "echo", "queue": "default", "payload": map[string]any{"i": i}}
	}
	b, _ := json.Marshal(map[string]any{"jobs": jobs})
	req := httptest.NewRequest("POST", "/v1/jobs/batch", bytes.NewReader(b))
	req.RemoteAddr = ip + ":40000"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	if idemKey != "" {
		req.Header.Set("Idempotency-Key", idemKey)
	}
	rec := httptest.NewRecorder()
	g.handler.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func batchIDs(t *testing.T, out map[string]any) []string {
	t.Helper()
	arr, _ := out["jobs"].([]any)
	ids := make([]string, 0, len(arr))
	for _, v := range arr {
		ids = append(ids, v.(map[string]any)["id"].(string))
	}
	return ids
}

func TestBatchEnqueue_CreatesAllJobsAndIsIdempotent(t *testing.T) {
	g := newRig(t)
	_, key := g.tenantWithKey("batch", allPerms, nil)
	ip := "198.51.100.90"

	code, out := g.postBatch(key, ip, "batch-key-1", 5)
	if code != 202 {
		t.Fatalf("first batch: %d %v", code, out)
	}
	first := batchIDs(t, out)
	if len(first) != 5 {
		t.Fatalf("want 5 jobs, got %d", len(first))
	}
	// identical retry must not create duplicates
	code, out = g.postBatch(key, ip, "batch-key-1", 5)
	if code != 200 {
		t.Fatalf("replay: %d %v", code, out)
	}
	second := batchIDs(t, out)
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("replay returned different job at %d: %s vs %s", i, first[i], second[i])
		}
	}
	var n int
	if err := g.db.Pool.QueryRow(g.ctx, `SELECT count(*) FROM jobs`).Scan(&n); err != nil || n != 5 {
		t.Fatalf("want exactly 5 jobs stored, got %d (err=%v)", n, err)
	}
}

func TestBatchEnqueue_IsAllOrNothing(t *testing.T) {
	g := newRig(t)
	tn, err := g.tenants.CreateTenant(g.ctx, postgres.CreateTenantParams{Name: "tiny-" + uuid.NewString()[:5],
		EnqueueRatePerSecond: 1000, EnqueueBurstCapacity: 1000, MaxPendingJobs: 3, MaxConcurrentJobs: 2, MaxSchedules: 1})
	if err != nil {
		t.Fatal(err)
	}
	k, err := g.tenants.CreateAPIKey(g.ctx, tn.ID, "k", allPerms, nil)
	if err != nil {
		t.Fatal(err)
	}
	code, out := g.postBatch(k.RawSecret, "198.51.100.91", "", 5) // 5 > quota of 3
	if code != 429 {
		t.Fatalf("want 429 for over-quota batch, got %d %v", code, out)
	}
	var n int
	if err := g.db.Pool.QueryRow(g.ctx, `SELECT count(*) FROM jobs WHERE tenant_id=$1`, tn.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("partial batch was stored: %d jobs (err=%v)", n, err)
	}
}

func TestBatchEnqueue_RejectsBadSizesAndEntries(t *testing.T) {
	g := newRig(t)
	_, key := g.tenantWithKey("batchbad", allPerms, nil)
	if code, _ := g.postBatch(key, "198.51.100.92", "", 0); code != 400 {
		t.Fatalf("empty batch: %d", code)
	}
	if code, _ := g.postBatch(key, "198.51.100.92", "", 101); code != 400 {
		t.Fatalf("oversized batch: %d", code)
	}
	req := httptest.NewRequest("POST", "/v1/jobs/batch", strings.NewReader(`{"jobs":[{"task_type":""}]}`))
	req.RemoteAddr = "198.51.100.92:40000"
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	g.handler.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("invalid entry: %d %s", rec.Code, rec.Body.String())
	}
}
