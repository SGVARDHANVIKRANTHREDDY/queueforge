package queueforge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/config"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/httpapi"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/observability"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/postgres"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/testutil"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/worker"
	"github.com/google/uuid"
)

type stack struct {
	t      *testing.T
	db     *postgres.DB
	tenant uuid.UUID
	key    string
	api    http.Handler
}

func newStack(t *testing.T) *stack {
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
	tn, err := tr.CreateTenant(ctx, postgres.CreateTenantParams{Name: "sdk-" + uuid.NewString()[:5], EnqueueRatePerSecond: 1000,
		EnqueueBurstCapacity: 1000, MaxPendingJobs: 1000, MaxConcurrentJobs: 8, MaxSchedules: 5})
	if err != nil {
		t.Fatal(err)
	}
	k, err := tr.CreateAPIKey(ctx, tn.ID, "sdk", []string{"jobs:read", "jobs:write"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	api := httpapi.NewAPI(cfg, db, tr, jr, postgres.NewScheduleRepo(db), postgres.NewWorkerRepo(db), observability.NewMetrics(nil))
	return &stack{t: t, db: db, tenant: tn.ID, key: k.RawSecret, api: api.Routes()}
}

func (s *stack) serve(wrap func(http.Handler) http.Handler) *Client {
	h := s.api
	if wrap != nil {
		h = wrap(h)
	}
	srv := httptest.NewServer(h)
	s.t.Cleanup(srv.Close)
	return New(srv.URL, s.key, WithBackoff(time.Millisecond, 5*time.Millisecond))
}

func (s *stack) jobCount() int {
	var n int
	if err := s.db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM jobs WHERE tenant_id=$1`, s.tenant).Scan(&n); err != nil {
		s.t.Fatal(err)
	}
	return n
}

func TestEnqueueAndWait_EndToEndWithRealWorker(t *testing.T) {
	s := newStack(t)
	cfg, _ := config.Load()
	cfg.DatabaseURL = s.db.Pool.Config().ConnString()
	w := worker.NewWorker(cfg, postgres.NewJobRepo(s.db), postgres.NewWorkerRepo(s.db), nil)
	_ = w.Register("echo", func(ctx context.Context, p json.RawMessage) (json.RawMessage, error) { return p, nil })
	w.Start()
	t.Cleanup(func() { w.Stop(2 * time.Second) })

	c := s.serve(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	job, err := c.Enqueue(ctx, EnqueueRequest{TaskType: "echo", Payload: json.RawMessage(`{"hello":"sdk"}`),
		TraceParent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"})
	if err != nil {
		t.Fatal(err)
	}
	done, err := c.Wait(ctx, job.ID, WaitOptions{PollInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != "succeeded" || string(done.Result) != `{"hello": "sdk"}` && string(done.Result) != `{"hello":"sdk"}` {
		t.Fatalf("unexpected final job: status=%s result=%s", done.Status, done.Result)
	}
	if done.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("trace id not propagated: %q", done.TraceID)
	}
}

// The nastiest real-world failure: the server CREATES the job but the response is lost (timeout,
// 503 from a proxy). A naive client retries and creates a duplicate. This SDK must not.
func TestRetryAfterLostResponseCreatesExactlyOneJob(t *testing.T) {
	s := newStack(t)
	var seen atomic.Int32
	c := s.serve(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && r.URL.Path == "/v1/jobs" && seen.Add(1) <= 2 {
				rec := httptest.NewRecorder()
				next.ServeHTTP(rec, r)                              // the job really is created...
				http.Error(w, "bad gateway", http.StatusBadGateway) // ...but the client never learns
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	job, err := c.Enqueue(context.Background(), EnqueueRequest{TaskType: "echo", Payload: json.RawMessage(`{"n":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.jobCount(); got != 1 {
		t.Fatalf("%d jobs exist after 2 lost responses and a retry; exactly 1 expected (no duplicates)", got)
	}
	if !job.IdempotentReplay {
		t.Fatal("the final response should be flagged as an idempotent replay of the original job")
	}
	if seen.Load() != 3 {
		t.Fatalf("expected 3 attempts, saw %d", seen.Load())
	}
}

func TestRetryHonoursRetryAfterHeader(t *testing.T) {
	s := newStack(t)
	var calls atomic.Int32
	c := s.serve(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/jobs" && calls.Add(1) == 1 {
				w.Header().Set("Retry-After", "3")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":{"code":"rate_limited","message":"slow down","request_id":"r1"}}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	var waits []time.Duration
	c.sleep = func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }
	if _, err := c.Enqueue(context.Background(), EnqueueRequest{TaskType: "echo"}); err != nil {
		t.Fatal(err)
	}
	if len(waits) != 1 || waits[0] != 3*time.Second {
		t.Fatalf("waits = %v, want exactly [3s] taken from Retry-After", waits)
	}
}

func TestClientErrorsAreNotRetried(t *testing.T) {
	s := newStack(t)
	var calls atomic.Int32
	c := s.serve(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); next.ServeHTTP(w, r) })
	})
	_, err := c.Enqueue(context.Background(), EnqueueRequest{TaskType: "echo", Queue: "no-such-queue"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 400 && apiErr.Status != 404 && apiErr.Status != 422 {
		t.Fatalf("want a 4xx *APIError, got %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("a 4xx must not be retried, saw %d calls", calls.Load())
	}
	if _, err := c.Get(context.Background(), uuid.NewString()); !IsNotFound(err) {
		t.Fatalf("IsNotFound = false for %v", err)
	}
}

func TestContextCancellationStopsRetrying(t *testing.T) {
	s := newStack(t)
	c := s.serve(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "down", http.StatusServiceUnavailable) })
	})
	c.maxRetries = 1000
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	c.sleep = sleepCtx
	c.baseDelay, c.maxDelay = 40*time.Millisecond, 40*time.Millisecond
	t0 := time.Now()
	_, err := c.Enqueue(ctx, EnqueueRequest{TaskType: "echo"})
	if err == nil || time.Since(t0) > 2*time.Second {
		t.Fatalf("retry loop did not stop promptly on context expiry (err=%v after %v)", err, time.Since(t0))
	}
}
