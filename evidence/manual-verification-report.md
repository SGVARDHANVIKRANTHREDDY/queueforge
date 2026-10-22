# QueueForge Manual Verification and Launch Checklist Report

Date: 2026-10-22
Repository: github.com/SGVARDHANVIKRANTHREDDY/queueforge
Workspace: Backend Project

## Executive result

The local Go module builds and the Docker images build. The full checklist is not green on this Windows host. The main blockers are: the API compose stack cannot bind host port 5432 because another PostgreSQL process owns it; the Windows Go toolchain has CGO disabled, so race tests cannot start; the chaos tests use Unix-only syscall fields; Python, psql, jq, gitleaks, and trivy are unavailable; and GitHub push is blocked by SSH authentication.

## Exact command outputs

### Phase 0

Commands: `git --version; go version; docker --version; python3 --version; psql --version; curl --version; jq --version`

Output:

    git version 2.50.1.windows.1
    go version go1.27.0 windows/amd64
    Docker version 29.7.2, build a7dcaa6
    Python was not found; run without arguments to install from the Microsoft Store, or disable this shortcut from Settings > Apps > Advanced app settings > App execution aliases.
    psql : The term 'psql' is not recognized...
    curl 8.16.0 ...
    jq : The term 'jq' is not recognized...

### Build and static checks

`go mod tidy`: completed with no output.
`gofmt -l .`: completed with no output.
`go build ./...`: completed successfully with no output.

`go vet ./...` output:

    # github.com/SGVARDHANVIKRANTHREDDY/queueforge/tests/chaos_test
    vet.exe: tests\\chaos\\kill9_test.go:68:40: unknown field Setpgid in struct literal of type syscall.SysProcAttr

The checklist repository commands were also run. `git init` reported the repository was reinitialized; `git commit -m "add license"` reported no changes to commit because LICENSE was already committed.

### Phase 3

The disposable PostgreSQL container `qf-pg` was created. Initial readiness output was `no response`; after startup it reported:

    /var/run/postgresql:5432 - accepting connections

The container was stopped for the compose quickstart as instructed.

### Phase 5 test outputs

Captured files:

#### evidence/bench-baseline.txt

``text
Windows PowerShell execution: see command output in session; no BENCH_RESULT was produced if tests failed.

``

#### evidence/bench-batch.txt

``text
Windows PowerShell execution: see command output in session; no BENCH_RESULT was produced if tests failed.

``

#### evidence/bench-idle.txt

``text
=== RUN   TestBenchmark_IdleLatency
    throughput_test.go:252: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestBenchmark_IdleLatency (0.01s)
FAIL
FAIL	github.com/SGVARDHANVIKRANTHREDDY/queueforge/tests/load	4.913s
FAIL

``

#### evidence/chaos-kill9.txt

