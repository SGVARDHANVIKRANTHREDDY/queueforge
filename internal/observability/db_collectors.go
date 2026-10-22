package observability

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

// QueueStatsCollector exposes the state of the queue as seen in the database. It replaces gauges
// that were declared but never updated anywhere (so the backlog alert in the runbook could never
// fire). Values are computed at scrape time from the database and cached for a short TTL, so a
// misconfigured scraper cannot turn /metrics into a database load generator. A failed refresh keeps
// the last good values and bumps queueforge_stats_refresh_errors_total.
type QueueStatsCollector struct {
	pool *pgxpool.Pool
	ttl  time.Duration

	mu        sync.Mutex
	fetchedAt time.Time
	queues    []queueStat
	workers   workerStat

	queued, runnable, running, oldest, workersAlive, freshest, retentionBacklog *prometheus.Desc
	retention                                                                   time.Duration
	knownQueues                                                                 []string
	backlog                                                                     float64
	errs                                                                        prometheus.Counter
}

type queueStat struct {
	name                     string
	queued, runnable, active float64
	oldestRunnableAge        float64
}
type workerStat struct{ alive, freshestAge float64 }

func NewQueueStatsCollector(pool *pgxpool.Pool) *QueueStatsCollector {
	d := func(name, help string, labels ...string) *prometheus.Desc {
		return prometheus.NewDesc("queueforge_"+name, help, labels, nil)
	}
	return &QueueStatsCollector{
		pool: pool, ttl: 10 * time.Second,
		queued:           d("jobs_queued", "Jobs in status queued (including delayed and retry-backoff jobs).", "queue"),
		runnable:         d("jobs_runnable", "Queued jobs that are due now (run_at has passed).", "queue"),
		running:          d("jobs_running", "Jobs currently leased by a worker.", "queue"),
		oldest:           d("oldest_runnable_job_age_seconds", "Age of the oldest job that is due but not yet claimed. The single best 'is anything stuck?' signal.", "queue"),
		retentionBacklog: d("retention_backlog_jobs", "Terminal jobs older than the retention window that have not been deleted yet (capped at 100001). Growing = retention is falling behind and the table is growing without bound."),
		workersAlive:     d("workers_alive", "Worker processes with a heartbeat in the last minute."),
		freshest:         d("worker_freshest_heartbeat_age_seconds", "Seconds since the most recent worker heartbeat (-1 if no worker has ever reported)."),
		errs: prometheus.NewCounter(prometheus.CounterOpts{Name: "queueforge_stats_refresh_errors_total",
			Help: "Failed attempts to refresh queue statistics from the database."}),
	}
}

func (c *QueueStatsCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{c.queued, c.runnable, c.running, c.oldest, c.workersAlive, c.freshest, c.retentionBacklog} {
		ch <- d
	}
	c.errs.Describe(ch)
}

// WithQueues declares the queues that always get series. Without it, a queue with no queued or
// running jobs would have NO series at all (the query only returns queues that have rows), so
// dashboards show gaps and "no data" looks the same as "healthy and empty".
func (c *QueueStatsCollector) WithQueues(queues []string) *QueueStatsCollector {
	c.knownQueues = queues
	return c
}

// WithRetention enables the retention-backlog gauge for the given terminal-job retention window.
func (c *QueueStatsCollector) WithRetention(window time.Duration) *QueueStatsCollector {
	c.retention = window
	return c
}

