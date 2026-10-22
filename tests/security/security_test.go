// Package security_test holds security regression tests: authentication hardening, audit logging,
// and a randomized cross-tenant isolation fuzzer.
package security_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/auth"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/config"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/httpapi"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/observability"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/postgres"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/testutil"
	"github.com/google/uuid"
)

type rig struct {
	t       *testing.T
	ctx     context.Context
	db      *postgres.DB
	tenants *postgres.TenantRepo
	jobs    *postgres.JobRepo
	audit   *postgres.AuditRepo
	handler http.Handler
}

func newRig(t *testing.T) *rig {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	testutil.IsolatedDatabase(t, cfg)
	ctx := context.Background()
	db, err := postgres.NewDB(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	tr, jr := postgres.NewTenantRepo(db), postgres.NewJobRepo(db)
	api := httpapi.NewAPI(cfg, db, tr, jr, postgres.NewScheduleRepo(db), postgres.NewWorkerRepo(db), observability.NewMetrics(nil))
	return &rig{t: t, ctx: ctx, db: db, tenants: tr, jobs: jr, audit: postgres.NewAuditRepo(db), handler: api.Routes()}
}

var allPerms = []string{"jobs:read", "jobs:write", "schedules:read", "schedules:write", "deadletters:read", "deadletters:write", "metrics:read"}

func (g *rig) tenantWithKey(name string, perms []string, expires *time.Time) (uuid.UUID, string) {
	g.t.Helper()
	tn, err := g.tenants.CreateTenant(g.ctx, postgres.CreateTenantParams{Name: name + "-" + uuid.NewString()[:5],
		EnqueueRatePerSecond: 1000, EnqueueBurstCapacity: 1000, MaxPendingJobs: 10000, MaxConcurrentJobs: 4, MaxSchedules: 20})
	if err != nil {
		g.t.Fatal(err)
	}
	k, err := g.tenants.CreateAPIKey(g.ctx, tn.ID, "k", perms, expires)
	if err != nil {
		g.t.Fatal(err)
	}
	return tn.ID, k.RawSecret
}

// do performs an HTTP request against the API. ip makes each test use its own client address so
// the (process-wide) auth failure limiter state of one test cannot leak into another.
func (g *rig) do(method, path, key, ip string, body any) (int, []byte) {
	g.t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.RemoteAddr = ip + ":40000"
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	rec := httptest.NewRecorder()
	g.handler.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func (g *rig) enqueue(key, ip string) string {
	g.t.Helper()
	code, body := g.do("POST", "/v1/jobs", key, ip, map[string]any{"task_type": "echo", "queue": "default", "payload": map[string]any{"x": 1}})
	if code != 202 && code != 200 && code != 201 {
		g.t.Fatalf("enqueue: %d %s", code, body)
	}
	var out struct {
		Job struct{ ID string } `json:"job"`
		ID  string              `json:"id"`
	}
	_ = json.Unmarshal(body, &out)
	if out.Job.ID != "" {
		return out.Job.ID
	}
	return out.ID
}

// ---------------------------------------------------------------- authentication hardening

func TestFailureLimiter(t *testing.T) {
	l := auth.NewFailureLimiter(3, 200*time.Millisecond)
	for i := 0; i < 2; i++ {
		l.RecordFailure("1.2.3.4")
	}
	if b, _ := l.Blocked("1.2.3.4"); b {
		t.Fatal("blocked before reaching the limit")
	}
	l.RecordFailure("1.2.3.4")
	if b, retry := l.Blocked("1.2.3.4"); !b || retry <= 0 {
		t.Fatalf("should be blocked after 3 failures (blocked=%v retry=%v)", b, retry)
	}
	if b, _ := l.Blocked("5.6.7.8"); b {
		t.Fatal("a different client must not be blocked")
	}
	time.Sleep(250 * time.Millisecond)
	if b, _ := l.Blocked("1.2.3.4"); b {
		t.Fatal("block must expire after the window")
	}
}

func TestClientKey_ForwardedForOnlyWhenTrusted(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.5:1234"
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
	if got := auth.ClientKey(r, false); got != "10.0.0.5" {
		t.Fatalf("untrusted: got %q, a client must not be able to choose its own identity", got)
	}
	if got := auth.ClientKey(r, true); got != "203.0.113.9" {
		t.Fatalf("trusted: got %q", got)
	}
}

func TestBruteForceIsThrottledBeforeTouchingTheDatabase(t *testing.T) {
	g := newRig(t)
	_, goodKey := g.tenantWithKey("bf", allPerms, nil)
	ip := "198.51.100.77"
	bad := "qf_live_" + strings.Repeat("x", 40)
	saw429 := false
	for i := 0; i < 40; i++ {
		code, _ := g.do("GET", "/v1/jobs", bad, ip, nil)
		if code == 429 {
			saw429 = true
			break
		}
		if code != 401 {
			t.Fatalf("attempt %d: got %d, want 401", i, code)
		}
	}
	if !saw429 {
		t.Fatal("40 consecutive wrong keys were never throttled")
	}
	// the throttled client is rejected even with a valid key (it must slow down first)...
	if code, _ := g.do("GET", "/v1/jobs", goodKey, ip, nil); code != 429 {
		t.Fatalf("throttled client with valid key got %d, want 429", code)
	}
	// ...while an unrelated client is unaffected.
	if code, _ := g.do("GET", "/v1/jobs", goodKey, "198.51.100.78", nil); code != 200 {
		t.Fatalf("unrelated client got %d, want 200", code)
	}
}

func TestExpiredAndRevokedKeysAreRejected(t *testing.T) {
	g := newRig(t)
	future := time.Now().Add(time.Hour)
	tid, key := g.tenantWithKey("exp", allPerms, &future)
	ip := "198.51.100.10"
	if code, _ := g.do("GET", "/v1/jobs", key, ip, nil); code != 200 {
		t.Fatalf("valid key: %d", code)
	}
	// Expire it using the DATABASE clock, not the test process's clock.
	if _, err := g.db.Pool.Exec(g.ctx, `UPDATE api_keys SET expires_at = clock_timestamp() - interval '1 second' WHERE tenant_id=$1`, tid); err != nil {
		t.Fatal(err)
	}
	if code, _ := g.do("GET", "/v1/jobs", key, ip, nil); code != 401 {
		t.Fatalf("expired key: %d, want 401", code)
	}
	_, key2 := g.tenantWithKey("rev", allPerms, nil)
	if code, _ := g.do("GET", "/v1/jobs", key2, "198.51.100.11", nil); code != 200 {
		t.Fatal("fresh key should work")
	}
	if _, err := g.db.Pool.Exec(g.ctx, `UPDATE api_keys SET revoked_at = clock_timestamp() WHERE secret_hash = $1`, postgres.HashSecret(key2)); err != nil {
		t.Fatal(err)
	}
	if code, _ := g.do("GET", "/v1/jobs", key2, "198.51.100.11", nil); code != 401 {
		t.Fatalf("revoked key: %d, want 401", code)
	}
}

func TestKeyRotationWithOverlap(t *testing.T) {
	g := newRig(t)
	tid, oldKey := g.tenantWithKey("rot", allPerms, nil)
	ip := "198.51.100.20"
	newKey, err := g.tenants.CreateAPIKey(g.ctx, tid, "rotated", allPerms, nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, k := range map[string]string{"old": oldKey, "new": newKey.RawSecret} {
		if code, _ := g.do("GET", "/v1/jobs", k, ip, nil); code != 200 {
			t.Fatalf("during overlap the %s key must work, got %d", name, code)
		}
	}
	if _, err := g.db.Pool.Exec(g.ctx, `UPDATE api_keys SET revoked_at = clock_timestamp() WHERE secret_hash = $1`, postgres.HashSecret(oldKey)); err != nil {
		t.Fatal(err)
	}
	if code, _ := g.do("GET", "/v1/jobs", oldKey, ip, nil); code != 401 {
		t.Fatalf("old key after revocation: %d", code)
	}
	if code, _ := g.do("GET", "/v1/jobs", newKey.RawSecret, ip, nil); code != 200 {
		t.Fatalf("new key after old revoked: %d", code)
	}
}

// last_used_at used to be rewritten (in a fresh goroutine) on EVERY request.
func TestLastUsedAtIsNotRewrittenOnEveryRequest(t *testing.T) {
	g := newRig(t)
	tid, key := g.tenantWithKey("lu", allPerms, nil)
	hammer := func(n int) {
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); g.do("GET", "/v1/tenant/limits", key, "198.51.100.30", nil) }()
		}
		wg.Wait()
		time.Sleep(400 * time.Millisecond)
	}
	read := func() (t0 time.Time) {
		var p *time.Time
		_ = g.db.Pool.QueryRow(g.ctx, `SELECT last_used_at FROM api_keys WHERE tenant_id=$1`, tid).Scan(&p)
		if p != nil {
			t0 = *p
		}
		return
	}
	hammer(100)
	first := read()
	if first.IsZero() {
		t.Fatal("last_used_at was never set")
	}
	var xminBefore, xminAfter string
	_ = g.db.Pool.QueryRow(g.ctx, `SELECT xmin::text FROM api_keys WHERE tenant_id=$1`, tid).Scan(&xminBefore)
	hammer(200)
	if second := read(); !second.Equal(first) {
		t.Fatalf("last_used_at advanced from %v to %v within a minute: it is being rewritten per request", first, second)
	}
	_ = g.db.Pool.QueryRow(g.ctx, `SELECT xmin::text FROM api_keys WHERE tenant_id=$1`, tid).Scan(&xminAfter)
	if xminBefore != xminAfter {
		t.Fatalf("api_keys row was rewritten by 200 more requests (xmin %s -> %s)", xminBefore, xminAfter)
	}
}

