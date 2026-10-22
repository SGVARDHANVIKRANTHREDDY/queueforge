package regression_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/domain"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/observability"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/postgres"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/worker"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
)

// captureRegisterer records the metric names a set of collectors would expose.
type captureRegisterer struct{ names map[string]bool }

var fqNameRe = regexp.MustCompile(`fqName: "([^"]+)"`)

func (c *captureRegisterer) Register(col prometheus.Collector) error {
	ch := make(chan *prometheus.Desc, 64)
	go func() { col.Describe(ch); close(ch) }()
	for d := range ch {
		if m := fqNameRe.FindStringSubmatch(d.String()); m != nil {
			c.names[m[1]] = true
		}
	}
	return nil
}
func (c *captureRegisterer) MustRegister(cols ...prometheus.Collector) {
	for _, col := range cols {
		_ = c.Register(col)
	}
}
func (c *captureRegisterer) Unregister(prometheus.Collector) bool { return true }

// BUG 7: the runbook alert "queueforge_jobs_queued > 1000" could never fire because that gauge was
// declared but never updated, and Runbook A told operators to inspect queueforge_db_pool_connections,
// a metric that did not exist. Every metric named in an alert rule or a runbook must really exist.
func TestAlertsAndRunbooksOnlyReferenceMetricsThatExist(t *testing.T) {
	e := setup(t)
	cap := &captureRegisterer{names: map[string]bool{}}
	observability.NewMetrics(cap)
	cap.MustRegister(observability.NewQueueStatsCollector(e.db.Pool), observability.NewPoolCollector(e.db.Pool))
	if len(cap.names) < 15 {
		t.Fatalf("captured only %d metric names; harness broken", len(cap.names))
	}
	// alert rules: any occurrence; runbooks: only metrics written as inline code (`queueforge_...`),
	// so container names in shell commands are not mistaken for metrics.
	ref := regexp.MustCompile("`?queueforge_[a-z0-9_]+")
	suffix := regexp.MustCompile(`_(bucket|sum|count)$`)
	files, _ := filepath.Glob("../../docs/runbooks/runbook-*.md")
	files = append(files, "../../deploy/alerts.yml")
	var missing []string
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range ref.FindAllString(string(b), -1) {
			if strings.HasSuffix(f, ".md") && !strings.HasPrefix(name, "`") {
				continue
			}
			name = strings.TrimPrefix(name, "`")
			base := suffix.ReplaceAllString(name, "")
			if !cap.names[name] && !cap.names[base] {
				missing = append(missing, filepath.Base(f)+": "+name)
			}
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("alerts/runbooks reference metrics that are never exported:\n  %s", strings.Join(missing, "\n  "))
	}
}

func TestQueueStatsCollectorReportsRealState(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	taskType := "reg.stats." + uuid.NewString()[:6]
	tn := e.tenant(t, "stats", 8)
	for i := 0; i < 3; i++ {
		e.enqueue(t, tn, taskType, 5)
	}
	e.bulkQueued(t, tn, taskType, 5, 2, "1 hour") // delayed: queued but NOT runnable
	if _, err := e.jobs.ClaimJob(ctx, uuid.New(), []string{"default"}, []string{taskType}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := postgres.NewWorkerRepo(e.db).Heartbeat(ctx, postgres.HeartbeatParams{WorkerID: uuid.New(), ProcessName: "t",
		Hostname: "h", Version: "v", Queues: []string{"default"}, TaskTypes: []string{taskType}}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)

	e.bulkTerminal(t, tn, 3, "40 days") // expired terminal jobs the retention service has not removed yet
	reg := prometheus.NewRegistry()
	reg.MustRegister(observability.NewQueueStatsCollector(e.db.Pool).WithRetention(30 * 24 * time.Hour))
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]float64{}
	for _, mf := range mfs {
		for _, m := range mf.GetMetric() {
			key := mf.GetName()
			for _, l := range m.GetLabel() {
				if l.GetName() == "queue" && l.GetValue() == "default" {
					key += "{default}"
				}
			}
			got[key] = m.GetGauge().GetValue()
		}
	}
	want := map[string]float64{
		"queueforge_jobs_queued{default}":   4, // 5 enqueued - 1 claimed
		"queueforge_jobs_runnable{default}": 2, // 3 due - 1 claimed (the 2 delayed ones are not due)
		"queueforge_jobs_running{default}":  1,
		"queueforge_workers_alive":          1,
		"queueforge_retention_backlog_jobs": 3,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v (all: %v)", k, got[k], v, got)
		}
	}
	if got["queueforge_oldest_runnable_job_age_seconds{default}"] <= 0 {
		t.Errorf("oldest runnable age should be > 0, got %v", got["queueforge_oldest_runnable_job_age_seconds{default}"])
	}
}

