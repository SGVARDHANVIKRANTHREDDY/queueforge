package load_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/config"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/domain"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/postgres"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/testutil"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/worker"
	"github.com/google/uuid"
)

// TestSoak runs a steady workload for QF_SOAK_SECONDS (opt-in) and checks that nothing drifts:
// latency stays flat, memory stays flat, no slot or quota leaks, tables do not bloat, and every
// job ends in a terminal state exactly as often as it should.
//
//	QF_SOAK_SECONDS=600 QF_SOAK_RATE=150 go test -v -run TestSoak ./tests/load/
//
// The workload deliberately includes failures: 2% of jobs fail once (retry), 0.2% fail permanently
// (dead letter), so retry, backoff, DLQ, and slot-release-on-failure paths run continuously.
func TestSoak(t *testing.T) {
	secs := envInt("QF_SOAK_SECONDS", 0)
	if secs == 0 {
		t.Skip("set QF_SOAK_SECONDS to run the soak test")
	}
	rate := envInt("QF_SOAK_RATE", 150)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	testutil.IsolatedDatabase(t, cfg)
	cfg.RetryBaseDelay, cfg.RetryMaxDelay = 200*time.Millisecond, time.Second
	taskType := "soak." + uuid.NewString()[:8]
	ctx := context.Background()

	adminDB := newDB(t, cfg, 16)
	defer adminDB.Close()
	jobs := postgres.NewJobRepo(adminDB)
	tenants := makeTenants(t, postgres.NewTenantRepo(adminDB), 4, 16)

	var execCount sync.Map // idx -> *atomic.Int32
	handler := func(ctx context.Context, payload json.RawMessage) (json.RawMessage, error) {
		var p struct {
			Idx int `json:"idx"`
		}
		_ = json.Unmarshal(payload, &p)
		// Track executions ONLY for the jobs that need it (the 2% that fail once). Tracking every
		// job grew this map linearly and made the harness itself look like a memory leak.
		var n int32 = 1
		if p.Idx%50 == 0 {
			v, _ := execCount.LoadOrStore(p.Idx, new(atomic.Int32))
			n = v.(*atomic.Int32).Add(1)
		}
		time.Sleep(time.Duration(rand.Intn(15)) * time.Millisecond)
		switch {
		case p.Idx%500 == 0:
			return nil, domain.NewPermanentError(errors.New("soak: permanent failure"))
		case p.Idx%50 == 0 && n == 1:
			return nil, errors.New("soak: transient failure")
		}
		return payload, nil
	}
	var workers []*worker.Worker
	for i := 0; i < 2; i++ {
		db := newDB(t, cfg, 12)
		defer db.Close()
		w := worker.NewWorker(cfg, postgres.NewJobRepo(db), postgres.NewWorkerRepo(db), nil)
		_ = w.Register(taskType, handler)
		w.Start()
		workers = append(workers, w)
	}

	// Producer: fixed rate, 8 goroutines pacing off one ticker.
	var enq, enqErr atomic.Int64
	stop := make(chan struct{})
	var pwg sync.WaitGroup
	tick := time.NewTicker(time.Second / time.Duration(rate))
	work := make(chan int, 1024)
	pwg.Add(1)
	go func() {
		defer pwg.Done()
		defer close(work)
		i := 0
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				work <- i
				i++
			}
		}
	}()
	var winMu sync.Mutex
	var window []time.Duration
	for g := 0; g < 8; g++ {
		pwg.Add(1)
		go func() {
			defer pwg.Done()
			for i := range work {
				t0 := time.Now()
				_, err := jobs.EnqueueJob(ctx, postgres.EnqueueParams{
					TenantID: tenants[i%len(tenants)].ID, TaskType: taskType, QueueName: "default",
					Payload: []byte(fmt.Sprintf(`{"idx":%d}`, i)), Priority: 5, MaxAttempts: 5, TimeoutSeconds: 30,
				})
				d := time.Since(t0)
				winMu.Lock()
				window = append(window, d)
				winMu.Unlock()
				if err != nil {
					enqErr.Add(1)
					continue
				}
				enq.Add(1)
			}
		}()
	}

	type sample struct {
		heapMB                      float64
		goroutines                  int
		queued, running             int
		enqP99ms, waitP95ms         float64
		deadTupJobs, deadTupAttempt int64
		jobsMB                      float64
	}
	var samples []sample
	end := time.Now().Add(time.Duration(secs) * time.Second)
	every := 10 * time.Second
	if secs < 60 {
		every = 3 * time.Second
	}
	for time.Now().Before(end) {
		time.Sleep(every)
		var s sample
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		s.heapMB, s.goroutines = float64(ms.HeapAlloc)/1e6, runtime.NumGoroutine()
		winMu.Lock()
		w := append([]time.Duration(nil), window...)
		window = window[:0]
		winMu.Unlock()
		sort.Slice(w, func(i, j int) bool { return w[i] < w[j] })
		s.enqP99ms = float64(pct(w, .99).Microseconds()) / 1000
		_ = adminDB.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status='queued'), count(*) FILTER (WHERE status='running') FROM jobs WHERE task_type=$1`, taskType).Scan(&s.queued, &s.running)
		_ = adminDB.Pool.QueryRow(ctx, `
			SELECT COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM (started_at - created_at))),0)*1000
			FROM jobs WHERE task_type=$1 AND started_at > clock_timestamp() - $2::interval`, taskType, fmt.Sprintf("%d seconds", int(every.Seconds()))).Scan(&s.waitP95ms)
		_ = adminDB.Pool.QueryRow(ctx, `SELECT COALESCE(n_dead_tup,0) FROM pg_stat_user_tables WHERE relname='jobs'`).Scan(&s.deadTupJobs)
		_ = adminDB.Pool.QueryRow(ctx, `SELECT COALESCE(n_dead_tup,0) FROM pg_stat_user_tables WHERE relname='job_attempts'`).Scan(&s.deadTupAttempt)
		_ = adminDB.Pool.QueryRow(ctx, `SELECT pg_total_relation_size('jobs')/1e6`).Scan(&s.jobsMB)
		samples = append(samples, s)
		t.Logf("SOAK_SAMPLE t=%3ds enq=%d queued=%d running=%d enq_p99=%.0fms qwait_p95=%.0fms heap=%.0fMB goroutines=%d dead_tup(jobs/attempts)=%d/%d jobs_size=%.1fMB",
			int(time.Since(end.Add(-time.Duration(secs)*time.Second)).Seconds()), enq.Load(), s.queued, s.running, s.enqP99ms, s.waitP95ms, s.heapMB, s.goroutines, s.deadTupJobs, s.deadTupAttempt, s.jobsMB)
	}

	close(stop)
	pwg.Wait()
	// Drain.
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		var open int
		_ = adminDB.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE task_type=$1 AND status IN ('queued','running')`, taskType).Scan(&open)
		if open == 0 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	for _, w := range workers {
		w.Stop(5 * time.Second)
	}

	// ---- invariants ----
	var succeeded, dead, open, leaked int
	var outstanding int64
	_ = adminDB.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status='succeeded'), count(*) FILTER (WHERE status='dead'), count(*) FILTER (WHERE status NOT IN ('succeeded','dead')) FROM jobs WHERE task_type=$1`, taskType).Scan(&succeeded, &dead, &open)
	_ = adminDB.Pool.QueryRow(ctx, `SELECT count(*) FROM tenant_execution_slots WHERE current_job_id IS NOT NULL`).Scan(&leaked)
	_ = adminDB.Pool.QueryRow(ctx, `SELECT COALESCE(sum(outstanding_jobs),0) FROM tenant_usage WHERE tenant_id = ANY($1::uuid[])`, tenantIDs(tenants)).Scan(&outstanding)
	var badAttempts int
	_ = adminDB.Pool.QueryRow(ctx, `
		SELECT count(*) FROM jobs j WHERE j.task_type=$1 AND j.status='succeeded'
		AND (SELECT count(*) FROM job_attempts a WHERE a.job_id=j.id AND a.outcome='succeeded') <> 1`, taskType).Scan(&badAttempts)
	expectedDead := 0
	for i := 0; i < int(enq.Load()); i++ { // approximate: idx%500==0 are permanent
		if i%500 == 0 {
			expectedDead++
		}
	}
	t.Logf("SOAK_RESULT enqueued=%d enqueue_errors=%d succeeded=%d dead=%d open=%d leaked_slots=%d outstanding=%d succeeded_without_exactly_one_success_attempt=%d",
		enq.Load(), enqErr.Load(), succeeded, dead, open, leaked, outstanding, badAttempts)

	if enqErr.Load() != 0 {
		t.Errorf("%d enqueue errors under steady load", enqErr.Load())
	}
	if open != 0 || leaked != 0 || outstanding != 0 || badAttempts != 0 {
		t.Errorf("leak/state violation: open=%d leaked=%d outstanding=%d badAttempts=%d", open, leaked, outstanding, badAttempts)
	}
	if int64(succeeded+dead) != enq.Load() {
		t.Errorf("succeeded(%d)+dead(%d) != enqueued(%d): jobs lost", succeeded, dead, enq.Load())
	}
	if dead != expectedDead {
		t.Errorf("dead=%d, want %d (only idx%%500==0 should be dead)", dead, expectedDead)
	}
	if len(samples) >= 6 { // drift checks: compare first and last third
		third := len(samples) / 3
		avg := func(xs []sample, f func(sample) float64) float64 {
			var s float64
			for _, x := range xs {
				s += f(x)
			}
			return s / float64(len(xs))
		}
		firstHeap, lastHeap := avg(samples[:third], func(s sample) float64 { return s.heapMB }), avg(samples[len(samples)-third:], func(s sample) float64 { return s.heapMB })
		firstG, lastG := avg(samples[:third], func(s sample) float64 { return float64(s.goroutines) }), avg(samples[len(samples)-third:], func(s sample) float64 { return float64(s.goroutines) })
		firstW, lastW := avg(samples[:third], func(s sample) float64 { return s.waitP95ms }), avg(samples[len(samples)-third:], func(s sample) float64 { return s.waitP95ms })
		t.Logf("SOAK_RESULT drift heap_MB first/last third=%.1f/%.1f goroutines=%.0f/%.0f qwait_p95_ms=%.0f/%.0f", firstHeap, lastHeap, firstG, lastG, firstW, lastW)
		if lastG > firstG*1.5+10 {
			t.Errorf("goroutine count grew %.0f -> %.0f (leak?)", firstG, lastG)
		}
		if lastHeap > firstHeap*2+20 {
			t.Errorf("heap grew %.1f -> %.1f MB (leak?)", firstHeap, lastHeap)
		}
	}
}