// ---------------------------------------------------------------- audit log

func TestAuditLogRecordsControlPlaneActionsOnly(t *testing.T) {
	g := newRig(t)
	tid, key := g.tenantWithKey("aud", allPerms, nil)
	ip := "198.51.100.40"
	id := g.enqueue(key, ip)
	g.do("GET", "/v1/jobs/"+id, key, ip, nil)
	if code, body := g.do("POST", "/v1/jobs/"+id+"/cancel", key, ip, nil); code != 200 {
		t.Fatalf("cancel: %d %s", code, body)
	}
	g.do("POST", "/v1/jobs/"+uuid.NewString()+"/cancel", key, ip, nil) // fails (404): must NOT be audited

	entries, err := g.audit.List(g.ctx, tid, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("want exactly 1 audit entry (the successful cancel), got %d: %+v", len(entries), entries)
	}
	e := entries[0]
	if e.Action != "POST /v1/jobs/{id}/cancel" || e.TargetID != id || e.ActorType != "api_key" || e.StatusCode != 200 || e.TenantID == nil || *e.TenantID != tid {
		t.Fatalf("unexpected audit entry: %+v", e)
	}
}

func TestAuditLogIsAppendOnly(t *testing.T) {
	g := newRig(t)
	tid, _ := g.tenantWithKey("imm", allPerms, nil)
	if err := g.audit.Record(g.ctx, postgres.AuditEntry{TenantID: &tid, ActorType: "operator", ActorID: "test", Action: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.db.Pool.Exec(g.ctx, `UPDATE audit_log SET action='tampered'`); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("UPDATE on audit_log must be rejected, got %v", err)
	}
	if _, err := g.db.Pool.Exec(g.ctx, `DELETE FROM audit_log`); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("DELETE on audit_log must be rejected, got %v", err)
	}
}

