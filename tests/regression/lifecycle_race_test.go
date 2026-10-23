package regression_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/domain"
	"github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/postgres"
	"github.com/google/uuid"
)

// Lease-expiry and cancellation races.
//
// These tests pin the guarantees that fencing is supposed to provide: a worker that lost its lease
// (because it hung, was partitioned, or simply outlived the lease) can still finish its handler,
// but it can never change the job's state, and the cross-table counters stay consistent whichever
// side of a race wins. They talk to the repository directly (real PostgreSQL, real concurrency),
// so they are deterministic where it matters and need no worker processes.
//
// Leases are always whole seconds in the repository API (sub-second durations are truncated to
// "0 seconds"), so expiry is forced with SQL instead of sleeping.

// lcInvariants checks the cross-table invariants the property test also checks, after every race.
func (e *env) lcInvariants(t *testing.T, tn *domain.Tenant) {
	t.Helper()
	live := e.count(t, `SELECT count(*) FROM jobs WHERE tenant_id=$1 AND status IN ('queued','running')`, tn.ID)
	running := e.count(t, `SELECT count(*) FROM jobs WHERE tenant_id=$1 AND status='running'`, tn.ID)
	busy := e.count(t, `SELECT count(*) FROM tenant_execution_slots WHERE tenant_id=$1 AND current_job_id IS NOT NULL`, tn.ID)
	unfinished := e.count(t, `SELECT count(*) FROM job_attempts WHERE tenant_id=$1 AND finished_at IS NULL`, tn.ID)
	outstanding := e.count(t, `SELECT outstanding_jobs FROM tenant_usage WHERE tenant_id=$1`, tn.ID)
	if outstanding != live || busy != running || unfinished != running {
		t.Fatalf("invariant violated: outstanding=%d (want queued+running=%d), busy_slots=%d and unfinished_attempts=%d (want running=%d)",
			outstanding, live, busy, unfinished, running)
	}
}

// lcClaimOne claims exactly one job of taskType, or fails the test.
func (e *env) lcClaimOne(t *testing.T, taskType string) *postgres.ClaimResult {
	t.Helper()
	res, err := e.jobs.ClaimJobs(context.Background(), uuid.New(), []string{"default"}, []string{taskType}, time.Minute, 1)
	if err != nil || len(res) != 1 {
		t.Fatalf("claim: n=%d err=%v", len(res), err)
	}
	return res[0]
}