func (c *QueueStatsCollector) refresh() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	rows, err := c.pool.Query(ctx, `
		SELECT queue_name,
		       count(*) FILTER (WHERE status = 'queued'),
		       count(*) FILTER (WHERE status = 'queued' AND run_at <= clock_timestamp()),
		       count(*) FILTER (WHERE status = 'running'),
		       COALESCE(EXTRACT(EPOCH FROM clock_timestamp() - min(run_at) FILTER (WHERE status = 'queued' AND run_at <= clock_timestamp())), 0)::float8
		FROM jobs WHERE status IN ('queued', 'running') GROUP BY queue_name`)
	if err != nil {
		c.errs.Inc()
		return
	}
	var qs []queueStat
	for rows.Next() {
		var q queueStat
		if err := rows.Scan(&q.name, &q.queued, &q.runnable, &q.active, &q.oldestRunnableAge); err != nil {
			rows.Close()
			c.errs.Inc()
			return
		}
		qs = append(qs, q)
	}
	rows.Close()
	have := map[string]bool{}
	for _, q := range qs {
		have[q.name] = true
	}
	for _, name := range c.knownQueues {
		if !have[name] {
			qs = append(qs, queueStat{name: name}) // explicit zeros
		}
	}
	var w workerStat
	if err := c.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE last_seen_at > clock_timestamp() - interval '1 minute')::float8,
		       COALESCE(EXTRACT(EPOCH FROM clock_timestamp() - max(last_seen_at)), -1)::float8
		FROM worker_instances`).Scan(&w.alive, &w.freshestAge); err != nil {
		c.errs.Inc()
		return
	}
	if c.retention > 0 {
		// Capped count over the retention index: bounded work however large the backlog is.
		var b float64
		if err := c.pool.QueryRow(ctx, `
			SELECT count(*)::float8 FROM (
				SELECT 1 FROM jobs
				WHERE status IN ('succeeded', 'dead', 'cancelled') AND completed_at <= statement_timestamp() - make_interval(secs => $1)
				LIMIT 100001) x`, c.retention.Seconds()).Scan(&b); err != nil {
			c.errs.Inc()
			return
		}
		c.backlog = b
	}
	c.queues, c.workers, c.fetchedAt = qs, w, time.Now() //nolint:appclock // cache TTL only, not queue logic
}

func (c *QueueStatsCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	if time.Since(c.fetchedAt) > c.ttl { //nolint:appclock // cache TTL only
		c.refresh()
	}
	qs, w := c.queues, c.workers
	c.mu.Unlock()
	for _, q := range qs {
		ch <- prometheus.MustNewConstMetric(c.queued, prometheus.GaugeValue, q.queued, q.name)
		ch <- prometheus.MustNewConstMetric(c.runnable, prometheus.GaugeValue, q.runnable, q.name)
		ch <- prometheus.MustNewConstMetric(c.running, prometheus.GaugeValue, q.active, q.name)
		ch <- prometheus.MustNewConstMetric(c.oldest, prometheus.GaugeValue, q.oldestRunnableAge, q.name)
	}
	if c.retention > 0 {
		ch <- prometheus.MustNewConstMetric(c.retentionBacklog, prometheus.GaugeValue, c.backlog)
	}
	ch <- prometheus.MustNewConstMetric(c.workersAlive, prometheus.GaugeValue, w.alive)
	ch <- prometheus.MustNewConstMetric(c.freshest, prometheus.GaugeValue, w.freshestAge)
	c.errs.Collect(ch)
}

// PoolCollector exposes pgx connection-pool statistics. The runbooks tell operators to look at pool
// saturation first when claims stall; acquire wait time is the number that proves it.
type PoolCollector struct {
	pool                                              *pgxpool.Pool
	conns, acquires, emptyAcquires, waitSeconds, errs *prometheus.Desc
}

func NewPoolCollector(pool *pgxpool.Pool) *PoolCollector {
	return &PoolCollector{
		pool:          pool,
		conns:         prometheus.NewDesc("queueforge_db_pool_connections", "Connections in the pool by state.", []string{"state"}, nil),
		acquires:      prometheus.NewDesc("queueforge_db_pool_acquires_total", "Connection acquisitions.", nil, nil),
		emptyAcquires: prometheus.NewDesc("queueforge_db_pool_empty_acquires_total", "Acquisitions that had to WAIT because every connection was busy (saturation).", nil, nil),
		waitSeconds:   prometheus.NewDesc("queueforge_db_pool_acquire_wait_seconds_total", "Total time spent waiting to acquire a connection.", nil, nil),
		errs:          prometheus.NewDesc("queueforge_db_pool_canceled_acquires_total", "Acquisitions abandoned because the caller's context ended while waiting.", nil, nil),
	}
}

func (p *PoolCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{p.conns, p.acquires, p.emptyAcquires, p.waitSeconds, p.errs} {
		ch <- d
	}
}

func (p *PoolCollector) Collect(ch chan<- prometheus.Metric) {
	s := p.pool.Stat()
	g := func(state string, v int32) {
		ch <- prometheus.MustNewConstMetric(p.conns, prometheus.GaugeValue, float64(v), state)
	}
	g("acquired", s.AcquiredConns())
	g("idle", s.IdleConns())
	g("total", s.TotalConns())
	g("max", s.MaxConns())
	ch <- prometheus.MustNewConstMetric(p.acquires, prometheus.CounterValue, float64(s.AcquireCount()))
	ch <- prometheus.MustNewConstMetric(p.emptyAcquires, prometheus.CounterValue, float64(s.EmptyAcquireCount()))
	ch <- prometheus.MustNewConstMetric(p.waitSeconds, prometheus.CounterValue, s.AcquireDuration().Seconds())
	ch <- prometheus.MustNewConstMetric(p.errs, prometheus.CounterValue, float64(s.CanceledAcquireCount()))
}