// ---------------------------------------------------------------- tenant isolation

// Workers are shared infrastructure: ordinary tenants must not learn hostnames or the task types
// other tenants' jobs use.
func TestWorkersEndpointDoesNotLeakInfrastructure(t *testing.T) {
	g := newRig(t)
	_, key := g.tenantWithKey("w", allPerms, nil)
	_, opKey := g.tenantWithKey("op", append([]string{"workers:read"}, allPerms...), nil)
	if err := postgres.NewWorkerRepo(g.db).Heartbeat(g.ctx, postgres.HeartbeatParams{WorkerID: uuid.New(), ProcessName: "secret-proc",
		Hostname: "prod-db-host-7", Version: "9.9.9", Queues: []string{"default"}, TaskTypes: []string{"acme.confidential.report"}}); err != nil {
		t.Fatal(err)
	}
	_, body := g.do("GET", "/v1/workers", key, "198.51.100.50", nil)
	for _, secret := range []string{"prod-db-host-7", "secret-proc", "9.9.9", "acme.confidential.report"} {
		if strings.Contains(string(body), secret) {
			t.Fatalf("tenant without workers:read can see %q in /v1/workers: %s", secret, body)
		}
	}
	_, body = g.do("GET", "/v1/workers", opKey, "198.51.100.51", nil)
	if !strings.Contains(string(body), "prod-db-host-7") {
		t.Fatalf("operator key with workers:read should see full details: %s", body)
	}
}

