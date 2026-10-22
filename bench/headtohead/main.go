// Command headtohead measures QueueForge-style workloads against other queues using each
// system's NATIVE client, with the same workload shape as tests/load/throughput_test.go:
//
//  1. drain: enqueue N no-op jobs, THEN start W worker processes' worth of workers and time how
//     long it takes to run them all (jobs/s), verifying every job ran exactly once
//  2. idle latency: with workers running and the queue empty, enqueue one job at a time and time
//     enqueue -> handler executed (p50/p95/p99)
//
// Run one system at a time: -system asynq|river . This is a separate Go module so the comparison
// libraries never touch the main module's dependencies.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"time"
)

type result struct {
	system                    string
	drainJobsPerSec           float64
	dups, missing             int
	idleP50, idleP95, idleP99 time.Duration
	enqueuePerSec             float64
}

func pct(d []time.Duration, p float64) time.Duration {
	if len(d) == 0 {
		return 0
	}
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	return d[int(float64(len(d)-1)*p)]
}

func main() {
	system := flag.String("system", "asynq", "asynq | river")
	jobs := flag.Int("jobs", 3000, "jobs in the drain test")
	workers := flag.Int("workers", 1, "worker instances")
	conc := flag.Int("concurrency", 16, "concurrent handlers per worker instance")
	idle := flag.Int("idle", 150, "samples in the idle-latency test")
	redisAddr := flag.String("redis", "127.0.0.1:6379", "redis address (asynq)")
	riverCooldown := flag.Duration("river-cooldown", 0, "river FetchCooldown (0 = library default, 100ms)")
	asynqPoll := flag.Duration("asynq-poll", 0, "asynq TaskCheckInterval (0 = library default, 1s)")
	dbURL := flag.String("db", os.Getenv("BENCH_DATABASE_URL"), "postgres url (river)")
	flag.Parse()

	var r result
	var err error
	switch *system {
	case "asynq":
		r, err = runAsynq(*redisAddr, *asynqPoll, *jobs, *workers, *conc, *idle)
	case "river":
		r, err = runRiver(*dbURL, *riverCooldown, *jobs, *workers, *conc, *idle)
	default:
		err = fmt.Errorf("unknown system %q", *system)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
	fmt.Printf("H2H_RESULT system=%s jobs=%d workers=%d concurrency=%d enqueue_per_sec=%.0f drain_jobs_per_sec=%.0f duplicates=%d missing=%d idle_p50_ms=%.1f idle_p95_ms=%.1f idle_p99_ms=%.1f\n",
		r.system, *jobs, *workers, *conc, r.enqueuePerSec, r.drainJobsPerSec, r.dups, r.missing,
		float64(r.idleP50.Microseconds())/1000, float64(r.idleP95.Microseconds())/1000, float64(r.idleP99.Microseconds())/1000)
}
