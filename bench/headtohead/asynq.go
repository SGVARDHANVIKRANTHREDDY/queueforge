package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hibiken/asynq"
)

func runAsynq(addr string, poll time.Duration, jobs, workers, conc, idle int) (result, error) {
	opt := asynq.RedisClientOpt{Addr: addr}
	client := asynq.NewClient(opt)
	defer client.Close()
	insp := asynq.NewInspector(opt)
	defer insp.Close()
	_, _ = insp.DeleteAllPendingTasks("default")

	seen := make([]atomic.Int32, jobs)
	var done atomic.Int64
	var idleMu sync.Mutex
	idleCh := map[int]chan time.Time{}

	mux := asynq.NewServeMux()
	mux.HandleFunc("noop", func(ctx context.Context, t *asynq.Task) error {
		var p struct {
			Idx  int  `json:"idx"`
			Idle bool `json:"idle"`
		}
		_ = json.Unmarshal(t.Payload(), &p)
		if p.Idle {
			idleMu.Lock()
			ch := idleCh[p.Idx]
			idleMu.Unlock()
			if ch != nil {
				ch <- time.Now()
			}
			return nil
		}
		if p.Idx >= 0 && p.Idx < jobs {
			seen[p.Idx].Add(1)
		}
		done.Add(1)
		return nil
	})

	// ---- enqueue (16 concurrent clients, like the QueueForge benchmark) ----
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
				b, _ := json.Marshal(map[string]any{"idx": i})
				if _, err := client.Enqueue(asynq.NewTask("noop", b), asynq.MaxRetry(3)); err != nil {
					fmt.Println("enqueue error:", err)
				}
			}
		}()
	}
	wg.Wait()
	enqPerSec := float64(jobs) / time.Since(t0).Seconds()

	// ---- drain ----
	var servers []*asynq.Server
	start := time.Now()
	for w := 0; w < workers; w++ {
		srv := asynq.NewServer(opt, asynq.Config{Concurrency: conc, TaskCheckInterval: poll})
		if err := srv.Start(mux); err != nil {
			return result{}, err
		}
		servers = append(servers, srv)
	}
	deadline := time.After(240 * time.Second)
	for done.Load() < int64(jobs) {
		select {
		case <-deadline:
			return result{}, fmt.Errorf("timeout: %d/%d", done.Load(), jobs)
		case <-time.After(20 * time.Millisecond):
		}
	}
	drain := time.Since(start)
	r := result{system: "asynq", enqueuePerSec: enqPerSec, drainJobsPerSec: float64(jobs) / drain.Seconds()}
	for i := range seen {
		switch n := seen[i].Load(); {
		case n == 0:
			r.missing++
		case n > 1:
			r.dups++
		}
	}

	// ---- idle latency (workers keep running) ----
	time.Sleep(500 * time.Millisecond)
	var lat []time.Duration
	for i := 0; i < idle; i++ {
		ch := make(chan time.Time, 1)
		idleMu.Lock()
		idleCh[i] = ch
		idleMu.Unlock()
		b, _ := json.Marshal(map[string]any{"idx": i, "idle": true})
		s := time.Now()
		if _, err := client.Enqueue(asynq.NewTask("noop", b), asynq.MaxRetry(1)); err != nil {
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
	for _, s := range servers {
		s.Shutdown()
	}
	r.idleP50, r.idleP95, r.idleP99 = pct(lat, .5), pct(lat, .95), pct(lat, .99)
	return r, nil
}