// Randomized cross-tenant attack: tenant B throws every kind of request at tenant A's resources.
// Nothing may succeed, and tenant A's data must be byte-for-byte unchanged afterwards.
func TestTenantIsolationFuzz(t *testing.T) {
	g := newRig(t)
	aID, aKey := g.tenantWithKey("A", allPerms, nil)
	_, bKey := g.tenantWithKey("B", allPerms, nil)
	ipA, ipB := "198.51.100.60", "198.51.100.61"

	var aJobs []string
	for i := 0; i < 6; i++ {
		aJobs = append(aJobs, g.enqueue(aKey, ipA))
	}
	var aSchedules []string
	for i := 0; i < 2; i++ {
		code, body := g.do("POST", "/v1/schedules", aKey, ipA, map[string]any{"name": fmt.Sprintf("s%d", i), "queue": "default",
			"task_type": "echo", "payload": map[string]any{}, "cron_expression": "0 * * * *", "timezone": "UTC"})
		if code >= 300 {
			t.Fatalf("create schedule: %d %s", code, body)
		}
		var out struct {
			ID       string              `json:"id"`
			Schedule struct{ ID string } `json:"schedule"`
		}
		_ = json.Unmarshal(body, &out)
		id := out.ID
		if id == "" {
			id = out.Schedule.ID
		}
		aSchedules = append(aSchedules, id)
	}
	snapshot := func() string {
		var h string
		_ = g.db.Pool.QueryRow(g.ctx, `
			SELECT md5(COALESCE((SELECT string_agg(j::text, '|' ORDER BY id) FROM jobs j WHERE tenant_id=$1),'') ||
			           COALESCE((SELECT string_agg(s::text, '|' ORDER BY id) FROM job_schedules s WHERE tenant_id=$1),'') ||
			           COALESCE((SELECT outstanding_jobs::text FROM tenant_usage WHERE tenant_id=$1),''))`, aID).Scan(&h)
		return h
	}
	before := snapshot()

	rnd := rand.New(rand.NewSource(7))
	randID := func(list []string) string {
		if rnd.Intn(5) == 0 {
			return uuid.NewString() // sometimes a random id, to compare existence-leak behaviour
		}
		return list[rnd.Intn(len(list))]
	}
	type attack struct {
		method, path string
		body         any
	}
	mk := func() attack {
		switch rnd.Intn(9) {
		case 0:
			return attack{"GET", "/v1/jobs/" + randID(aJobs), nil}
		case 1:
			return attack{"POST", "/v1/jobs/" + randID(aJobs) + "/cancel", nil}
		case 2:
			return attack{"POST", "/v1/jobs/" + randID(aJobs) + "/replay", nil}
		case 3:
			return attack{"GET", "/v1/schedules/" + randID(aSchedules), nil}
		case 4:
			return attack{"PATCH", "/v1/schedules/" + randID(aSchedules), map[string]any{"paused": true}}
		case 5:
			return attack{"DELETE", "/v1/schedules/" + randID(aSchedules), nil}
		case 6:
			return attack{"GET", "/v1/jobs/" + randID(aJobs) + "?x=1", nil}
		case 7:
			return attack{"GET", "/v1/jobs/" + randID(aJobs) + "/../" + randID(aJobs), nil}
		default:
			return attack{"POST", "/v1/jobs/" + randID(aJobs) + "/cancel?force=true", nil}
		}
	}
	for i := 0; i < 400; i++ {
		a := mk()
		code, body := g.do(a.method, a.path, bKey, ipB, a.body)
		if code >= 200 && code < 300 {
			t.Fatalf("CROSS-TENANT ACCESS: tenant B %s %s succeeded with %d: %s", a.method, a.path, code, body)
		}
		for _, id := range append(append([]string{}, aJobs...), aSchedules...) {
			if code != 404 && code != 400 && code != 405 && strings.Contains(string(body), id) {
				t.Fatalf("response to %s %s leaks tenant A id %s", a.method, a.path, id)
			}
		}
	}
	// Listings must contain only the caller's own data.
	for _, p := range []string{"/v1/jobs", "/v1/dead-letters", "/v1/schedules"} {
		_, body := g.do("GET", p, bKey, ipB, nil)
		for _, id := range append(append([]string{}, aJobs...), aSchedules...) {
			if strings.Contains(string(body), id) {
				t.Fatalf("GET %s as tenant B lists tenant A's %s", p, id)
			}
		}
	}
	if after := snapshot(); after != before {
		t.Fatal("tenant A's jobs/schedules/usage changed as a result of tenant B's requests")
	}
}