func (e *env) lcStatus(t *testing.T, jobID uuid.UUID) string {
	t.Helper()
	var s string
	if err := e.db.Pool.QueryRow(context.Background(), `SELECT status FROM jobs WHERE id=$1`, jobID).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

// A worker whose lease expired mid-handler must not be able to overwrite the recovered job.
func TestLifecycle_StaleWorkerAfterLeaseExpiryCannotOverwriteRecoveredJob(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	tn := e.tenant(t, "lease", 2)
	taskType := "lc.lease." + uuid.NewString()[:6]
	id := e.enqueue(t, tn, taskType, 5)
	backoff := domain.NewBackoffPolicy(time.Millisecond, 2*time.Millisecond, nil)

	a := e.lcClaimOne(t, taskType)
	tokenA := a.LeaseToken

	// Worker A "hangs": its lease runs out while its handler is still working.
	mustExec(t, e.db, `UPDATE jobs SET lease_expires_at = clock_timestamp() - interval '1 second' WHERE id=$1`, id)

	// An expired lease cannot be renewed, even before anyone has recovered the job.
	if _, err := e.jobs.RenewLease(ctx, id, tokenA, time.Minute); !errors.Is(err, domain.ErrLeaseLost) {
		t.Fatalf("renewing an expired lease: want ErrLeaseLost, got %v", err)
	}

	n, err := e.jobs.RecoverExpiredLeases(ctx, 10, backoff)
	if err != nil || n != 1 {
		t.Fatalf("recovery: n=%d err=%v", n, err)
	}
	mustExec(t, e.db, `UPDATE jobs SET run_at = clock_timestamp() - interval '1 second' WHERE id=$1`, id)

	b := e.lcClaimOne(t, taskType)
	tokenB := b.LeaseToken
	if tokenB == tokenA {
		t.Fatal("the re-claim reused the stale lease token")
	}

	// Worker A finally finishes its handler. Every way it can report back must be rejected.
	if err := e.jobs.CompleteJobSuccess(ctx, tn.ID, id, tokenA, json.RawMessage(`{"by":"A"}`), 5); !errors.Is(err, domain.ErrLeaseLost) {
		t.Fatalf("stale success: want ErrLeaseLost, got %v", err)
	}
	if err := e.jobs.CompleteJobFailure(ctx, tn.ID, id, tokenA, errors.New("late failure"), backoff, 5); !errors.Is(err, domain.ErrLeaseLost) {
		t.Fatalf("stale failure: want ErrLeaseLost, got %v", err)
	}
	if err := e.jobs.CompleteJobCancelled(ctx, tn.ID, id, tokenA, 5); !errors.Is(err, domain.ErrLeaseLost) {
		t.Fatalf("stale cancel: want ErrLeaseLost, got %v", err)
	}
	if n := e.count(t, `SELECT count(*) FROM jobs WHERE id=$1 AND status='running' AND lease_token=$2`, id, tokenB); n != 1 {
		t.Fatal("the stale worker disturbed a job that now belongs to worker B")
	}

	// Worker B completes normally; its result is the one that sticks.
	if err := e.jobs.CompleteJobSuccess(ctx, tn.ID, id, tokenB, json.RawMessage(`{"by":"B"}`), 7); err != nil {
		t.Fatal(err)
	}
	var by string
	if err := e.db.Pool.QueryRow(ctx, `SELECT result->>'by' FROM jobs WHERE id=$1`, id).Scan(&by); err != nil || by != "B" {
		t.Fatalf("stored result is from %q (err=%v), want B", by, err)
	}
	attempts, err := e.jobs.GetJobAttempts(ctx, tn.ID, id)
	if err != nil || len(attempts) != 2 {
		t.Fatalf("attempts: n=%d err=%v, want 2", len(attempts), err)
	}
	if attempts[0].Outcome == nil || *attempts[0].Outcome != domain.OutcomeLeaseExpired ||
		attempts[1].Outcome == nil || *attempts[1].Outcome != domain.OutcomeSucceeded {
		t.Fatalf("attempt outcomes: %v, %v; want lease_expired then succeeded", attempts[0].Outcome, attempts[1].Outcome)
	}
	e.lcInvariants(t, tn)
}

// A renewal that lands just before recovery runs must win: recovery leaves the job alone.
func TestLifecycle_RenewalBeatsRecovery(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	tn := e.tenant(t, "renew", 2)
	taskType := "lc.renew." + uuid.NewString()[:6]
	id := e.enqueue(t, tn, taskType, 5)

	c := e.lcClaimOne(t, taskType)
	// The lease is about to run out...
	mustExec(t, e.db, `UPDATE jobs SET lease_expires_at = clock_timestamp() + interval '2 seconds' WHERE id=$1`, id)
	// ...the renewer extends it...
	if _, err := e.jobs.RenewLease(ctx, id, c.LeaseToken, time.Minute); err != nil {
		t.Fatalf("renewal of a still-valid lease failed: %v", err)
	}
	// ...so recovery, even if it runs "late", must not touch the job.
	n, err := e.jobs.RecoverExpiredLeases(ctx, 10, domain.NewBackoffPolicy(time.Millisecond, 2*time.Millisecond, nil))
	if err != nil || n != 0 {
		t.Fatalf("recovery touched a renewed lease: n=%d err=%v", n, err)
	}
	if n := e.count(t, `SELECT count(*) FROM jobs WHERE id=$1 AND status='running' AND lease_token=$2 AND attempts_started=1`, id, c.LeaseToken); n != 1 {
		t.Fatal("job is no longer running on its original lease")
	}
	if err := e.jobs.CompleteJobSuccess(ctx, tn.ID, id, c.LeaseToken, json.RawMessage(`{}`), 1); err != nil {
		t.Fatal(err)
	}
	e.lcInvariants(t, tn)
}

// A queued job being cancelled while a worker claims it: exactly one coherent outcome.
func TestLifecycle_CancelQueuedJobRacingWithClaim(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	tn := e.tenant(t, "cancelclaim", 4)
	taskType := "lc.cancelclaim." + uuid.NewString()[:6]

	cancelWon, claimWon := 0, 0
	for i := 0; i < 100; i++ {
		id := e.enqueue(t, tn, taskType, 5)
		start := make(chan struct{})
		var wg sync.WaitGroup
		var claimed []*postgres.ClaimResult
		var claimErr, cancelErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			claimed, claimErr = e.jobs.ClaimJobs(ctx, uuid.New(), []string{"default"}, []string{taskType}, time.Minute, 1)
		}()
		go func() {
			defer wg.Done()
			<-start
			_, cancelErr = e.jobs.CancelJob(ctx, tn.ID, id)
		}()
		close(start)
		wg.Wait()
		if claimErr != nil {
			t.Fatalf("iteration %d: claim: %v", i, claimErr)
		}

		switch len(claimed) {
		case 0: // cancellation won before the claim
			cancelWon++
			if cancelErr != nil || e.lcStatus(t, id) != "cancelled" {
				t.Fatalf("iteration %d: nothing claimed but the job is %q (cancel err=%v)", i, e.lcStatus(t, id), cancelErr)
			}
			if n := e.count(t, `SELECT count(*) FROM job_attempts WHERE job_id=$1`, id); n != 0 {
				t.Fatalf("iteration %d: a cancelled-before-claim job has %d attempts", i, n)
			}
		case 1: // the claim won: the cancel could only ask the running job to stop
			claimWon++
			if cancelErr != nil {
				t.Fatalf("iteration %d: cancel of a just-claimed job returned %v", i, cancelErr)
			}
			if got := e.lcStatus(t, id); got != "running" {
				t.Fatalf("iteration %d: claimed job is %q, want running", i, got)
			}
			if n := e.count(t, `SELECT count(*) FROM jobs WHERE id=$1 AND cancellation_requested_at IS NOT NULL`, id); n != 1 {
				t.Fatalf("iteration %d: the cancel request was lost", i)
			}
			// the worker notices the flag on its next renewal and finishes the job as cancelled
			if err := e.jobs.CompleteJobCancelled(ctx, tn.ID, id, claimed[0].LeaseToken, 1); err != nil {
				t.Fatalf("iteration %d: completing as cancelled: %v", i, err)
			}
		default:
			t.Fatalf("iteration %d: one job claimed %d times", i, len(claimed))
		}
		e.lcInvariants(t, tn)
	}
	t.Logf("cancel won %d, claim won %d (both orders should normally occur)", cancelWon, claimWon)
}