``text
go : # github.com/SGVARDHANVIKRANTHREDDY/queueforge/tests/chaos_test 
[github.com/SGVARDHANVIKRANTHREDDY/queueforge/tests/chaos.test]
At line:2 char:104
+ ... h-idle.txt; go test -count=1 -v -run RealKill9 ./tests/chaos/ 2>&1 |  ...
+                 ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
    + CategoryInfo          : NotSpecified: (# github.com/SG...sts/chaos.test]:String) [], RemoteException
    + FullyQualifiedErrorId : NativeCommandError
 
tests\chaos\kill9_test.go:68:40: unknown field Setpgid in struct literal of type syscall.SysProcAttr
tests\chaos\kill9_test.go:77:29: undefined: syscall.Kill
tests\chaos\kill9_test.go:94:20: undefined: syscall.Kill
tests\chaos\netfault_test.go:72:40: unknown field Setpgid in struct literal of type syscall.SysProcAttr
tests\chaos\netfault_test.go:82:16: undefined: syscall.Kill
tests\chaos\pg_outage_test.go:85:40: unknown field Setpgid in struct literal of type syscall.SysProcAttr
tests\chaos\pg_outage_test.go:98:16: undefined: syscall.Kill
FAIL	github.com/SGVARDHANVIKRANTHREDDY/queueforge/tests/chaos [build failed]
FAIL

``

#### evidence/chaos-network.txt

``text
go : # github.com/SGVARDHANVIKRANTHREDDY/queueforge/tests/chaos_test 
[github.com/SGVARDHANVIKRANTHREDDY/queueforge/tests/chaos.test]
At line:2 char:233
+ ... STRICT='1'; go test -count=1 -v -p 1 -run NetworkFaults ./tests/chaos ...
+                 ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
    + CategoryInfo          : NotSpecified: (# github.com/SG...sts/chaos.test]:String) [], RemoteException
    + FullyQualifiedErrorId : NativeCommandError
 
tests\chaos\kill9_test.go:68:40: unknown field Setpgid in struct literal of type syscall.SysProcAttr
tests\chaos\kill9_test.go:77:29: undefined: syscall.Kill
tests\chaos\kill9_test.go:94:20: undefined: syscall.Kill
tests\chaos\netfault_test.go:72:40: unknown field Setpgid in struct literal of type syscall.SysProcAttr
tests\chaos\netfault_test.go:82:16: undefined: syscall.Kill
tests\chaos\pg_outage_test.go:85:40: unknown field Setpgid in struct literal of type syscall.SysProcAttr
tests\chaos\pg_outage_test.go:98:16: undefined: syscall.Kill
FAIL	github.com/SGVARDHANVIKRANTHREDDY/queueforge/tests/chaos [build failed]
FAIL

``

#### evidence/chaos-outage.txt

``text
go : # github.com/SGVARDHANVIKRANTHREDDY/queueforge/tests/chaos_test 
[github.com/SGVARDHANVIKRANTHREDDY/queueforge/tests/chaos.test]
At line:2 char:89
+ ... art qf-pg'; go test -count=1 -v -p 1 -timeout 20m -run PostgresOutage ...
+                 ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
    + CategoryInfo          : NotSpecified: (# github.com/SG...sts/chaos.test]:String) [], RemoteException
    + FullyQualifiedErrorId : NativeCommandError
 
tests\chaos\kill9_test.go:68:40: unknown field Setpgid in struct literal of type syscall.SysProcAttr
tests\chaos\kill9_test.go:77:29: undefined: syscall.Kill
tests\chaos\kill9_test.go:94:20: undefined: syscall.Kill
tests\chaos\netfault_test.go:72:40: unknown field Setpgid in struct literal of type syscall.SysProcAttr
tests\chaos\netfault_test.go:82:16: undefined: syscall.Kill
tests\chaos\pg_outage_test.go:85:40: unknown field Setpgid in struct literal of type syscall.SysProcAttr
tests\chaos\pg_outage_test.go:98:16: undefined: syscall.Kill
FAIL	github.com/SGVARDHANVIKRANTHREDDY/queueforge/tests/chaos [build failed]
FAIL

``

#### evidence/focused-ratelimiter.txt

``text
go: -race requires cgo; enable cgo by setting CGO_ENABLED=1

``

#### evidence/focused-redact.txt

``text
go: -race requires cgo; enable cgo by setting CGO_ENABLED=1

``

#### evidence/focused-regression.txt

``text
/usr/bin/bash: line 1: TestCompleteJobsSuccessBatch: command not found

``

#### evidence/focused-safecall.txt

``text
go: -race requires cgo; enable cgo by setting CGO_ENABLED=1

``

#### evidence/focused-security.txt

``text
/usr/bin/bash: line 1: TestBruteForceCannotBeBypassed: command not found

``

#### evidence/go-race.txt

``text
go : go: -race requires cgo; enable cgo by setting CGO_ENABLED=1
At line:2 char:1
+ go test -race -count=1 -p 1 -timeout 30m ./... 2>&1 | Tee-Object evid ...
+ ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
    + CategoryInfo          : NotSpecified: (go: -race requi...g CGO_ENABLED=1:String) [], RemoteException
    + FullyQualifiedErrorId : NativeCommandError
 

``

#### evidence/go-short.txt

``text
go : # github.com/SGVARDHANVIKRANTHREDDY/queueforge/tests/chaos_test 
[github.com/SGVARDHANVIKRANTHREDDY/queueforge/tests/chaos.test]
At line:2 char:58
+ ... Force evidence | Out-Null; go test -short -count=1 ./... 2>&1 | Tee-O ...
+                                ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
    + CategoryInfo          : NotSpecified: (# github.com/SG...sts/chaos.test]:String) [], RemoteException
    + FullyQualifiedErrorId : NativeCommandError
 
tests\chaos\kill9_test.go:68:40: unknown field Setpgid in struct literal of type syscall.SysProcAttr
tests\chaos\kill9_test.go:77:29: undefined: syscall.Kill
tests\chaos\kill9_test.go:94:20: undefined: syscall.Kill
tests\chaos\netfault_test.go:72:40: unknown field Setpgid in struct literal of type syscall.SysProcAttr
tests\chaos\netfault_test.go:82:16: undefined: syscall.Kill
tests\chaos\pg_outage_test.go:85:40: unknown field Setpgid in struct literal of type syscall.SysProcAttr
tests\chaos\pg_outage_test.go:98:16: undefined: syscall.Kill
?   	github.com/SGVARDHANVIKRANTHREDDY/queueforge/cmd/admin	[no test files]
?   	github.com/SGVARDHANVIKRANTHREDDY/queueforge/cmd/api	[no test files]
?   	github.com/SGVARDHANVIKRANTHREDDY/queueforge/cmd/worker	[no test files]
?   	github.com/SGVARDHANVIKRANTHREDDY/queueforge/examples/echo-handler	[no test files]
ok  	github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/auth	4.796s
ok  	github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/config	1.653s
ok  	github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/domain	1.915s
?   	github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/httpapi	[no test files]
ok  	github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/observability	4.657s
ok  	github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/postgres	2.395s
?   	github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/recovery	[no test files]
?   	github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/retention	[no test files]
?   	github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/scheduler	[no test files]
?   	github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/testutil	[no test files]
ok  	github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/worker	4.870s
?   	github.com/SGVARDHANVIKRANTHREDDY/queueforge/migrations	[no test files]
?   	github.com/SGVARDHANVIKRANTHREDDY/queueforge/outbox	[no test files]
--- FAIL: TestEnqueueAndWait_EndToEndWithRealWorker (0.02s)
    client_test.go:76: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestRetryAfterLostResponseCreatesExactlyOneJob (0.00s)
    client_test.go:107: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestRetryHonoursRetryAfterHeader (0.00s)
    client_test.go:136: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestClientErrorsAreNotRetried (0.00s)
    client_test.go:161: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestContextCancellationStopsRetrying (0.00s)
    client_test.go:180: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
FAIL
FAIL	github.com/SGVARDHANVIKRANTHREDDY/queueforge/sdk/go/queueforge	5.626s
FAIL	github.com/SGVARDHANVIKRANTHREDDY/queueforge/tests/chaos [build failed]
--- FAIL: TestStage1_VerticalSlice (0.01s)
    integration_test.go:33: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestStage2_IdempotencyAndRecovery (0.00s)
    integration_test.go:33: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestStage3_SchedulesQuotasAndDelayedJobs (0.00s)
    integration_test.go:33: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
FAIL
FAIL	github.com/SGVARDHANVIKRANTHREDDY/queueforge/tests/integration	5.594s
ok  	github.com/SGVARDHANVIKRANTHREDDY/queueforge/tests/load	4.019s
--- FAIL: TestStateMachine (0.02s)
    state_machine_test.go:65: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
FAIL
FAIL	github.com/SGVARDHANVIKRANTHREDDY/queueforge/tests/property	2.799s
--- FAIL: TestCompleteJobsSuccessBatch_MatchesSingleCompletionSemantics (0.02s)
    batch_completion_test.go:21: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestEnqueueJobTx_CommitsWithCallerTransaction (0.00s)
    enqueue_tx_test.go:48: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestEnqueueJobTx_RollsBackWithCallerTransaction (0.00s)
    enqueue_tx_test.go:69: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestEnqueueJobTx_QuotaRejectionLeavesCallerTransactionUsable (0.00s)
    enqueue_tx_test.go:86: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestAlertsAndRunbooksOnlyReferenceMetricsThatExist (0.00s)
    observability_test.go:50: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestQueueStatsCollectorReportsRealState (0.00s)
    observability_test.go:87: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestWorkerMetricsCountRetriesAndDeadLetters (0.00s)
    observability_test.go:198: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestQueueStatsCollectorReportsZerosForIdleQueues (0.00s)
    observability_test.go:244: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestClaim_DoesNotIdleWhenTopTenantSlotIsContended (0.00s)
    regression_test.go:75: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestRecovery_PoisonJobDoesNotBlockHealthyJobs (0.00s)
    regression_test.go:110: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestClaim_ReturnsRealRunAt (0.00s)
    regression_test.go:166: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestEnqueue_TimestampsComeFromDatabaseClock (0.00s)
    regression_test.go:214: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestClaim_CostIndependentOfForeignTaskTypeBacklog (0.00s)
    regression_test.go:280: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestClaim_CostIndependentOfDelayedBacklog (0.00s)
    regression_test.go:296: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestClaim_SaturatedTenantDoesNotStarveOthers (0.00s)
    regression_test.go:312: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestClaim_PriorityThenFIFOAcrossBatches (0.00s)
    regression_test.go:361: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestClaim_ConcurrentWorkersNeverDoubleClaim (0.00s)
    regression_test.go:415: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestRetention_DeletesOnlyOldTerminalJobsAndTheirChildren (0.00s)
    retention_test.go:39: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestRetention_DrainsABacklogLargerThanOneBatch (0.00s)
    retention_test.go:106: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestRetention_PoisonRowIsIsolatedAndReported (0.00s)
    retention_test.go:122: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestRetention_SteadyStateCostIndependentOfTableSize (0.00s)
    retention_test.go:153: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
FAIL
FAIL	github.com/SGVARDHANVIKRANTHREDDY/queueforge/tests/regression	4.134s
--- FAIL: TestBatchEnqueue_CreatesAllJobsAndIsIdempotent (0.02s)
    batch_test.go:46: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestBatchEnqueue_IsAllOrNothing (0.00s)
    batch_test.go:76: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestBatchEnqueue_RejectsBadSizesAndEntries (0.00s)
    batch_test.go:97: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestBruteForceIsThrottledBeforeTouchingTheDatabase (0.00s)
    security_test.go:150: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestExpiredAndRevokedKeysAreRejected (0.00s)
    security_test.go:179: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestKeyRotationWithOverlap (0.00s)
    security_test.go:206: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestLastUsedAtIsNotRewrittenOnEveryRequest (0.00s)
    security_test.go:231: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestAuditLogRecordsControlPlaneActionsOnly (0.00s)
    security_test.go:270: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestAuditLogIsAppendOnly (0.00s)
    security_test.go:294: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestWorkersEndpointDoesNotLeakInfrastructure (0.00s)
    security_test.go:312: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestTenantIsolationFuzz (0.00s)
    security_test.go:334: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestTraceIDPropagatesFromRequestToStoredJob (0.00s)
    security_test.go:433: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestBruteForceCannotBeBypassedWithSpoofedForwardingHeaders (0.00s)
    spoofed_ip_test.go:14: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
FAIL
FAIL	github.com/SGVARDHANVIKRANTHREDDY/queueforge/tests/security	4.761s
?   	github.com/SGVARDHANVIKRANTHREDDY/queueforge/workerkit	[no test files]
FAIL

``

#### evidence/go-short-corrected.txt

``text
?   	github.com/SGVARDHANVIKRANTHREDDY/queueforge/cmd/admin	[no test files]
?   	github.com/SGVARDHANVIKRANTHREDDY/queueforge/cmd/api	[no test files]
?   	github.com/SGVARDHANVIKRANTHREDDY/queueforge/cmd/worker	[no test files]
?   	github.com/SGVARDHANVIKRANTHREDDY/queueforge/examples/echo-handler	[no test files]
# github.com/SGVARDHANVIKRANTHREDDY/queueforge/tests/chaos_test [github.com/SGVARDHANVIKRANTHREDDY/queueforge/tests/chaos.test]
tests\chaos\kill9_test.go:68:40: unknown field Setpgid in struct literal of type syscall.SysProcAttr
tests\chaos\kill9_test.go:77:29: undefined: syscall.Kill
tests\chaos\kill9_test.go:94:20: undefined: syscall.Kill
tests\chaos\netfault_test.go:72:40: unknown field Setpgid in struct literal of type syscall.SysProcAttr
tests\chaos\netfault_test.go:82:16: undefined: syscall.Kill
tests\chaos\pg_outage_test.go:85:40: unknown field Setpgid in struct literal of type syscall.SysProcAttr
tests\chaos\pg_outage_test.go:98:16: undefined: syscall.Kill
ok  	github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/auth	5.538s
ok  	github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/config	1.104s
ok  	github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/domain	2.387s
?   	github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/httpapi	[no test files]
ok  	github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/observability	5.592s
ok  	github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/postgres	2.752s
?   	github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/recovery	[no test files]
?   	github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/retention	[no test files]
?   	github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/scheduler	[no test files]
?   	github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/testutil	[no test files]
ok  	github.com/SGVARDHANVIKRANTHREDDY/queueforge/internal/worker	5.916s
?   	github.com/SGVARDHANVIKRANTHREDDY/queueforge/migrations	[no test files]
?   	github.com/SGVARDHANVIKRANTHREDDY/queueforge/outbox	[no test files]
--- FAIL: TestEnqueueAndWait_EndToEndWithRealWorker (0.04s)
    client_test.go:76: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
--- FAIL: TestRetryAfterLostResponseCreatesExactlyOneJob (0.03s)
    client_test.go:107: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
--- FAIL: TestRetryHonoursRetryAfterHeader (0.03s)
    client_test.go:136: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
--- FAIL: TestClientErrorsAreNotRetried (0.04s)
    client_test.go:161: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
--- FAIL: TestContextCancellationStopsRetrying (0.04s)
    client_test.go:180: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
FAIL
FAIL	github.com/SGVARDHANVIKRANTHREDDY/queueforge/sdk/go/queueforge	6.048s
FAIL	github.com/SGVARDHANVIKRANTHREDDY/queueforge/tests/chaos [build failed]
--- FAIL: TestStage1_VerticalSlice (0.04s)
    integration_test.go:33: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
--- FAIL: TestStage2_IdempotencyAndRecovery (0.04s)
    integration_test.go:33: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
--- FAIL: TestStage3_SchedulesQuotasAndDelayedJobs (0.03s)
    integration_test.go:33: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
FAIL
FAIL	github.com/SGVARDHANVIKRANTHREDDY/queueforge/tests/integration	6.019s
ok  	github.com/SGVARDHANVIKRANTHREDDY/queueforge/tests/load	6.022s
--- FAIL: TestStateMachine (0.10s)
    state_machine_test.go:65: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
FAIL
FAIL	github.com/SGVARDHANVIKRANTHREDDY/queueforge/tests/property	2.818s
--- FAIL: TestCompleteJobsSuccessBatch_MatchesSingleCompletionSemantics (0.04s)
    batch_completion_test.go:21: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
--- FAIL: TestEnqueueJobTx_CommitsWithCallerTransaction (0.03s)
    enqueue_tx_test.go:48: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
--- FAIL: TestEnqueueJobTx_RollsBackWithCallerTransaction (0.03s)
    enqueue_tx_test.go:69: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
--- FAIL: TestEnqueueJobTx_QuotaRejectionLeavesCallerTransactionUsable (0.03s)
    enqueue_tx_test.go:86: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
--- FAIL: TestAlertsAndRunbooksOnlyReferenceMetricsThatExist (0.03s)
    observability_test.go:50: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
--- FAIL: TestQueueStatsCollectorReportsRealState (0.03s)
    observability_test.go:87: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
--- FAIL: TestWorkerMetricsCountRetriesAndDeadLetters (0.02s)
    observability_test.go:198: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
--- FAIL: TestQueueStatsCollectorReportsZerosForIdleQueues (0.02s)
    observability_test.go:244: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
--- FAIL: TestClaim_DoesNotIdleWhenTopTenantSlotIsContended (0.02s)
    regression_test.go:75: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
--- FAIL: TestRecovery_PoisonJobDoesNotBlockHealthyJobs (0.03s)
    regression_test.go:110: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
--- FAIL: TestClaim_ReturnsRealRunAt (0.02s)
    regression_test.go:166: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
--- FAIL: TestEnqueue_TimestampsComeFromDatabaseClock (0.03s)
    regression_test.go:214: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
--- FAIL: TestClaim_CostIndependentOfForeignTaskTypeBacklog (0.02s)
    regression_test.go:280: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
--- FAIL: TestClaim_CostIndependentOfDelayedBacklog (0.02s)
    regression_test.go:296: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
--- FAIL: TestClaim_SaturatedTenantDoesNotStarveOthers (0.03s)
    regression_test.go:312: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
--- FAIL: TestClaim_PriorityThenFIFOAcrossBatches (0.03s)
    regression_test.go:361: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
--- FAIL: TestClaim_ConcurrentWorkersNeverDoubleClaim (0.02s)
    regression_test.go:415: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
--- FAIL: TestRetention_DeletesOnlyOldTerminalJobsAndTheirChildren (0.03s)
    retention_test.go:39: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
--- FAIL: TestRetention_DrainsABacklogLargerThanOneBatch (0.02s)
    retention_test.go:106: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
--- FAIL: TestRetention_PoisonRowIsIsolatedAndReported (0.04s)
    retention_test.go:122: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
--- FAIL: TestRetention_SteadyStateCostIndependentOfTableSize (0.03s)
    retention_test.go:153: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
FAIL
FAIL	github.com/SGVARDHANVIKRANTHREDDY/queueforge/tests/regression	6.671s
--- FAIL: TestBatchEnqueue_CreatesAllJobsAndIsIdempotent (0.04s)
    batch_test.go:46: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
--- FAIL: TestBatchEnqueue_IsAllOrNothing (0.03s)
    batch_test.go:76: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
--- FAIL: TestBatchEnqueue_RejectsBadSizesAndEntries (0.03s)
    batch_test.go:97: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
--- FAIL: TestBruteForceIsThrottledBeforeTouchingTheDatabase (0.03s)
    security_test.go:150: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
--- FAIL: TestExpiredAndRevokedKeysAreRejected (0.03s)
    security_test.go:179: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
--- FAIL: TestKeyRotationWithOverlap (0.03s)
    security_test.go:206: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
--- FAIL: TestLastUsedAtIsNotRewrittenOnEveryRequest (0.03s)
    security_test.go:231: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
--- FAIL: TestAuditLogRecordsControlPlaneActionsOnly (0.03s)
    security_test.go:270: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
--- FAIL: TestAuditLogIsAppendOnly (0.03s)
    security_test.go:294: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
--- FAIL: TestWorkersEndpointDoesNotLeakInfrastructure (0.03s)
    security_test.go:312: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
--- FAIL: TestTenantIsolationFuzz (0.03s)
    security_test.go:334: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
--- FAIL: TestTraceIDPropagatesFromRequestToStoredJob (0.03s)
    security_test.go:433: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
--- FAIL: TestBruteForceCannotBeBypassedWithSpoofedForwardingHeaders (0.03s)
    spoofed_ip_test.go:14: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`: [::1]:5432 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)
FAIL
FAIL	github.com/SGVARDHANVIKRANTHREDDY/queueforge/tests/security	5.333s
?   	github.com/SGVARDHANVIKRANTHREDDY/queueforge/workerkit	[no test files]
FAIL

``

#### evidence/govulncheck.txt

``text
=== Symbol Results ===

Vulnerability #1: GO-2026-6809
    Distrust compromised intermediate certificates in crypto/x509
  More info: https://pkg.go.dev/vuln/GO-2026-6809
  Standard library
    Found in: crypto/x509@go1.27
    Fixed in: crypto/x509@go1.27.3
    Example traces found:
      #1: cmd/admin/main.go:44:14: admin.main calls fmt.Fprintf, which eventually calls x509.Certificate.Verify

Vulnerability #2: GO-2026-6617
    HTTP/2 server crash due to HPACK encoder race in net/http
  More info: https://pkg.go.dev/vuln/GO-2026-6617
  Standard library
    Found in: net/http/internal/http2@go1.27
    Fixed in: net/http/internal/http2@go1.27.2
    Example traces found:
      #1: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.NetHTTPClientConn.Close
      #2: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.NetHTTPClientConn.Close
      #3: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.NetHTTPClientConn.RoundTrip
      #4: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.NetHTTPClientConn.RoundTrip
      #5: workerkit/workerkit.go:103:25: workerkit.Run calls http.Server.Shutdown, which calls http2.Server.GracefulShutdown
      #6: workerkit/workerkit.go:103:25: workerkit.Run calls http.Server.Shutdown, which calls http2.Server.GracefulShutdown
      #7: workerkit/workerkit.go:85:38: workerkit.Run calls http.Server.ListenAndServe, which eventually calls http2.Server.ServeConn
      #8: workerkit/workerkit.go:85:38: workerkit.Run calls http.Server.ListenAndServe, which eventually calls http2.Server.ServeConn
      #9: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.Transport.AddConn
      #10: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.Transport.AddConn
      #11: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.Transport.NewClientConn
      #12: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.Transport.NewClientConn
      #13: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.Transport.RoundTrip
      #14: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.Transport.RoundTrip
      #15: cmd/admin/main.go:44:14: admin.main calls fmt.Fprintf, which eventually calls http2.bufferedWriterTimeoutWriter.Write
      #16: cmd/admin/main.go:44:14: admin.main calls fmt.Fprintf, which eventually calls http2.bufferedWriterTimeoutWriter.Write
      #17: cmd/admin/main.go:44:14: admin.main calls fmt.Fprintf, which eventually calls http2.chunkWriter.Write
      #18: cmd/admin/main.go:44:14: admin.main calls fmt.Fprintf, which eventually calls http2.chunkWriter.Write
      #19: sdk/go/queueforge/client.go:261:19: queueforge.Client.do calls http2.gzipReader.Close
      #20: sdk/go/queueforge/client.go:261:19: queueforge.Client.do calls http2.gzipReader.Close
      #21: internal/postgres/tenant_repo.go:195:24: postgres.GenerateSecureToken calls rand.Read, which eventually calls http2.gzipReader.Read
      #22: internal/postgres/tenant_repo.go:195:24: postgres.GenerateSecureToken calls rand.Read, which eventually calls http2.gzipReader.Read
      #23: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.requestBody.Close
      #24: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.requestBody.Close
      #25: cmd/admin/main.go:102:16: admin.main calls fmt.Fprintln, which calls http2.responseWriter.Write
      #26: cmd/admin/main.go:102:16: admin.main calls fmt.Fprintln, which calls http2.responseWriter.Write
      #27: cmd/admin/main.go:102:16: admin.main calls fmt.Fprintln, which eventually calls http2.responseWriter.WriteHeader
      #28: cmd/admin/main.go:102:16: admin.main calls fmt.Fprintln, which eventually calls http2.responseWriter.WriteHeader
      #29: internal/postgres/job_repo.go:762:42: postgres.JobRepo.CompleteJobFailure calls multierr.multiError.Error, which eventually calls http2.responseWriter.WriteString
      #30: internal/postgres/job_repo.go:762:42: postgres.JobRepo.CompleteJobFailure calls multierr.multiError.Error, which eventually calls http2.responseWriter.WriteString
      #31: cmd/admin/main.go:44:14: admin.main calls fmt.Fprintf, which eventually calls http2.stickyErrWriter.Write
      #32: cmd/admin/main.go:44:14: admin.main calls fmt.Fprintf, which eventually calls http2.stickyErrWriter.Write
      #33: sdk/go/queueforge/client.go:261:19: queueforge.Client.do calls http2.transportResponseBody.Close
      #34: sdk/go/queueforge/client.go:261:19: queueforge.Client.do calls http2.transportResponseBody.Close
      #35: internal/postgres/tenant_repo.go:195:24: postgres.GenerateSecureToken calls rand.Read, which eventually calls http2.transportResponseBody.Read
      #36: internal/postgres/tenant_repo.go:195:24: postgres.GenerateSecureToken calls rand.Read, which eventually calls http2.transportResponseBody.Read

Vulnerability #3: GO-2026-6613
    HTTP/1 server connection desynchronization after 2xx CONNECT response in
    net/http
  More info: https://pkg.go.dev/vuln/GO-2026-6613
  Standard library
    Found in: net/http@go1.27
    Fixed in: net/http@go1.27.2
    Example traces found:
      #1: internal/httpapi/api.go:21:2: httpapi.init calls middleware.init, which calls http.CanonicalHeaderKey
      #2: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do
      #3: workerkit/workerkit.go:41:2: workerkit.init calls prometheus.init, which eventually calls http.HandleFunc
      #4: cmd/admin/main.go:102:16: admin.main calls fmt.Fprintln, which eventually calls http.Header.Add
      #5: cmd/admin/main.go:102:16: admin.main calls fmt.Fprintln, which eventually calls http.Header.Del
      #6: internal/auth/throttle.go:76:25: auth.ClientKey calls http.Header.Get
      #7: sdk/go/queueforge/client.go:246:17: queueforge.Client.do calls http.Header.Set
      #8: sdk/go/queueforge/client.go:242:41: queueforge.Client.do calls http.NewRequestWithContext
      #9: internal/observability/ops_server.go:21:12: observability.NewOpsHandler calls http.ServeMux.Handle
      #10: internal/observability/ops_server.go:22:16: observability.NewOpsHandler calls http.ServeMux.HandleFunc
      #11: workerkit/workerkit.go:85:38: workerkit.Run calls http.Server.ListenAndServe
      #12: workerkit/workerkit.go:103:25: workerkit.Run calls http.Server.Shutdown
      #13: sdk/go/queueforge/client.go:261:19: queueforge.Client.do calls http.body.Close
      #14: sdk/go/queueforge/client.go:260:24: queueforge.Client.do calls io.ReadAll, which calls http.body.Read
      #15: sdk/go/queueforge/client.go:261:19: queueforge.Client.do calls http.bodyEOFSignal.Close
      #16: internal/postgres/tenant_repo.go:195:24: postgres.GenerateSecureToken calls rand.Read, which eventually calls http.bodyEOFSignal.Read
      #17: sdk/go/queueforge/client.go:260:24: queueforge.Client.do calls io.ReadAll, which eventually calls http.bodyLocked.Read
      #18: internal/postgres/job_repo.go:762:42: postgres.JobRepo.CompleteJobFailure calls multierr.multiError.Error, which eventually calls http.bufioFlushWriter.Write
      #19: sdk/go/queueforge/client.go:261:19: queueforge.Client.do calls http.cancelTimerBody.Close
      #20: sdk/go/queueforge/client.go:260:24: queueforge.Client.do calls io.ReadAll, which eventually calls http.cancelTimerBody.Read
      #21: cmd/admin/main.go:44:14: admin.main calls fmt.Fprintf, which eventually calls http.checkConnErrorWriter.Write
      #22: cmd/admin/main.go:44:14: admin.main calls fmt.Fprintf, which eventually calls http.chunkWriter.Write
      #23: sdk/go/queueforge/client.go:260:24: queueforge.Client.do calls io.ReadAll, which eventually calls http.connReader.Read
      #24: sdk/go/queueforge/client.go:261:19: queueforge.Client.do calls http2.transportResponseBody.Close, which eventually calls http.expectContinueReader.Close
      #25: sdk/go/queueforge/client.go:260:24: queueforge.Client.do calls io.ReadAll, which calls http.expectContinueReader.Read
      #26: sdk/go/queueforge/client.go:261:19: queueforge.Client.do calls http.gzipReader.Close
      #27: sdk/go/queueforge/client.go:260:24: queueforge.Client.do calls io.ReadAll, which eventually calls http.gzipReader.Read
      #28: internal/postgres/tenant_repo.go:195:24: postgres.GenerateSecureToken calls rand.Read, which eventually calls http.loggingConn.Read
      #29: cmd/admin/main.go:44:14: admin.main calls fmt.Fprintf, which calls http.loggingConn.Write
      #30: sdk/go/queueforge/client.go:261:19: queueforge.Client.do calls http2.transportResponseBody.Close, which eventually calls http.maxBytesReader.Close
      #31: sdk/go/queueforge/client.go:260:24: queueforge.Client.do calls io.ReadAll, which calls http.maxBytesReader.Read
      #32: internal/worker/completer.go:83:15: worker.batchCompleter.Stop calls sync.Once.Do, which eventually calls http.onceCloseListener.Close
      #33: sdk/go/queueforge/client.go:260:24: queueforge.Client.do calls io.ReadAll, which eventually calls http.persistConn.Read
      #34: internal/postgres/schedule_repo.go:297:15: postgres.ScheduleRepo.ProcessDueSchedules calls pgx.baseRows.Next, which eventually calls http.persistConnWriter.ReadFrom
      #35: cmd/admin/main.go:44:14: admin.main calls fmt.Fprintf, which eventually calls http.persistConnWriter.Write
      #36: sdk/go/queueforge/client.go:261:19: queueforge.Client.do calls http2.transportResponseBody.Close, which eventually calls http.readTrackingBody.Close
      #37: sdk/go/queueforge/client.go:260:24: queueforge.Client.do calls io.ReadAll, which calls http.readTrackingBody.Read
      #38: sdk/go/queueforge/client.go:260:24: queueforge.Client.do calls io.ReadAll, which eventually calls http.readWriteCloserBody.Read
      #39: internal/postgres/schedule_repo.go:297:15: postgres.ScheduleRepo.ProcessDueSchedules calls pgx.baseRows.Next, which eventually calls http.response.ReadFrom
      #40: cmd/admin/main.go:102:16: admin.main calls fmt.Fprintln, which calls http.response.Write
      #41: cmd/admin/main.go:102:16: admin.main calls fmt.Fprintln, which eventually calls http.response.WriteHeader
      #42: internal/postgres/job_repo.go:762:42: postgres.JobRepo.CompleteJobFailure calls multierr.multiError.Error, which eventually calls http.response.WriteString
      #43: cmd/admin/main.go:102:16: admin.main calls fmt.Fprintln, which calls http.timeoutWriter.Write
      #44: cmd/admin/main.go:102:16: admin.main calls fmt.Fprintln, which eventually calls http.timeoutWriter.WriteHeader
      #45: internal/postgres/job_repo.go:762:42: postgres.JobRepo.CompleteJobFailure calls http.transportReadFromServerError.Error

Vulnerability #4: GO-2026-6612
    Double flow control refund on HTTP/2 server streams in net/http
  More info: https://pkg.go.dev/vuln/GO-2026-6612
  Standard library
    Found in: net/http/internal/http2@go1.27
    Fixed in: net/http/internal/http2@go1.27.2
    Example traces found:
      #1: workerkit/workerkit.go:85:38: workerkit.Run calls http.Server.ListenAndServe, which eventually calls http2.Server.ServeConn
      #2: workerkit/workerkit.go:85:38: workerkit.Run calls http.Server.ListenAndServe, which eventually calls http2.Server.ServeConn
      #3: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.requestBody.Close
      #4: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.requestBody.Close
      #5: sdk/go/queueforge/client.go:260:24: queueforge.Client.do calls io.ReadAll, which calls http2.requestBody.Read
      #6: sdk/go/queueforge/client.go:260:24: queueforge.Client.do calls io.ReadAll, which calls http2.requestBody.Read

Vulnerability #5: GO-2026-6611
    Excessive CPU consumption from repeated initial window changes in net/http
  More info: https://pkg.go.dev/vuln/GO-2026-6611
  Standard library
    Found in: net/http/internal/http2@go1.27
    Fixed in: net/http/internal/http2@go1.27.2
    Example traces found:
      #1: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.NetHTTPClientConn.Close
      #2: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.NetHTTPClientConn.Close
      #3: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.NetHTTPClientConn.RoundTrip
      #4: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.NetHTTPClientConn.RoundTrip
      #5: workerkit/workerkit.go:103:25: workerkit.Run calls http.Server.Shutdown, which calls http2.Server.GracefulShutdown
      #6: workerkit/workerkit.go:103:25: workerkit.Run calls http.Server.Shutdown, which calls http2.Server.GracefulShutdown
      #7: workerkit/workerkit.go:85:38: workerkit.Run calls http.Server.ListenAndServe, which eventually calls http2.Server.ServeConn
      #8: workerkit/workerkit.go:85:38: workerkit.Run calls http.Server.ListenAndServe, which eventually calls http2.Server.ServeConn
      #9: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.Transport.AddConn
      #10: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.Transport.AddConn
      #11: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.Transport.NewClientConn
      #12: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.Transport.NewClientConn
      #13: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.Transport.RoundTrip
      #14: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.Transport.RoundTrip
      #15: cmd/admin/main.go:44:14: admin.main calls fmt.Fprintf, which eventually calls http2.bufferedWriterTimeoutWriter.Write
      #16: cmd/admin/main.go:44:14: admin.main calls fmt.Fprintf, which eventually calls http2.bufferedWriterTimeoutWriter.Write
      #17: cmd/admin/main.go:44:14: admin.main calls fmt.Fprintf, which eventually calls http2.chunkWriter.Write
      #18: cmd/admin/main.go:44:14: admin.main calls fmt.Fprintf, which eventually calls http2.chunkWriter.Write
      #19: sdk/go/queueforge/client.go:261:19: queueforge.Client.do calls http2.gzipReader.Close
      #20: sdk/go/queueforge/client.go:261:19: queueforge.Client.do calls http2.gzipReader.Close
      #21: internal/postgres/tenant_repo.go:195:24: postgres.GenerateSecureToken calls rand.Read, which eventually calls http2.gzipReader.Read
      #22: internal/postgres/tenant_repo.go:195:24: postgres.GenerateSecureToken calls rand.Read, which eventually calls http2.gzipReader.Read
      #23: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.requestBody.Close
      #24: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.requestBody.Close
      #25: cmd/admin/main.go:102:16: admin.main calls fmt.Fprintln, which calls http2.responseWriter.Write
      #26: cmd/admin/main.go:102:16: admin.main calls fmt.Fprintln, which calls http2.responseWriter.Write
      #27: cmd/admin/main.go:102:16: admin.main calls fmt.Fprintln, which eventually calls http2.responseWriter.WriteHeader
      #28: cmd/admin/main.go:102:16: admin.main calls fmt.Fprintln, which eventually calls http2.responseWriter.WriteHeader
      #29: internal/postgres/job_repo.go:762:42: postgres.JobRepo.CompleteJobFailure calls multierr.multiError.Error, which eventually calls http2.responseWriter.WriteString
      #30: internal/postgres/job_repo.go:762:42: postgres.JobRepo.CompleteJobFailure calls multierr.multiError.Error, which eventually calls http2.responseWriter.WriteString
      #31: cmd/admin/main.go:44:14: admin.main calls fmt.Fprintf, which eventually calls http2.stickyErrWriter.Write
      #32: cmd/admin/main.go:44:14: admin.main calls fmt.Fprintf, which eventually calls http2.stickyErrWriter.Write
      #33: sdk/go/queueforge/client.go:261:19: queueforge.Client.do calls http2.transportResponseBody.Close
      #34: sdk/go/queueforge/client.go:261:19: queueforge.Client.do calls http2.transportResponseBody.Close
      #35: internal/postgres/tenant_repo.go:195:24: postgres.GenerateSecureToken calls rand.Read, which eventually calls http2.transportResponseBody.Read
      #36: internal/postgres/tenant_repo.go:195:24: postgres.GenerateSecureToken calls rand.Read, which eventually calls http2.transportResponseBody.Read

Vulnerability #6: GO-2026-6610
    HTTP/2 transport accepts malformed framing-related headers in net/http
  More info: https://pkg.go.dev/vuln/GO-2026-6610
  Standard library
    Found in: net/http/internal/http2@go1.27
    Fixed in: net/http/internal/http2@go1.27.2
    Example traces found:
      #1: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.Transport.AddConn
      #2: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.Transport.AddConn
      #3: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.Transport.NewClientConn
      #4: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.Transport.NewClientConn
      #5: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.Transport.RoundTrip
      #6: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.Transport.RoundTrip

Vulnerability #7: GO-2026-6609
    Lack of limit on size of parsed Range headers in net/http
  More info: https://pkg.go.dev/vuln/GO-2026-6609
  Standard library
    Found in: net/http@go1.27
    Fixed in: net/http@go1.27.2
    Example traces found:
      #1: workerkit/workerkit.go:85:38: workerkit.Run calls http.Server.ListenAndServe, which eventually calls http.fileHandler.ServeHTTP

Vulnerability #8: GO-2026-6608
    Memory limit bypass when parsing MIME headers in net/textproto,
    mime/multipart
  More info: https://pkg.go.dev/vuln/GO-2026-6608
  Standard library
    Found in: net/textproto@go1.27
    Fixed in: net/textproto@go1.27.2
    Example traces found:
      #1: workerkit/workerkit.go:85:38: workerkit.Run calls http.Server.ListenAndServe, which eventually calls multipart.Reader.ReadForm
      #2: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls textproto.Reader.ReadMIMEHeader

Vulnerability #9: GO-2026-6607
    Reject malformed ECH outer extension references in crypto/tls
  More info: https://pkg.go.dev/vuln/GO-2026-6607
  Standard library
    Found in: crypto/tls@go1.27
    Fixed in: crypto/tls@go1.27.2
    Example traces found:
      #1: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls tls.Conn.HandshakeContext
      #2: internal/postgres/tenant_repo.go:195:24: postgres.GenerateSecureToken calls rand.Read, which eventually calls tls.Conn.Read
      #3: cmd/admin/main.go:44:14: admin.main calls fmt.Fprintf, which calls tls.Conn.Write
      #4: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls tls.Dialer.DialContext

Vulnerability #10: GO-2026-6605
    HTTP/1 client connection desynchronization after CONNECT rejection in
    net/http
  More info: https://pkg.go.dev/vuln/GO-2026-6605
  Standard library
    Found in: net/http@go1.27
    Fixed in: net/http@go1.27.2
    Example traces found:
      #1: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do

Vulnerability #11: GO-2026-6603
    HTTP/2 server memory exhaustion due to Trailer headers in net/http
  More info: https://pkg.go.dev/vuln/GO-2026-6603
  Standard library
    Found in: net/http/internal/http2@go1.27
    Fixed in: net/http/internal/http2@go1.27.2
    Example traces found:
      #1: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.NetHTTPClientConn.Close
      #2: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.NetHTTPClientConn.Close
      #3: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.NetHTTPClientConn.RoundTrip
      #4: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.NetHTTPClientConn.RoundTrip
      #5: workerkit/workerkit.go:103:25: workerkit.Run calls http.Server.Shutdown, which calls http2.Server.GracefulShutdown
      #6: workerkit/workerkit.go:103:25: workerkit.Run calls http.Server.Shutdown, which calls http2.Server.GracefulShutdown
      #7: workerkit/workerkit.go:85:38: workerkit.Run calls http.Server.ListenAndServe, which eventually calls http2.Server.ServeConn
      #8: workerkit/workerkit.go:85:38: workerkit.Run calls http.Server.ListenAndServe, which eventually calls http2.Server.ServeConn
      #9: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.Transport.AddConn
      #10: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.Transport.AddConn
      #11: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.Transport.NewClientConn
      #12: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.Transport.NewClientConn
      #13: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.Transport.RoundTrip
      #14: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.Transport.RoundTrip
      #15: cmd/admin/main.go:44:14: admin.main calls fmt.Fprintf, which eventually calls http2.bufferedWriterTimeoutWriter.Write
      #16: cmd/admin/main.go:44:14: admin.main calls fmt.Fprintf, which eventually calls http2.bufferedWriterTimeoutWriter.Write
      #17: cmd/admin/main.go:44:14: admin.main calls fmt.Fprintf, which eventually calls http2.chunkWriter.Write
      #18: cmd/admin/main.go:44:14: admin.main calls fmt.Fprintf, which eventually calls http2.chunkWriter.Write
      #19: sdk/go/queueforge/client.go:261:19: queueforge.Client.do calls http2.gzipReader.Close
      #20: sdk/go/queueforge/client.go:261:19: queueforge.Client.do calls http2.gzipReader.Close
      #21: internal/postgres/tenant_repo.go:195:24: postgres.GenerateSecureToken calls rand.Read, which eventually calls http2.gzipReader.Read
      #22: internal/postgres/tenant_repo.go:195:24: postgres.GenerateSecureToken calls rand.Read, which eventually calls http2.gzipReader.Read
      #23: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.requestBody.Close
      #24: sdk/go/queueforge/client.go:252:25: queueforge.Client.do calls http.Client.Do, which eventually calls http2.requestBody.Close
      #25: cmd/admin/main.go:102:16: admin.main calls fmt.Fprintln, which calls http2.responseWriter.Write
      #26: cmd/admin/main.go:102:16: admin.main calls fmt.Fprintln, which calls http2.responseWriter.Write
      #27: cmd/admin/main.go:102:16: admin.main calls fmt.Fprintln, which eventually calls http2.responseWriter.WriteHeader
      #28: cmd/admin/main.go:102:16: admin.main calls fmt.Fprintln, which eventually calls http2.responseWriter.WriteHeader
      #29: internal/postgres/job_repo.go:762:42: postgres.JobRepo.CompleteJobFailure calls multierr.multiError.Error, which eventually calls http2.responseWriter.WriteString
      #30: internal/postgres/job_repo.go:762:42: postgres.JobRepo.CompleteJobFailure calls multierr.multiError.Error, which eventually calls http2.responseWriter.WriteString
      #31: cmd/admin/main.go:44:14: admin.main calls fmt.Fprintf, which eventually calls http2.stickyErrWriter.Write
      #32: cmd/admin/main.go:44:14: admin.main calls fmt.Fprintf, which eventually calls http2.stickyErrWriter.Write
      #33: sdk/go/queueforge/client.go:261:19: queueforge.Client.do calls http2.transportResponseBody.Close
      #34: sdk/go/queueforge/client.go:261:19: queueforge.Client.do calls http2.transportResponseBody.Close
      #35: internal/postgres/tenant_repo.go:195:24: postgres.GenerateSecureToken calls rand.Read, which eventually calls http2.transportResponseBody.Read
      #36: internal/postgres/tenant_repo.go:195:24: postgres.GenerateSecureToken calls rand.Read, which eventually calls http2.transportResponseBody.Read

Your code is affected by 11 vulnerabilities from the Go standard library.
This scan also found 1 vulnerability in packages you import and 2
vulnerabilities in modules you require, but your code doesn't appear to call
these vulnerabilities.
Use '-show verbose' for more details.

``

#### evidence/least-privilege-smoke.txt

``text
bash : ./deploy/smoke.sh: line 6: BASE_URL: set BASE_URL
At line:2 char:399
+ ... BASE_URL='http://localhost:8080'; bash ./deploy/smoke.sh 2>&1 | Tee-O ...
+                                       ~~~~~~~~~~~~~~~~~~~~~~~~~~~
    + CategoryInfo          : NotSpecified: (./deploy/smoke....L: set BASE_URL:String) [], RemoteException
    + FullyQualifiedErrorId : NativeCommandError
 

``

#### evidence/least-privilege-sql.txt

``text
docker : Error response from daemon: container 4a645853916800572439ed1b970e0e23acfe8a38a6e9b936d4ab210d6e4f50b6 is not 
running
At line:2 char:46
+ ... ilege.sql | docker exec -i qf-pg psql -U postgres -d queueforge -v ap ...
+                 ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
    + CategoryInfo          : NotSpecified: (Error response ... is not running:String) [], RemoteException
    + FullyQualifiedErrorId : NativeCommandError
 

``

#### evidence/phase6-compose-ps.txt

``text
NAME      IMAGE     COMMAND   SERVICE   CREATED   STATUS    PORTS

``

#### evidence/phase6-create-key.txt

``text
docker : service "api" is not running
At line:2 char:288
+ ... tenant.txt; docker compose -f deploy/docker-compose.yml exec api /app ...
+                 ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
    + CategoryInfo          : NotSpecified: (service "api" is not running:String) [], RemoteException
    + FullyQualifiedErrorId : NativeCommandError
 

``

#### evidence/phase6-create-tenant.txt

``text
docker : service "api" is not running
At line:2 char:98
+ ... ose-ps.txt; docker compose -f deploy/docker-compose.yml exec api /app ...
+                 ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
    + CategoryInfo          : NotSpecified: (service "api" is not running:String) [], RemoteException
    + FullyQualifiedErrorId : NativeCommandError
 

``

#### evidence/phase7-worker-rebuild.txt

``text
docker :  Image deploy-worker Building 
At line:2 char:1
+ docker compose -f deploy/docker-compose.yml up -d --build worker 2>&1 ...
+ ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
    + CategoryInfo          : NotSpecified: ( Image deploy-worker Building :String) [], RemoteException
    + FullyQualifiedErrorId : NativeCommandError
 
#1 [internal] load local bake definitions
#1 reading from stdin 629B done
#1 DONE 0.0s

#2 [internal] load build definition from Dockerfile
#2 transferring dockerfile: 770B done
#2 DONE 0.0s

#3 [internal] load metadata for docker.io/library/alpine:3.20
#3 ...

#4 [internal] load metadata for docker.io/library/golang:1.27-alpine
#4 DONE 1.2s

#3 [internal] load metadata for docker.io/library/alpine:3.20
#3 DONE 1.2s

#5 [internal] load .dockerignore
#5 transferring context: 2B done
#5 DONE 0.0s

#6 [builder 1/9] FROM docker.io/library/golang:1.27-alpine@sha256:d530cf60723abf1ffae0cd16bb546b379ec0550d2f3815e5b16ed69dbc00fba4
#6 resolve docker.io/library/golang:1.27-alpine@sha256:d530cf60723abf1ffae0cd16bb546b379ec0550d2f3815e5b16ed69dbc00fba4 0.0s done
#6 DONE 0.0s

#7 [stage-1 1/8] FROM docker.io/library/alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc
#7 resolve docker.io/library/alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc 0.0s done
#7 DONE 0.0s

#8 [internal] load build context
#8 transferring context: 124.02kB 0.1s done
#8 DONE 0.1s

#9 [builder 4/9] COPY go.mod go.sum ./
#9 CACHED

#10 [builder 2/9] WORKDIR /src
#10 CACHED

#11 [builder 3/9] RUN apk add --no-cache git
#11 CACHED

#12 [builder 5/9] RUN go mod download
#12 CACHED

#13 [builder 6/9] COPY . .
#13 DONE 0.5s

#14 [builder 7/9] RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /bin/api ./cmd/api
#14 DONE 8.6s

#15 [builder 8/9] RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /bin/worker ./cmd/worker
#15 DONE 0.8s

#16 [builder 9/9] RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /bin/admin ./cmd/admin
#16 DONE 0.8s

#17 [stage-1 7/8] COPY web /app/web
#17 CACHED

#18 [stage-1 5/8] COPY --from=builder /bin/worker /app/worker
#18 CACHED

#19 [stage-1 2/8] WORKDIR /app
#19 CACHED

#20 [stage-1 3/8] RUN apk add --no-cache ca-certificates tzdata
#20 CACHED

#21 [stage-1 4/8] COPY --from=builder /bin/api /app/api
#21 CACHED

#22 [stage-1 6/8] COPY --from=builder /bin/admin /app/admin
#22 CACHED

#23 [stage-1 8/8] RUN adduser -D -H -u 10001 qf
#23 CACHED

#24 exporting to image
#24 exporting layers done
#24 exporting manifest sha256:b8266b0704acb0a50592ab8a4781d02077f495c9a1f633641fc87cc169358769 done
#24 exporting config sha256:3c918976d919bd9024da78fb8a3a5656502691379fbcc41ba8db87ed99b65d2b done
#24 exporting attestation manifest sha256:a43ed1b0dd96d45f4916f28ff4321e82c0d1b0ba0052b7f550842fb579837bee 0.0s done
#24 exporting manifest list sha256:1b1d679b49336c308b8690ce4a949e62358b2f84c8fc9e1beda50751fb4a7c7a 0.0s done
#24 naming to docker.io/library/deploy-worker:latest done
#24 unpacking to docker.io/library/deploy-worker:latest done
#24 DONE 0.1s

#25 resolving provenance for metadata file
#25 DONE 0.0s
 Image deploy-worker Built 
 Container deploy-worker-1 Recreate 
 Container deploy-worker-2 Recreate 
 Container deploy-worker-2 Recreated 
 Container deploy-worker-1 Recreated 
 Container queueforge-db Starting 
Error response from daemon: ports are not available: exposing port TCP 127.0.0.1:5432 -> 127.0.0.1:0: listen tcp4 
127.0.0.1:5432: bind: An attempt was made to access a socket in a way forbidden by its access permissions.

``

#### evidence/property.txt

``text
go : go: -race requires cgo; enable cgo by setting CGO_ENABLED=1
At line:2 char:1
+ go test -race -count=1 -v ./tests/property/ 2>&1 | Tee-Object evidenc ...
+ ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
    + CategoryInfo          : NotSpecified: (go: -race requi...g CGO_ENABLED=1:String) [], RemoteException
    + FullyQualifiedErrorId : NativeCommandError
 

``

#### evidence/python-tests.txt

``text
python3 : Python was not found; run without arguments to install from the Microsoft Store, or disable this shortcut 
from Settings > Apps > Advanced app settings > App execution aliases.
At line:2 char:1
+ python3 -m unittest discover -s sdk/python/tests -v 2>&1 | Tee-Object ...
+ ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~
    + CategoryInfo          : NotSpecified: (Python was not ...cution aliases.:String) [], RemoteException
    + FullyQualifiedErrorId : NativeCommandError
 

``

#### evidence/python-tests-corrected.txt

``text
test_batch_posts_to_batch_endpoint_with_key (test_client.TestIdempotencyDefaults.test_batch_posts_to_batch_endpoint_with_key) ... ok
test_batch_size_is_validated_client_side (test_client.TestIdempotencyDefaults.test_batch_size_is_validated_client_side) ... ok
test_retry_reuses_an_automatically_generated_key (test_client.TestIdempotencyDefaults.test_retry_reuses_an_automatically_generated_key) ... ok
test_two_separate_calls_use_different_keys (test_client.TestIdempotencyDefaults.test_two_separate_calls_use_different_keys) ... ok
test_idempotency_conflict_error (test_client.TestQueueForgeClient.test_idempotency_conflict_error) ... ok
test_idempotent_replay (test_client.TestQueueForgeClient.test_idempotent_replay) ... ok
test_rate_limit_error (test_client.TestQueueForgeClient.test_rate_limit_error) ... ok
test_submit_job_success (test_client.TestQueueForgeClient.test_submit_job_success) ... ok

----------------------------------------------------------------------
Ran 8 tests in 4.017s

OK

``

#### evidence/soak.txt

``text
=== RUN   TestSoak
    soak_test.go:42: connect to admin database: failed to ping database: failed to connect to `user=postgres database=queueforge`:
        	[::1]:5435 (localhost): dial error: dial tcp [::1]:5435: connectex: No connection could be made because the target machine actively refused it.
        	127.0.0.1:5435 (localhost): dial error: dial tcp 127.0.0.1:5435: connectex: No connection could be made because the target machine actively refused it.
--- FAIL: TestSoak (0.02s)
FAIL
FAIL	github.com/SGVARDHANVIKRANTHREDDY/queueforge/tests/load	2.598s
FAIL

``

Additional exact command results:

- `go test -race -count=1 -p 1 -timeout 30m ./...`: `go: -race requires cgo; enable cgo by setting CGO_ENABLED=1`.
- All five focused race commands: the same CGO-disabled error.
- `python3 -m unittest discover -s sdk/python/tests -v`: Python was not found.
- Chaos compilation additionally reports undefined `syscall.Kill` on Windows.

### Phase 6 quickstart

`docker compose -f deploy/docker-compose.yml up -d --build` built both images successfully, then failed:

    Error response from daemon: ports are not available: exposing port TCP 127.0.0.1:5432 ... bind: An attempt was made to access a socket in a way forbidden by its access permissions.

`docker compose ... ps` showed the services in Created/not-running state. The admin commands failed with `service "api" is not running`. The curl and Python SDK commands could not complete because the API was not running. The configured compose stack uses host port 5432; PID 6412 was identified as `postgres` and listening on that port.

### Phase 7

The worker rebuild/image build succeeded, but starting the worker failed at the same compose database port conflict. The spoofed-IP curl loop and generic-error curl checks were started but stopped after they hung against the unavailable API; no valid HTTP result was produced. Temporary panic and redaction handlers were not added because the API stack could not be started and the checklist explicitly requires removing them afterward.

### Phase 8 and 9

Benchmark, idle, soak, and chaos commands were run. The load tests attempted localhost:5435 and failed with connection refused. Chaos tests failed to compile on Windows due to `Setpgid` and `syscall.Kill`. No benchmark medians or chaos pass results exist.

### Phase 10 security

`go install golang.org/x/vuln/cmd/govulncheck@latest` completed, and the installed scanner reported vulnerabilities including GO-2026-6809 and GO-2026-6617; full output is in [govulncheck.txt](./govulncheck.txt). `gitleaks` and `trivy` were not found. The Docker image `queueforge:local` built successfully. The least-privilege SQL command could not run because `qf-pg` was stopped and could not be restarted while host port 5432 was occupied. `deploy/smoke.sh` failed because `BASE_URL` was initially unset; after setting it, the API was still unavailable.

### Phase 11 and 12

Paper exercises, interview explanations, rebuilding historical bugs, dogfooding, and real external integrations require human decisions, a separate project, private credentials, or a live deployment. They were not executable commands in this workspace and were not fabricated.

### Phase 13 publish

`git remote add origin git@github.com:SGVARDHANVIKRANTHREDDY/queueforge.git` failed because origin already exists. `git push -u origin main` failed:

    git@github.com: Permission denied (publickey).
    fatal: Could not read from remote repository.

`git tag v0.1.0` failed because the tag already exists locally. `git push origin v0.1.0` failed with the same SSH authentication error.

### Phase 14

No VPS, domain, public URL, external monitor, Kubernetes cluster, or real tenants were provided. Public deployment, backups, restore drill, worker kill, canary, and four-week usage could not be executed.

## Files and commits

- Evidence commit: `6831c41` (`test evidence`)
- Existing local tag: `v0.1.0`
- Existing modified/unrelated file was not reverted.
- Captured command outputs are in the [evidence folder](.).

## Next actions required for a green rerun

1. Free host port 5432 or change both compose mapping and test URL to an unused port.
2. Run in WSL2/Linux with CGO enabled for race tests and Unix chaos tests.
3. Install Python 3.10+, psql, jq, gitleaks, and trivy.
4. Review and update stale `QF_DATABASE_TLS_MODE` documentation references.
5. Configure GitHub SSH credentials (or an authenticated HTTPS remote), then push `main` and `v0.1.0`.
6. Complete the human-only phases against disposable/test infrastructure.

## Rerun from checklist start: 2026-10-22 13:02-13:35

This rerun was performed after creating the empty GitHub repository
`github.com/SGVARDHANVIKRANTHREDDY/queueforge`.

### Rerun results

- `git --version`, `go version`, Docker CLI, Docker `hello-world`, and `go build ./...` passed.
- The exact `python3` command still failed because Windows does not expose a `python3` executable. The installed `python` interpreter passed the 8 Python SDK tests in the earlier run.
- `psql`, `jq`, `gitleaks`, and `trivy` remain unavailable.
- The exact module-path script correctly refused to rerun because the module is already renamed:

      go.mod module is not 'queueforge' (already renamed?)

- `go mod tidy` and the final `gofmt -l .` check passed.
- `go vet ./...` still fails on Windows-only chaos-test APIs:

      unknown field Setpgid in struct literal of type syscall.SysProcAttr

- All exact `-race` commands still stop before test execution because this Go installation has `CGO_ENABLED=0`:

      go: -race requires cgo; enable cgo by setting CGO_ENABLED=1

- Docker image builds passed. The development Compose stack still cannot start because an unrelated PostgreSQL process owns host port 5432. The process was not terminated.
- The exact benchmark commands completed successfully. The latest baseline drain times were 2.65s, 2.45s, and 2.40s; batch-completion drain times were 2.75s, 2.60s, and 2.55s. Batch completion remained slower in this environment. Latest idle latency was p50 16.2ms, p95 21.8ms, p99 31.4ms.
- Chaos commands still fail to compile on Windows because of `Setpgid` and `syscall.Kill`.
- `govulncheck` again reported the Go 1.27 standard-library advisories, including GO-2026-6809 and GO-2026-6617. Full output remains in [govulncheck.txt](./govulncheck.txt).
- The exact gitleaks and Trivy commands failed because those tools are not installed.
- The live API quickstart, tenant creation, API key creation, and smoke test could not run because the Compose API was not running.
- Backup and restore could not run locally because the production backup path `/var/backups` is not available on Windows.
- The Kubernetes dry run could not contact a Kubernetes API server.

### GitHub publishing results

The SSH remote initially failed with:

    git@github.com: Permission denied (publickey).

The remote was changed to HTTPS and publishing then succeeded:

- `main` pushed to `origin/main`.
- `v0.1.0` pushed to `origin` and is visible at `refs/tags/v0.1.0`.
- A fresh temporary Go module successfully ran:

      go get github.com/SGVARDHANVIKRANTHREDDY/queueforge/workerkit@v0.1.0

- GitHub Actions run 1 was created and observed in `queued` state.
- `docker pull ghcr.io/sgvardhanvikranthreddy/queueforge:v0.1.0` was denied because the release workflow has not yet published the image.

The second-run command outputs remain in the existing [evidence folder](.).
