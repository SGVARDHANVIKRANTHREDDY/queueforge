package worker

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/domain"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/postgres"
	"github.com/google/uuid"
)

// batchCompleter implements "group commit" for successful jobs: finished jobs queue up and are
// recorded together with one SQL statement. There is no timer, so there is no added latency when
// the worker is idle: while one flush is running, completions that arrive pile up and become the
// next batch, so batch size grows by itself exactly when throughput matters.
//
// Guarantees are unchanged: every job is still fenced by its own lease token. If a batch statement
// fails for any reason, every job in it falls back to the proven single-job statement.
type batchCompleter struct {
	repo     *postgres.JobRepo
	maxBatch int
	in       chan *successItem
	stopCh   chan struct{}
	exited   chan struct{}
	stopOnce sync.Once
}

type successItem struct {
	tenantID, jobID, leaseToken uuid.UUID
	result                      json.RawMessage
	durationMs                  int64
	done                        chan error
}

func newBatchCompleter(repo *postgres.JobRepo, maxBatch int) *batchCompleter {
	if maxBatch < 1 {
		maxBatch = 64
	}
	c := &batchCompleter{
		repo: repo, maxBatch: maxBatch,
		in:     make(chan *successItem, maxBatch*2),
		stopCh: make(chan struct{}), exited: make(chan struct{}),
	}
	go c.loop()
	return c
}

// Submit queues a completion and waits until it has been committed. It returns
// domain.ErrLeaseLost for a stale lease, like CompleteJobSuccess.
func (c *batchCompleter) Submit(ctx context.Context, it successItem) error {
	it.done = make(chan error, 1)
	select {
	case c.in <- &it:
	case <-c.stopCh:
		return c.direct(ctx, &it)
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-it.done:
		return err
	case <-c.exited:
		select {
		case err := <-it.done:
			return err
		default:
			return c.direct(ctx, &it)
		}
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *batchCompleter) direct(ctx context.Context, it *successItem) error {
	return c.repo.CompleteJobSuccess(ctx, it.tenantID, it.jobID, it.leaseToken, it.result, it.durationMs)
}

// Stop drains whatever is queued and ends the loop. Call it after in-flight handlers finished.
func (c *batchCompleter) Stop() {
	c.stopOnce.Do(func() { close(c.stopCh) })
	<-c.exited
}

func (c *batchCompleter) loop() {
	defer close(c.exited)
	for {
		var first *successItem
		select {
		case first = <-c.in:
		case <-c.stopCh:
			for {
				select {
				case it := <-c.in:
					c.flush([]*successItem{it})
				default:
					return
				}
			}
		}
		batch := []*successItem{first}
	drain:
		for len(batch) < c.maxBatch {
			select {
			case it := <-c.in:
				batch = append(batch, it)
			default:
				break drain
			}
		}
		c.flush(batch)
	}
}

func (c *batchCompleter) flush(batch []*successItem) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	items := make([]postgres.SuccessCompletion, len(batch))
	for i, it := range batch {
		items[i] = postgres.SuccessCompletion{
			TenantID: it.tenantID, JobID: it.jobID, LeaseToken: it.leaseToken,
			Result: it.result, DurationMs: it.durationMs,
		}
	}
	ok, err := c.repo.CompleteJobsSuccessBatch(ctx, items)
	if err != nil {
		slog.Warn("batch completion failed; falling back to per-job completion", "error", err, "size", len(batch))
		for _, it := range batch {
			it.done <- c.direct(ctx, it)
		}
		return
	}
	for i, it := range batch {
		if ok[i] {
			it.done <- nil
		} else {
			it.done <- domain.ErrLeaseLost
		}
	}
}