// A cancel request racing with a successful completion: the completion wins, nothing is corrupted.
func TestLifecycle_CancelRequestRacingWithSuccessCompletion(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	tn := e.tenant(t, "cancelsuccess", 4)
	taskType := "lc.cancelsuccess." + uuid.NewString()[:6]

	for i := 0; i < 100; i++ {
		id := e.enqueue(t, tn, taskType, 5)
		c := e.lcClaimOne(t, taskType)
		start := make(chan struct{})
		var wg sync.WaitGroup
		var cancelErr, completeErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, cancelErr = e.jobs.CancelJob(ctx, tn.ID, id)
		}()
		go func() {
			defer wg.Done()
			<-start
			completeErr = e.jobs.CompleteJobSuccess(ctx, tn.ID, id, c.LeaseToken, json.RawMessage(`{}`), 1)
		}()
		close(start)
		wg.Wait()

		if completeErr != nil {
			t.Fatalf("iteration %d: a cancel request must not make a valid completion fail: %v", i, completeErr)
		}
		// Either the flag was set just before completion (nil) or the job was already terminal.
		if cancelErr != nil && !errors.Is(cancelErr, domain.ErrInvalidState) {
			t.Fatalf("iteration %d: unexpected cancel error: %v", i, cancelErr)
		}
		if got := e.lcStatus(t, id); got != "succeeded" {
			t.Fatalf("iteration %d: job is %q, want succeeded", i, got)
		}
		e.lcInvariants(t, tn)
	}
}

