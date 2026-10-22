package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type Metrics struct {
	RetentionDeletedTotal          *prometheus.CounterVec
	APIRequestsTotal               *prometheus.CounterVec
	APIRequestDurationSeconds      *prometheus.HistogramVec
	JobsEnqueuedTotal              *prometheus.CounterVec
	JobsClaimedTotal               *prometheus.CounterVec
	JobsCompletedTotal             *prometheus.CounterVec
	JobAttemptsTotal               *prometheus.CounterVec
	JobExecutionDurationSeconds    *prometheus.HistogramVec
	JobQueueWaitSeconds            *prometheus.HistogramVec
	JobRetriesTotal                *prometheus.CounterVec
	JobLeaseRecoveriesTotal        *prometheus.CounterVec
	SchedulerRunsTotal             *prometheus.CounterVec
	TenantRateLimitRejectionsTotal *prometheus.CounterVec
	RecoveryScanDurationSeconds    *prometheus.HistogramVec
}

var DefaultMetrics = NewMetrics(prometheus.DefaultRegisterer)

func NewMetrics(reg prometheus.Registerer) *Metrics {
	factory := promauto.With(reg)

	return &Metrics{
		RetentionDeletedTotal: factory.NewCounterVec(prometheus.CounterOpts{Name: "queueforge_retention_deleted_total", Help: "Rows removed by the retention service, by kind (jobs, idempotency_keys, workers)."}, []string{"kind"}),
		APIRequestsTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "queueforge_api_requests_total",
				Help: "Total API requests processed",
			},
			[]string{"route", "method", "status_class"},
		),
		APIRequestDurationSeconds: factory.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "queueforge_api_request_duration_seconds",
				Help:    "Duration of API requests in seconds",
				Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
			},
			[]string{"route", "method"},
		),
		JobsEnqueuedTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "queueforge_jobs_enqueued_total",
				Help: "Total jobs successfully accepted",
			},
			[]string{"queue"},
		),
		JobsClaimedTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "queueforge_jobs_claimed_total",
				Help: "Total jobs claimed by workers",
			},
			[]string{"queue"},
		),
		JobsCompletedTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "queueforge_jobs_completed_total",
				Help: "Total jobs completed by final outcome",
			},
			[]string{"queue", "outcome"},
		),
		JobAttemptsTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "queueforge_job_attempts_total",
				Help: "Total job attempt executions",
			},
			[]string{"queue", "outcome"},
		),
		JobExecutionDurationSeconds: factory.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "queueforge_job_execution_duration_seconds",
				Help:    "Execution duration of job handlers in seconds",
				Buckets: []float64{0.01, 0.05, 0.1, 0.5, 1, 5, 10, 30, 60, 120, 300},
			},
			[]string{"queue"},
		),
		JobQueueWaitSeconds: factory.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "queueforge_job_queue_wait_seconds",
				Help:    "Time jobs spent queued before starting claim",
				Buckets: []float64{0.01, 0.05, 0.1, 0.5, 1, 5, 10, 30, 60, 300, 600},
			},
			[]string{"queue"},
		),
		JobRetriesTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "queueforge_job_retries_total",
				Help: "Total job retry attempts scheduled",
			},
			[]string{"queue", "error_class"},
		),
		JobLeaseRecoveriesTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "queueforge_job_lease_recoveries_total",
				Help: "Total jobs recovered from expired worker leases",
			},
			[]string{"queue"},
		),
		SchedulerRunsTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "queueforge_scheduler_runs_total",
				Help: "Scheduler execution cycles count",
			},
			[]string{"outcome"},
		),
		TenantRateLimitRejectionsTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "queueforge_tenant_rate_limit_rejections_total",
				Help: "Total enqueue requests rejected due to rate limit",
			},
			[]string{"reason"},
		),
		RecoveryScanDurationSeconds: factory.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "queueforge_recovery_scan_duration_seconds",
				Help:    "Duration of lease recovery loop execution",
				Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1},
			},
			[]string{"result"},
		),
	}
}
