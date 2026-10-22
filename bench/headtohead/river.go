package main

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

type noopArgs struct {
	Idx  int  `json:"idx"`
	Idle bool `json:"idle"`
}

func (noopArgs) Kind() string { return "noop" }

type noopWorker struct {
	river.WorkerDefaults[noopArgs]
	jobs   int
	seen   []atomic.Int32
	done   *atomic.Int64
	idleMu *sync.Mutex
	idleCh map[int]chan time.Time
}

func (w *noopWorker) Work(ctx context.Context, job *river.Job[noopArgs]) error {
	if job.Args.Idle {
		w.idleMu.Lock()
		ch := w.idleCh[job.Args.Idx]
		w.idleMu.Unlock()
		if ch != nil {
			ch <- time.Now()
		}
		return nil
	}
	if job.Args.Idx >= 0 && job.Args.Idx < w.jobs {
		w.seen[job.Args.Idx].Add(1)
	}
	w.done.Add(1)
	return nil
}

func runRiver(dbURL string, cooldown time.Duration, jobs, workers, conc, idle int) (result, error) {
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return result{}, err
	}
	defer admin.Close()
	name := fmt.Sprintf("h2h_river_%d", time.Now().UnixNano()%1_000_000)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		return result{}, err
	}
	defer admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	u, _ := url.Parse(dbURL)
	u.Path = "/" + name
	newPool := func(max int32) (*pgxpool.Pool, error) {
		cfg, err := pgxpool.ParseConfig(u.String())
		if err != nil {
			return nil, err
		}
		cfg.MaxConns = max
		return pgxpool.NewWithConfig(ctx, cfg)
	}

	pool, err := newPool(32)
	if err != nil {
		return result{}, err
	}
	defer pool.Close()
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		return result{}, err
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return result{}, fmt.Errorf("river migrate: %w", err)
	}

	w := &noopWorker{jobs: jobs, seen: make([]atomic.Int32, jobs), done: new(atomic.Int64), idleMu: new(sync.Mutex), idleCh: map[int]chan time.Time{}}
	mkClient := func(p *pgxpool.Pool) (*river.Client[pgx.Tx], error) {
		workersReg := river.NewWorkers()
		river.AddWorker(workersReg, w)
		cfg := &river.Config{
			Queues:  map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: conc}},
			Workers: workersReg,
		}
		if cooldown > 0 {
			cfg.FetchCooldown = cooldown
		}
		return river.NewClient[pgx.Tx](riverpgxv5.New(p), cfg)
	}

	// ---- enqueue: 16 concurrent inserters, each insert its own transaction (like QueueForge) ----
	enq, err := mkClient(pool)
	if err != nil {
		return result{}, err
	}
	var next atomic.Int64
	var wg sync.WaitGroup
	t0 := time.Now()
	for c := 0; c < 16; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= jobs {
					return
				}
				if _, err := enq.Insert(ctx, noopArgs{Idx: i}, nil); err != nil {
					fmt.Println("insert error:", err)
				}
			}
		}()
	}
	wg.Wait()
	enqPerSec := float64(jobs) / time.Since(t0).Seconds()
	if _, err := pool.Exec(ctx, "ANALYZE"); err != nil { // same courtesy the QueueForge benchmark gets
		return result{}, err
	}

	// ---- drain ----
	var clients []*river.Client[pgx.Tx]
	var pools []*pgxpool.Pool
	start := time.Now()
	for i := 0; i < workers; i++ {
		p, err := newPool(10) // each worker instance has its own pool, like the QueueForge benchmark
		if err != nil {
			return result{}, err
		}
		pools = append(pools, p)
		c, err := mkClient(p)
		if err != nil {
			return result{}, err
		}
		if err := c.Start(ctx); err != nil {
			return result{}, err
		}
		clients = append(clients, c)
	}
	deadline := time.After(200 * time.Second)
	for w.done.Load() < int64(jobs) {
		select {
		case <-deadline:
			return result{}, fmt.Errorf("timeout: %d/%d", w.done.Load(), jobs)
		case <-time.After(20 * time.Millisecond):
		}
	}
	drain := time.Since(start)
	r := result{system: "river", enqueuePerSec: enqPerSec, drainJobsPerSec: float64(jobs) / drain.Seconds()}
	for i := range w.seen {
		switch n := w.seen[i].Load(); {
		case n == 0:
			r.missing++
		case n > 1:
			r.dups++
		}
	}

	// ---- idle latency ----
	time.Sleep(500 * time.Millisecond)
	var lat []time.Duration
	for i := 0; i < idle; i++ {
		ch := make(chan time.Time, 1)
		w.idleMu.Lock()
		w.idleCh[i] = ch
		w.idleMu.Unlock()
		s := time.Now()
		if _, err := enq.Insert(ctx, noopArgs{Idx: i, Idle: true}, nil); err != nil {
			return result{}, err
		}
		select {
		case at := <-ch:
			lat = append(lat, at.Sub(s))
		case <-time.After(10 * time.Second):
			return result{}, fmt.Errorf("idle job %d never ran", i)
		}
		time.Sleep(time.Duration(10+i%40) * time.Millisecond)
	}
	for _, c := range clients {
		stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_ = c.Stop(stopCtx)
		cancel()
	}
	for _, p := range pools {
		p.Close()
	}
	r.idleP50, r.idleP95, r.idleP99 = pct(lat, .5), pct(lat, .95), pct(lat, .99)
	return r, nil
}