// Worker reports "cancelled" while another path reports "succeeded" for the same lease:
// exactly one wins, the other is fenced, and the counters change exactly once.
func TestLifecycle_CancelledVersusSuccessCompletionHasExactlyOneWinner(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	tn := e.tenant(t, "onewinner", 4)
	taskType := "lc.onewinner." + uuid.NewString()[:6]

	for i := 0; i < 100; i++ {
		id := e.enqueue(t, tn, taskType, 5)
		c := e.lcClaimOne(t, taskType)
		start := make(chan struct{})
		var wg sync.WaitGroup
		var cancelledErr, successErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			cancelledErr = e.jobs.CompleteJobCancelled(ctx, tn.ID, id, c.LeaseToken, 1)
		}()
		go func() {
			defer wg.Done()
			<-start
			successErr = e.jobs.CompleteJobSuccess(ctx, tn.ID, id, c.LeaseToken, json.RawMessage(`{}`), 1)
		}()
		close(start)
		wg.Wait()

		switch {
		case cancelledErr == nil && errors.Is(successErr, domain.ErrLeaseLost):
			if got := e.lcStatus(t, id); got != "cancelled" {
				t.Fatalf("iteration %d: cancel won but job is %q", i, got)
			}
		case successErr == nil && errors.Is(cancelledErr, domain.ErrLeaseLost):
			if got := e.lcStatus(t, id); got != "succeeded" {
				t.Fatalf("iteration %d: success won but job is %q", i, got)
			}
		default:
			t.Fatalf("iteration %d: want exactly one winner, got cancelledErr=%v successErr=%v", i, cancelledErr, successErr)
		}
		e.lcInvariants(t, tn)
	}
}

// Lease recovery racing with a late (but token-valid) completion: either side may win, never both.
func TestLifecycle_RecoveryRacingWithLateCompletion(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	tn := e.tenant(t, "recoverrace", 4)
	taskType := "lc.recoverrace." + uuid.NewString()[:6]
	backoff := domain.NewBackoffPolicy(time.Millisecond, 2*time.Millisecond, nil)

	completionWon, recoveryWon := 0, 0
	for i := 0; i < 100; i++ {
		id := e.enqueue(t, tn, taskType, 5)
		c := e.lcClaimOne(t, taskType)
		mustExec(t, e.db, `UPDATE jobs SET lease_expires_at = clock_timestamp() - interval '1 second' WHERE id=$1`, id)

		start := make(chan struct{})
		var wg sync.WaitGroup
		var recovered int
		var recErr, completeErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			recovered, recErr = e.jobs.RecoverExpiredLeases(ctx, 10, backoff)
		}()
		go func() {
			defer wg.Done()
			<-start
			completeErr = e.jobs.CompleteJobSuccess(ctx, tn.ID, id, c.LeaseToken, json.RawMessage(`{}`), 1)
		}()
		close(start)
		wg.Wait()
		if recErr != nil {
			t.Fatalf("iteration %d: recovery: %v", i, recErr)
		}

		switch {
		case completeErr == nil: // the late completion landed first; recovery must leave the job alone
			completionWon++
			if recovered != 0 || e.lcStatus(t, id) != "succeeded" {
				t.Fatalf("iteration %d: completion won but recovered=%d status=%q", i, recovered, e.lcStatus(t, id))
			}
		case errors.Is(completeErr, domain.ErrLeaseLost): // recovery won; the stale completion is fenced
			recoveryWon++
			if recovered != 1 || e.lcStatus(t, id) != "queued" {
				t.Fatalf("iteration %d: recovery won but recovered=%d status=%q", i, recovered, e.lcStatus(t, id))
			}
			// retire the requeued job so the next iteration starts clean
			if _, err := e.jobs.CancelJob(ctx, tn.ID, id); err != nil {
				t.Fatalf("iteration %d: cleaning up: %v", i, err)
			}
		default:
			t.Fatalf("iteration %d: unexpected completion error: %v", i, completeErr)
		}
		e.lcInvariants(t, tn)
	}
	t.Logf("late completion won %d, recovery won %d", completionWon, recoveryWon)
}