// ---------------------------------------------------------------- tracing

func TestTraceIDPropagatesFromRequestToStoredJob(t *testing.T) {
	g := newRig(t)
	_, key := g.tenantWithKey("trace", allPerms, nil)
	const tid = "4bf92f3577b34da6a3ce929d0e0e4736"

	post := func(hdr map[string]string, idemKey string) (*httptest.ResponseRecorder, string) {
		body, _ := json.Marshal(map[string]any{"task_type": "echo", "queue": "default", "payload": map[string]any{}})
		req := httptest.NewRequest("POST", "/v1/jobs", bytes.NewReader(body))
		req.RemoteAddr = "198.51.100.90:1"
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")
		if idemKey != "" {
			req.Header.Set("Idempotency-Key", idemKey)
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		g.handler.ServeHTTP(rec, req)
		var out struct {
			ID      string `json:"id"`
			TraceID string `json:"trace_id"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if out.TraceID != rec.Header().Get("X-Trace-Id") {
			t.Fatalf("body trace_id %q != X-Trace-Id header %q", out.TraceID, rec.Header().Get("X-Trace-Id"))
		}
		return rec, out.ID + "|" + out.TraceID
	}

	// 1. W3C traceparent is adopted.
	_, v := post(map[string]string{"traceparent": "00-" + tid + "-00f067aa0ba902b7-01"}, "")
	id, got := strings.Split(v, "|")[0], strings.Split(v, "|")[1]
	if got != tid {
		t.Fatalf("traceparent not adopted: %q", got)
	}
	// 2. it is stored and readable back through the API.
	code, body := g.do("GET", "/v1/jobs/"+id, key, "198.51.100.90", nil)
	if code != 200 || !strings.Contains(string(body), tid) {
		t.Fatalf("GET job: %d %s (want trace_id %s)", code, body, tid)
	}
	// 3. with no headers a fresh id is generated: every job is traceable.
	_, v = post(nil, "")
	if tr := strings.Split(v, "|")[1]; len(tr) != 32 {
		t.Fatalf("generated trace id has wrong shape: %q", tr)
	}
	// 4. an idempotent replay returns the ORIGINAL job's trace id, not a new one.
	_, first := post(nil, "idem-trace-key-0001")
	_, replay := post(map[string]string{"traceparent": "00-" + strings.Repeat("a", 32) + "-00f067aa0ba902b7-01"}, "idem-trace-key-0001")
	if strings.Split(first, "|")[1] != strings.Split(replay, "|")[1] || strings.Split(first, "|")[0] != strings.Split(replay, "|")[0] {
		t.Fatalf("replay must return the original job and trace id: %q vs %q", first, replay)
	}
}