// Every metric field declared on observability.Metrics must be updated somewhere outside the
// declaration file. (Three gauges used to be declared and never touched: always zero, so any alert
// built on them could never fire.)
func TestEveryDeclaredMetricIsUpdatedSomewhere(t *testing.T) {
	typ := reflect.TypeOf(observability.Metrics{})
	var src strings.Builder
	for _, dir := range []string{"../../internal", "../../cmd"} {
		_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") ||
				strings.HasSuffix(path, "internal/observability/metrics.go") {
				return nil
			}
			b, _ := os.ReadFile(path)
			src.Write(b)
			return nil
		})
	}
	text := src.String()
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if !regexp.MustCompile(`\.` + name + `\b`).MatchString(text) {
			t.Errorf("metric field %s is declared but never updated anywhere: it will always read zero", name)
		}
	}
}

func counterValue(t *testing.T, reg *prometheus.Registry, name string, labels map[string]string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
	next:
		for _, m := range mf.GetMetric() {
			for k, v := range labels {
				found := false
				for _, l := range m.GetLabel() {
					if l.GetName() == k && l.GetValue() == v {
						found = true
					}
				}
				if !found {
					continue next
				}
			}
			return m.GetCounter().GetValue()
		}
	}
	return 0
}

// Failures must be visible in metrics: retries are counted, and dead-lettered jobs are counted as
// completed{outcome="dead"} (the DeadLetterSpike alert depends on it).
func TestWorkerMetricsCountRetriesAndDeadLetters(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	reg := prometheus.NewRegistry()
	metrics := observability.NewMetrics(reg)
	e.cfg.RetryBaseDelay, e.cfg.RetryMaxDelay = 5*time.Millisecond, 10*time.Millisecond
	e.cfg.ClaimPollInterval = 20 * time.Millisecond
	w := worker.NewWorker(e.cfg, e.jobs, postgres.NewWorkerRepo(e.db), metrics)
	permType, flakyType := "reg.perm."+uuid.NewString()[:5], "reg.flaky."+uuid.NewString()[:5]
	_ = w.Register(permType, func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return nil, domain.NewPermanentError(errors.New("never works"))
	})
	_ = w.Register(flakyType, func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("transient")
	})
	tn := e.tenant(t, "wm", 4)
	e.enqueue(t, tn, permType, 5)
	flaky, err := e.jobs.EnqueueJob(ctx, postgres.EnqueueParams{TenantID: tn.ID, TaskType: flakyType, QueueName: "default",
		Payload: []byte(`{}`), Priority: 5, MaxAttempts: 3, TimeoutSeconds: 30})
	if err != nil {
		t.Fatal(err)
	}
	w.Start()
	defer w.Stop(2 * time.Second)

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var dead int
		_ = e.db.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE tenant_id=$1 AND status='dead'`, tn.ID).Scan(&dead)
		if dead == 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond) // let the worker update metrics after the final commit
	_ = flaky
	if got := counterValue(t, reg, "queueforge_jobs_completed_total", map[string]string{"outcome": "dead"}); got != 2 {
		t.Errorf("completed{outcome=dead} = %v, want 2 (one permanent failure + one job that exhausted 3 attempts)", got)
	}
	if got := counterValue(t, reg, "queueforge_job_retries_total", map[string]string{"error_class": "retryable"}); got != 2 {
		t.Errorf("job_retries_total{retryable} = %v, want 2 (attempts 1 and 2 of the flaky job)", got)
	}
}

// An idle queue must report explicit zeros, not vanish (dashboards would show gaps and "no data"
// would be indistinguishable from "healthy and empty").
func TestQueueStatsCollectorReportsZerosForIdleQueues(t *testing.T) {
	e := setup(t) // fresh database: no jobs at all
	reg := prometheus.NewRegistry()
	reg.MustRegister(observability.NewQueueStatsCollector(e.db.Pool).WithQueues([]string{"default", "notifications"}))
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, mf := range mfs {
		if mf.GetName() == "queueforge_jobs_runnable" || mf.GetName() == "queueforge_oldest_runnable_job_age_seconds" {
			seen[mf.GetName()] = len(mf.GetMetric())
			for _, m := range mf.GetMetric() {
				if m.GetGauge().GetValue() != 0 {
					t.Errorf("%s should be 0 on an idle system, got %v", mf.GetName(), m.GetGauge().GetValue())
				}
			}
		}
	}
	if seen["queueforge_jobs_runnable"] != 2 || seen["queueforge_oldest_runnable_job_age_seconds"] != 2 {
		t.Fatalf("idle queues must have series: %v", seen)
	}
}
