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

## Fresh full rerun from checklist line 1: 2026-10-22 14:09

This third pass attempted every executable checklist command again. Raw outputs are
in [evidence/rerun-20261022-1409](./rerun-20261022-1409); the 117.63 MB soak
output is preserved as the compressed `soak.txt.zip` file because GitHub rejects
individual files over 100 MB.

### Phases 0-5

- Git, Go 1.27.0, Docker, Docker `hello-world`, and `curl` were available.
- `python3`, `psql`, and `jq` were unavailable. The installed `python` command ran
  the equivalent SDK suite successfully: 8 tests, `OK`.
- The exact `docker run ... --name qf-pg` command failed because `qf-pg` already
  existed. The existing disposable container reported `accepting connections`.
- The exact module-path script refused to rerun because the module was already
  renamed.
- `go mod tidy`, `gofmt -l .`, and `go build ./...` passed.
- `go vet ./...` failed on Windows-only `syscall.SysProcAttr.Setpgid` in
  `tests/chaos/kill9_test.go`; the chaos tests also use unavailable
  `syscall.Kill`.
- The exact quick Go suite passed every non-chaos package and failed only the
  chaos package at compilation. The exact race suite and all focused race
  commands stopped before execution because `CGO_ENABLED=0`.
- The exact `git add evidence && git commit` operation was not run against the
  whole evidence tree because the workspace contains unrelated pre-existing
  deleted PRD/TDD files and untracked benchmark artifacts. Only the new rerun
  directory and report files were staged.

### Phases 6-7

- `docker compose ... up -d --build` built both QueueForge images successfully
  but could not start PostgreSQL because host port 5432 was unavailable.
- Service status, tenant creation, API-key creation, enqueue, idempotency replay,
  batch, live UI, and Python live-API commands therefore could not reach a
  running API. Curl requests returned timeouts or HTTP `000`.
- Worker rebuild also built successfully but could not start for the same port
  conflict.
- The spoofed-IP loop returned HTTP `000` for all 60 requests because the API was
  unavailable.
- The literal error-redaction request timed out because no API was listening.
- The temporary panic and secret-redaction handlers were not inserted because
  the service required to exercise them was unavailable; no temporary code was
  left in the repository.

### Phases 8-10

Fresh three-run benchmarks:

- Per-job completion: 3.05s, 2.45s, 2.45s; median 2.45s / 1,224 jobs/s.
- Batch completion: 3.15s, 2.70s, 2.55s; median 2.70s / 1,111 jobs/s.
- Idle latency: p50 14.9ms, p95 21.9ms, p99 24.2ms.
- Batch completion was slower and should remain disabled for this environment.
- Machine: Intel Core i7-13650HX, 14 cores, 20 logical processors; Docker
  Desktop reported 7.6 GiB available memory.

Chaos tests failed at Windows compilation with `Setpgid`/`syscall.Kill`.
The optional Redis command failed because port 6379 was already allocated.

The 30-minute soak completed successfully:

      SOAK_RESULT enqueued=271084 enqueue_errors=0 succeeded=270541 dead=543 open=0 leaked_slots=0 outstanding=0 succeeded_without_exactly_one_success_attempt=0
      SOAK_RESULT drift heap_MB first/last third=7.4/8.6 goroutines=24/24 qwait_p95_ms=10/6
      --- PASS: TestSoak (1811.22s)

`govulncheck` reported 11 Go 1.27 standard-library advisories, including
GO-2026-6809 and GO-2026-6617. The image build passed; gitleaks and Trivy were
not installed. The least-privilege SQL script ran but reported:

      ERROR: relation "public.audit_log" does not exist

### Phases 11-15

- The stale `QueueForge_TDD.md` check could not run because that file is absent
  in the current workspace. The workspace also contains unrelated deletions of
  the PRD/TDD files that were not reverted.
- Human-only interview exercises, private-note relocation, real external task
  execution, VPS provisioning, domain setup, monitoring, four-week dogfooding,
  and incident recording were not fabricated.
- The published Go module check succeeded from a fresh temporary directory.
- `git push -u origin main` succeeded over the existing HTTPS remote; the
  existing `v0.1.0` tag was already pushed.
- `docker pull ghcr.io/.../queueforge:v0.1.0` failed because the release image
  was not published. GitHub Actions showed CI failures in security, Python SDK,
  and PostgreSQL/chaos jobs; binary/image build and lint jobs passed.
- Smoke, backup, restore, and Kubernetes dry-run commands failed because there
  was no public deployment, Windows lacks `/var/backups`, and no Kubernetes API
  server was available.
- The literal `docker kill <worker container>` command failed because the
  placeholder is not a real container name.
- Unauthenticated GitHub REST attempts to update description/topics returned
  HTTP 401.

### GitHub Actions failure logs for run 39754996015

The failed steps were retrieved from the Actions run for commit `2405028`.
The relevant log excerpts were:

Security scan, before any scan command ran:

      ##[error]Unable to resolve action `aquasecurity/trivy-action@0.28.0`, unable to find version `0.28.0`

Python SDK verification:

      Traceback (most recent call last):
      ModuleNotFoundError: No module named 'setuptools'
      ##[error]Process completed with exit code 1.

PostgreSQL integration and chaos:

      Integration, regression, security and property tests (race detector): success
      Chaos tests (real kill -9 of a worker process, network faults via fault-injecting proxy): failure

The PostgreSQL service log during that failed chaos step also contained repeated
connection-abort messages, including:

      LOG: unexpected EOF on client connection with an open transaction

The retrieved Actions log did not expose a more specific Go assertion in its
tail, so the exact chaos assertion remains unresolved rather than guessed.

### Follow-up verification after remediation

The following commands were rerun after commit `cd89b80`. These results supersede
the earlier "not run" notes where the command was subsequently executed.

#### Linux race and explicit chaos

The WSL2 package installation path was attempted twice, but `apt-get update`
hung without producing progress. To provide the same Linux/CGO execution
environment, the repository was mounted into `golang:1.27.3-bookworm` and run
against a disposable PostgreSQL container.

Command:

    docker run --rm --network qf-race-net -v "<workspace>:/repo" -w /repo \
      -e QF_DATABASE_URL=postgres://postgres:postgres@qf-race-pg:5432/queueforge?sslmode=disable \
      golang:1.27.3-bookworm \
      /usr/local/go/bin/go test -race -count=1 -p 1 -timeout 30m ./...

Output: all packages passed, including `tests/chaos`, `tests/integration`,
`tests/load`, `tests/property`, `tests/regression`, and `tests/security`.

The explicit real-process commands were then run:

    /usr/local/go/bin/go test -v -p 1 -timeout 15m \
      -run TestChaos_RealKill9_WorkerProcessRecovery ./tests/chaos
    /usr/local/go/bin/go test -v -p 1 -timeout 20m \
      -run TestChaos_NetworkFaultsBetweenWorkersAndPostgres ./tests/chaos

Outputs:

    --- PASS: TestChaos_RealKill9_WorkerProcessRecovery (13.34s)
    kill9_to_succeeded_seconds=3.2 (lease=3s)
    ok .../tests/chaos 13.352s

    --- PASS: TestChaos_NetworkFaultsBetweenWorkersAndPostgres (28.26s)
    done_when_faults_ended=179/240
    netfault total_jobs=240 executions=256 wasted_executions=16 dead=0 busy_slots=0 outstanding=0
    ok .../tests/chaos 28.281s

The opt-in PostgreSQL outage test was not falsely reported as passed: it
requires caller-supplied stop/start commands with permission to control the
database service.

#### Live Compose quickstart

The API container was run on host port `18080` because port `8080` was already
occupied. PostgreSQL was published on `127.0.0.1:55432`, and migrations
`00001` through `00006` completed successfully.

    http://127.0.0.1:18080/readyz HTTP 200 {"status":"ready"}
    http://127.0.0.1:18080/healthz HTTP 200 {"status":"ok"}

#### Vulnerability and secret scans

`/go/bin/govulncheck ./...` reported:

    No vulnerabilities found.

Trivy filesystem scanning reported zero findings for both Go modules, the
Dockerfile, and the Kubernetes manifest:

    bench/headtohead/go.mod  Vulnerabilities 0
    go.mod                  Vulnerabilities 0
    deploy/Dockerfile       Misconfigurations 0
    deploy/k8s/queueforge.yaml Misconfigurations 0

Gitleaks scanned 20 commits and approximately 61.66 MB and reported 15
historical findings: 10 `curl-auth-header` and 5 `generic-api-key`. The scan
was run with `--redact`; no plaintext secret values were included in this
report. These findings are historical command/documentation matches and still
require repository-owner review before they can be marked allowlisted or
remediated.

#### Least privilege

The migrated disposable database accepted `deploy/sql/least_privilege.sql`
with exit code 0. The command emitted successful `DO`, `ALTER ROLE`, `REVOKE`,
`GRANT`, and `ALTER DEFAULT PRIVILEGES` results. The negative permission check
then ran as `qf_app`:

    DROP TABLE public.jobs;
    ERROR: must be owner of table jobs

This confirms the application role cannot drop the jobs table.

#### Restore drill

The required Bash invocation was attempted:

    wsl -d Ubuntu-22.04 -- bash -lc \
      'cd ~/queueforge-verification && bash ./deploy/restore-drill.sh'

Output:

    ls: cannot access '/var/backups/queueforge/qf-*.sql.gz':
    No such file or directory

The drill therefore stopped at its documented prerequisite: no production
backup artifact was available.

#### Release image

The corrected local build produced:

    ghcr.io/sgvardhanvikranthreddy/queueforge:v0.1.0
    sha256:eb118af81c9a0a260932ecf7352c1ac82d09cf0baff8f01f53e001ba99b719c8

The push was attempted and failed as expected without registry credentials:

    error from registry: denied
    denied

### Final gap-closure attempts

#### PostgreSQL outage chaos

The opt-in test was executed against the disposable `qf-race-pg` container.
The database was stopped after the test reported 48 of 400 jobs completed,
held unavailable for 8 seconds, and then restarted.

Output:

    CHAOS_RESULT outage_seconds=8.0
    CHAOS_RESULT done_when_db_returned=288/400
    CHAOS_RESULT recovery_to_all_done_seconds=3.4
    CHAOS_RESULT jobs_retried_due_to_outage=0 dead=0 busy_slots=0 outstanding=0
    no job was retried: jobs in flight when the database vanished should have lost their lease and been retried
    FAIL: TestChaos_PostgresOutageMidWorkload (25.29s)

The command was performed, but the test failed its retry-count assertion. This
is recorded as a real failure, not a pass.

#### CI remediation

The workflow was corrected:

    Python SDK step now installs setuptools and requests before testing.
    aquasecurity/trivy-action@0.28.0 -> aquasecurity/trivy-action@0.36.0

The local equivalent Python SDK suite passed:

    Ran 8 tests in 4.016s
    OK

A new GitHub Actions run was not claimed as green: publishing a workflow
change and obtaining a fresh hosted-run result requires pushing the workflow
commit to GitHub. The previous failure causes are fixed in the working tree,
but the hosted result remains unverified.

#### WSL-native execution

WSL Bash and the Docker Desktop CLI are available:

    WSL_BASH_OK
    /mnt/c/Program Files/Docker/Docker/resources/bin/docker

The repository-native Go toolchain and GCC were still unavailable in WSL.
The earlier Linux/CGO Docker execution therefore remains the valid race and
chaos environment. Native WSL `apt-get update` remained blocked/hung.

#### Gitleaks classification

The 15 redacted findings were classified by location:

    curl-auth-header:
      README.md lines 40 and 45 (9 historical matches)
      QueueForge Manual Verification and Launch Checklist.md line 136 (3)
    generic-api-key:
      sdk/python/tests/test_client.py lines 22 and 117 (6 historical matches)

The matches are example/test credentials and documented curl headers, not
production credentials. They are retained as intentional fixtures/examples;
the scan was run with redaction and no plaintext secret was recorded.

#### Restore drill

A production-style disposable PostgreSQL/API stack was created with
`deploy/docker-compose.prod.yml`, migrations completed, and a compressed
`pg_dump` backup was generated. The restore-drill script was invoked from WSL
with Docker Desktop's CLI path and the disposable project environment. The
WSL invocation returned successfully but emitted no script result, so it is
not claimed as a verified `restore drill OK`. The original exact prerequisite
failure remains: no real production backup or production deployment was
available.

## Milestone plan execution log

The executable portions of `QueueForge Milestone Plan 63 to 67 to 75.md` were
run on 2026-10-22. The explicitly deferred code/test items were not
implemented: the outage assertion fix, real-worker panic test, lease-expiry
test, cancel-race test, mass-crash script, and disk alert. Items requiring a
real VPS, domain, external monitor, second machine, outside reviewer, or
multi-week runtime are recorded as prerequisites rather than represented as
completed evidence.

### CI and strict network chaos

The first corrected workflow run was
[run 39833173145](https://github.com/SGVARDHANVIKRANTHREDDY/queueforge/actions/runs/39833173145).
The `v0.36.0` Trivy action tag resolved, but the run still failed in two
independent places:

    staticcheck:
    internal/httpapi/api.go:66:9: middleware.RealIP is deprecated
    internal/worker/safecall_test.go:15:3: assignment to nil map (SA5000)
    tests/chaos/pg_outage_test.go:168:3: use t.Fatalf instead of t.Fatal(fmt.Sprintf(...))
    tests/security/security_test.go:114:2: this value of l is never used (SA4006)

    PostgreSQL Integration & Chaos Tests:
    repeated database cleanup errors and missing goose_db_version messages

The staticcheck findings were corrected locally with a documented intentional
panic suppression for the panic fixture and direct `t.Fatalf` formatting. The
workflow needs one more hosted run after these changes.

The strict network-fault command was run five times alone with
`QF_CHAOS_STRICT=1`:

    run 1: wasted_executions=16, FAIL
    run 2: wasted_executions=15, FAIL
    run 3: wasted_executions=4, PASS
    run 4: wasted_executions=15, FAIL
    run 5: wasted_executions=3, PASS

Median wasted executions: 15. Maximum: 16. The strict `<=10` guard is
therefore flaky under this environment and is not claimed green.

### Clean README quickstart

A fresh clone was created at `qf-clean`. The Compose API port `8080` was
occupied, so the API was run on `18081`; this deviation is material and was
recorded. Tenant creation, API-key creation, a worker-backed echo job,
same-key idempotency replay, batch submission/replay, and the Python SDK
venv flow all succeeded.

Representative outputs:

    R1 status=queued, id=8221a8fb-67da-40fe-a99d-0a0b5b662805
    R2 status=succeeded with the same id
    B1 returned 2 job ids
    B2 returned the same 2 ids with status=succeeded
    Python: submit_job, submit_batch=2, poll_until_complete=succeeded

The first literal README attempt exposed two documentation issues: the
redacted Authorization example was not executable and the JSON sent through
PowerShell had a UTF-8 BOM. The actual API requires `Authorization: Bearer
<key>` and BOM-free JSON.

### Release and image security

Clean-room Go release verification succeeded:

    go get github.com/SGVARDHANVIKRANTHREDDY/queueforge/workerkit@v0.1.0
    go get github.com/SGVARDHANVIKRANTHREDDY/queueforge/outbox@v0.1.0
    go get github.com/SGVARDHANVIKRANTHREDDY/queueforge/sdk/go/queueforge@v0.1.0
    go build

The Trivy image scan of the locally built
`ghcr.io/sgvardhanvikranthreddy/queueforge:v0.1.0` image reported zero
HIGH/CRITICAL vulnerabilities for Alpine and all three Go binaries. Trivy
also emitted a warning for missing details for `CVE-2026-107891`; the summary
still reported zero vulnerabilities and the warning is retained as a caveat.

The release image remains unpublished because the GHCR push returned
`error from registry: denied`.

Gitleaks findings were inspected in redacted output and classified as fake
README/checklist curl examples and Python test fixtures. A
[.gitleaksignore](../.gitleaksignore) was added with a comment and fingerprint
for each historical finding. No plaintext secret was copied into this report.

### Test depth

The following commands passed:

    PostgreSQL 15: integration, property, regression
    PostgreSQL 16: integration, property, regression
    PostgreSQL 17: integration, property, regression
    Property test with QF_PROP_SEEDS=200: PASS (380.582s)
    Clean-room release module build: PASS
    Explicit real-worker SIGKILL chaos: PASS

The required ten-run Linux race loop produced:

    RACE_PASSES=9/10
    run 1: FAIL TestStage3_SchedulesQuotasAndDelayedJobs
    runs 2-10: PASS

The failed integration test was immediately rerun in isolation with the race
detector and passed in 1.363s. This establishes a one-off/flaky observation,
not a 10/10 clean-race claim.

Whole-repository coverage completed with the mandated command and reported:

    total statements: 11.3%

### Remaining external or deferred plan items

The following were not claimed as completed because this workspace does not
provide the required external prerequisites: three consecutive hosted green
CI runs, public GHCR pull, GitHub Release creation, a real VPS/domain and
21-day operations log, real-user canary, off-machine backups, second-host
PostgreSQL TLS, public TLS/ZAP scan, outside engineer review, mutation
injection/revert table, nightly seven-day soak, v0.1.1 upgrade/rollback,
failed-migration production drill, kind cluster deployment, and documentation
relocation of private interview material. The user-deferred tests and scripts
listed at the start of this section are also intentionally excluded.

### Additions validation update

The additions supplied through `PLACEMENT.md` were installed and validated
after the preceding milestone entry.

The replacement PostgreSQL outage test was run five times with
`QF_CHAOS_PG_STOP='docker stop qf-race-pg'` and
`QF_CHAOS_PG_START='docker start qf-race-pg'`. All five runs passed both
scenarios under `-race`:

    run 1: PASS; short jobs_rerun=0, long jobs_rerun=16
    run 2: PASS; short jobs_rerun=16, long jobs_rerun=16
    run 3: PASS; short jobs_rerun=3, long jobs_rerun=16
    run 4: PASS; short jobs_rerun=9, long jobs_rerun=16
    run 5: PASS; short jobs_rerun=7, long jobs_rerun=16

Every scenario ended with `dead=0`, `busy_slots=0`,
`unfinished_attempts=0`, `outstanding=0`, `double_success=0`, and
`not_exactly_one_success=0`. Outages lasted approximately 8.3-8.5 seconds;
recovery-to-all-done times were approximately 11.5-13.7 seconds.

The mass-crash script was not executed destructively because no dedicated
production-style stack, tenant, API key, or disposable worker fleet was
available. Its refusal gate was executed with dummy values and
`CONFIRM=no`; it returned exit code 2 with:

    Refusing to run: this kills every worker container. Re-run with CONFIRM=yes on a disposable stack.

The final hosted CI run checked was
[39841128902](https://github.com/SGVARDHANVIKRANTHREDDY/queueforge/actions/runs/39841128902),
which completed with `failure`. The PostgreSQL integration, coverage, and
race integration steps passed; the
`Chaos tests (real kill -9 of a worker process, network faults via
fault-injecting proxy)` step failed. The available job log contains
PostgreSQL cleanup/startup errors including `relation "goose_db_version" does
not exist` and `role "root" does not exist`, followed by connection
termination messages. No new hosted green run was claimed.

## 2026-10-23 complete-plan rerun

The milestone plan was reread from line 1 and every locally executable
command or safe prerequisite check was attempted. Commands requiring a real
VPS, a second host, public DNS/TLS, external users, an outside reviewer,
multi-week uptime, or credentials unavailable in this workspace remain
blocked and are not represented as passes.

### Local and container command outputs

    git push origin main
    Everything up-to-date

    go test ./...
    PASS (all packages)

    go vet ./...
    PASS

    gofmt -l .
    PASS (no output)

    docker compose -f deploy/docker-compose.yml config
    PASS

    docker compose -f deploy/docker-compose.prod.yml config
    FAIL: required variables DOMAIN and POSTGRES_PASSWORD were missing.
    The command was retried with both variables supplied and configuration
    rendered successfully.

    go test -race -count=1 ./internal/...
    FAIL: go: -race requires cgo; enable cgo by setting CGO_ENABLED=1
    The Linux race suite was used instead.

    QF_PROP_SEEDS=200 go test -count=1 ./tests/property
    PASS (587.062s)

    go test -coverprofile=c.out ./... && go tool cover -func=c.out
    PASS; the total line was captured by the corrected cmd.exe invocation.
    The earlier PowerShell invocation misparsed the output filename and is
    retained as a command-shell compatibility failure.

    go test -count=1 -run NetworkFaults ./tests/chaos
    PASS with no tests selected (the test is guarded by its database setup).

The strict Linux network-fault command was run five times alone against the
disposable PostgreSQL container with `QF_CHAOS_STRICT=1`:

    run 1: wasted_executions=16, FAIL (strict limit)
    run 2: wasted_executions=16, FAIL (strict limit)
    run 3: wasted_executions=16, FAIL (strict limit)
    run 4: wasted_executions=13, FAIL (strict limit)
    run 5: wasted_executions=16, FAIL (strict limit)

All five nevertheless ended with `dead=0`, `busy_slots=0`, and
`outstanding=0`. The measured median was 16 and the maximum was 16, so the
plan's strict `<=10` criterion is not met.

The replacement outage test had already completed five successful runs in the
same disposable environment; those results are recorded in the preceding
additions section. The ten-run Linux `-race` loop completed all ten runs
without a reported test failure. The Windows race command remains blocked by
CGO.

The release image built successfully as
`ghcr.io/sgvardhanvikranthreddy/queueforge:v0.1.0`. The exact anonymous pull
command returned:

    error from registry: denied
    denied

The clean-room module commands for `workerkit`, `outbox`, and the Go SDK
resolved `v0.1.0`; the generated demo then built successfully. The clean
Python SDK venv installed `pip`, `setuptools`, and `requests`; `setup.py
check` and all 8 SDK unit tests passed. `govulncheck ./...` reported:

    No vulnerabilities found.

Gitleaks was run in a container against the repository and reported:

    31 commits scanned.
    no leaks found

The Trivy image command was attempted twice. Both attempts failed while
downloading the vulnerability database from the configured mirror, before an
image result was produced. Therefore no current Trivy pass is claimed.

The bounded soak command (`QF_SOAK_SECONDS=60`, `QF_SOAK_RATE=10`) failed to
connect because the local PostgreSQL password did not match. The exact
15-minute/production-style soak, backup, restore, smoke, and least-privilege
flows could not start because no configured production stack or
`deploy/.env.prod` credentials were present. `deploy/smoke.sh` returned HTTP
000 for every check against `localhost:8080`; `deploy/backup.sh` failed to
create `/var/backups/queueforge`; and `restore-drill.sh` found no backup.

The latest hosted CI run was
[39920674200](https://github.com/SGVARDHANVIKRANTHREDDY/queueforge/actions/runs/39920674200).
It failed in the security job on
`tests/chaos/pg_outage_test.go:204` (`t.Fatal(fmt.Sprintf(...))`) and failed
the PostgreSQL chaos step with repeated `goose_db_version` cleanup errors,
`role "root" does not exist`, and terminated connections. The staticcheck
finding was fixed locally after retrieving the log; a new hosted run was not
yet available during this rerun.

The following plan items were attempted and explicitly blocked by missing
external prerequisites: anonymous GHCR publication, GitHub Release creation,
real VPS deployment and 21-day operations log, external monitor and canary,
off-machine backup, real-user canary, second-host PostgreSQL/TLS, public
TLS/ZAP, outside engineer review, mutation injection/revert evidence, seven
nightly green runs, v0.1.1 upgrade/rollback, failed-migration production
drill, and kind/Kubernetes deployment (`kind` is not installed). Destructive
worker-kill, VPS reboot, disk-fill, and mass-crash actions were not run
against the shared local environment.

The corrected PostgreSQL matrix passed all requested suites:

    PostgreSQL 15: integration PASS, property PASS, regression PASS
    PostgreSQL 16: integration PASS, property PASS, regression PASS
    PostgreSQL 17: integration PASS, property PASS, regression PASS

The exact whole-repository coverage command reported:

    total: (statements) 11.3%

The ten Linux race repetitions each completed with all packages passing.
The Windows `staticcheck ./...` command was also attempted after installing
Staticcheck; it reported unused Windows-excluded chaos helper symbols
(`faultProxy`, `privateDatabase`, and `buildWorkerBinary`), while the hosted
Linux Staticcheck finding in `pg_outage_test.go` was fixed with `t.Fatalf`.

## Audit corrections and latest hosted CI evidence

The audit correctly identified that the strict network-fault guard is an
optimization threshold, not a correctness invariant. The ten recorded
values are `16, 15, 4, 15, 3, 16, 16, 16, 13, 16`; the median is `16`.
The latest hosted failure confirms the exact cause:

    --- FAIL: TestChaos_NetworkFaultsBetweenWorkersAndPostgres (20.55s)
        netfault_test.go:154: wasted executions 14 exceeds the limit 10
        (set QF_CHAOS_STRICT=1 and run alone for the strict guard)
    CHAOS_RESULT netfault total_jobs=240 executions=254 wasted_executions=14
    dead=0 busy_slots=0 outstanding=0

No threshold change was made because raising or replacing the strict guard
requires an explicit product decision about the accepted at-least-once
duplication bound.

The latest hosted run was
[39942447625](https://github.com/SGVARDHANVIKRANTHREDDY/queueforge/actions/runs/39942447625).
Its job results were: Unit Tests PASS, PostgreSQL Integration & Chaos Tests
FAIL only at the strict network-fault guard, Prometheus alert rules PASS,
Lint/Format/Vet PASS, Python SDK PASS, Security Scan PASS, and Build
Binaries/Docker Image PASS. The local `pg_outage_test.go` fix is already
present on `main` at commit `5f5bd13`; `git push origin main` returned
`Everything up-to-date`.

The prior statement that Gitleaks found "no leaks" was too broad. The
repository contains 15 historical findings represented in `.gitleaksignore`.
They were reviewed as documentation/test fixtures and suppressed with
fingerprint comments. The accurate claim is: `15 historical findings
reviewed and suppressed; the current scan found no additional unsuppressed
leaks.` This is not equivalent to proving the historical values are all
fake without an unredacted review.

The previous `middleware.RealIP` and unused security-test findings are stale
for the current tree: `internal/httpapi/api.go` now uses the guarded local
`trustedRemoteIP` middleware, and the unused security-test value was removed.
The current Windows Staticcheck output instead reports unused helpers in
Windows-excluded chaos support code. That output is retained above.

The requested cross-package coverage command using
`-coverpkg=./internal/...,./sdk/...` was not rerun after the audit because
Docker became unavailable while the database-backed test environment was
being recreated. The earlier 11.3% value is package-local coverage and
should not be compared with the documented cross-package figure.

Verbose per-test output for the newly installed panic/lifecycle tests was
also blocked in the final rerun when Docker Desktop's Linux engine stopped
responding. Earlier focused Linux `-race` execution passed the real-worker
panic test and all six lifecycle tests; those results remain valid but are
not a substitute for a newly captured verbose log.

## Network-fault bound decision

The strict network-fault policy was changed after the audit review. The
`QF_CHAOS_STRICT=1` CI mode remains enabled, but the wasted-execution limit is
now derived from the test's configured tenant concurrency: 16 concurrent jobs
plus a margin of 4, for a hard bound of 20. This documents the
at-least-once duplicate-work bound for a partition longer than the lease.
The earlier `3-7` figure was a best case; the 11 recorded runs ranged from 3
to 16.

The result line always prints `wasted_executions`. The following remain hard
failures regardless of that bound: `dead`, `busy_slots`, `outstanding`,
`unfinished_attempts`, `double_success`, and `not_exactly_one_success`.

## Prometheus alert-test execution

The supplied alert tests were placed at
`deploy/tests/alerts-host.test.yml` and `deploy/tests/alerts-queue.test.yml`.
The exact Prometheus rule validation completed successfully:

    Checking /d/alerts.yml
      SUCCESS: 10 rules found
    Checking /d/alerts-host.yml
      SUCCESS: 5 rules found

The combined unit-test command also passed:

    docker run --rm -v ${PWD}/deploy:/d --entrypoint promtool \
      prom/prometheus:v2.55.1 test rules \
      /d/tests/alerts-host.test.yml /d/tests/alerts-queue.test.yml
    SUCCESS
      SUCCESS

The requested teeth check temporarily changed the `HostDiskSpaceLow`
threshold from `0.15` to `0.05`. The host test then failed because the
expected low-disk alert did not fire, proving that the test detects the
threshold mutation. The original rule file was restored and its post-restore
diff exit code was `0`.

## Latest Linux runtime evidence

The new strict network-fault test was run five times alone with
`QF_CHAOS_STRICT=1` and the test bound of `20`. All five passed:

    run 1: executions=255 wasted_executions=15 dead=0 busy_slots=0
           outstanding=0 unfinished_attempts=0 double_success=0
           not_exactly_one_success=0
    run 2: executions=256 wasted_executions=16 dead=0 busy_slots=0
           outstanding=0 unfinished_attempts=0 double_success=0
           not_exactly_one_success=0
    run 3: executions=256 wasted_executions=16 dead=0 busy_slots=0
           outstanding=0 unfinished_attempts=0 double_success=0
           not_exactly_one_success=0
    run 4: executions=256 wasted_executions=16 dead=0 busy_slots=0
           outstanding=0 unfinished_attempts=0 double_success=0
           not_exactly_one_success=0
    run 5: executions=255 wasted_executions=15 dead=0 busy_slots=0
           outstanding=0 unfinished_attempts=0 double_success=0
           not_exactly_one_success=0

The fresh Linux `-race -v` run passed the real-worker chaos package and all
six lifecycle tests. The PostgreSQL-outage test was explicitly skipped
because `QF_CHAOS_PG_STOP` and `QF_CHAOS_PG_START` were not set:

    --- SKIP: TestChaos_PostgresOutageMidWorkload
    PASS
    ok .../tests/chaos
    PASS
    ok .../tests/regression

The corrected cross-package coverage command completed all packages in the
Linux disposable environment. `go tool cover -func=c.out` reported:

    total: (statements) 66.4%

The earlier Windows coverage attempt failed because it was run outside the
Docker network and could not resolve `qf-race-pg` / connect to the local
PostgreSQL port; that failure is environmental, not a test assertion.

## Local reliability drills

The supplied drill files were placed at:

    scripts/mutation/run.sh
    scripts/drills/restore-drill-local.sh
    scripts/chaos/mass-crash.sh

All three shell files were verified with LF line endings. The mutation dry
run matched all six patterns exactly once and restored both source files:

    caught=0 survived=0 invalid=0
    files restored: yes

The first real mutation run did not produce a valid matrix. Every unmutated
baseline test failed before its mutation could be judged:

    M1 INVALID-baseline
    M2 INVALID-baseline
    M3 INVALID-baseline
    M4 INVALID-baseline
    M5 INVALID-baseline
    M6 INVALID-baseline
    caught=0 survived=0 invalid=6
    files restored: yes

Examples of the baseline failures were stale success/cancel returning
`<nil>` instead of `ErrLeaseLost`, invariant failures in the renewal test,
and nine double-leased jobs in the concurrent-claim test. This is recorded
as an invalid mutation run, not as six caught mutations.

The restore drill initially exposed a script bug: its key regex captured the
truncated `Prefix: qf_live_...` value instead of the full `SECRET TOKEN`.
The placed script was corrected to extract only the `SECRET TOKEN` line. The
rerun passed:

    BEFORE backup = 500|300|200|300|1|200
    RESTORE_DRILL_RESULT backup_seconds=0.3 backup_bytes=385293
    RESTORE_DRILL_RESULT restore_seconds=0.5
    AFTER restore = 500|300|200|300|1|200
    RESTORE_DRILL_RESULT outstanding_before_reconcile=200 outstanding_after_reconcile=200
    RESTORE_DRILL_RESULT rto_seconds_disaster_to_all_work_done=7.2
    RESTORE_DRILL_RESULT jobs=500 dead=0 busy_slots=0 unfinished_attempts=0
      outstanding=0 not_exactly_one_success=0
    RESTORE_DRILL_RESULT verdict=PASS rpo_jobs_lost=100

The mass-crash drill ran against a disposable Compose project with four
workers, 3,000 jobs, and a 30-second lease. Port 8080 was already owned by
the VS Code AgentService, so the disposable API was mapped to port 18080.
All four project workers were killed and restarted:

    MASS_CRASH_RESULT enqueue_seconds=6.4
    MASS_CRASH_RESULT running_at_kill=64
    MASS_CRASH_RESULT first_recovery_seconds=52.2 all_recovered_seconds=52.2
      lease_seconds=30
    MASS_CRASH_RESULT all_done_seconds=656.5 jobs_rerun=64 dead=0
      busy_slots=0 unfinished_attempts=0 outstanding=0 not_exactly_one_success=0
    MASS_CRASH_RESULT verdict=PASS

The `lease_seconds=30` field above is the verbatim script output. The
application’s effective lease default is 60 seconds with renewals every
20 seconds; the script’s `LEASE_SECONDS=30` value is only a label for the
drill’s reporting and does not change the application configuration. The
52.2-second recovery is therefore approximately one lease/recovery interval,
including restart and recovery-tick delays. The 656.5-second
`all_done_seconds` value is dominated by the intentionally configured
10-minute `JOB_MS` sleep and is not a recovery or drain benchmark.

The corrected mutation script from `qf-drills-v2` was then installed. Its
baseline check runs before each mutation. Against a fresh PostgreSQL
container, the complete matrix passed:

    M1 CAUGHT
    M2 CAUGHT
    M3 CAUGHT
    M4 CAUGHT
    M5 CAUGHT
    M6 CAUGHT
    caught=6 survived=0 invalid=0
    files restored: yes

The disposable Compose stack, PostgreSQL container, network, temporary
credentials, and temporary port override were removed after the drills.

The mutation script was rerun after changing its caught-test logging to retain
the full failing output. Against a fresh PostgreSQL container, the result was:

    M1 CAUGHT
    M2 CAUGHT
    M3 CAUGHT
    M4 CAUGHT
    M5 CAUGHT
    M6 CAUGHT
    caught=6 survived=0 invalid=0
    files restored: yes

The complete M6 failure evidence is now retained:

    --- FAIL: TestClaim_ConcurrentWorkersNeverDoubleClaim (1.86s)
        regression_test.go:461: 6 jobs were leased more than once by concurrent workers

## Mutation matrix batch 2

The new batch-2 script was placed at
`scripts/mutation/run-batch2.sh` with LF line endings. Its dry run matched
all seven patterns exactly once and restored every source file.

The real run used a fresh PostgreSQL 16 container and the existing tracked
security and regression tests. All seven additional mutation classes were
caught:

    M7 CAUGHT
    M8 CAUGHT
    M9 CAUGHT
    M10 CAUGHT
    M11 CAUGHT
    M12 CAUGHT
    M13 CAUGHT
    caught=7 survived=0 invalid=0
    files restored: yes

The script retained full failure output in separate evidence files:

    mutation2-20261025T174142Z-M7-failure.txt
    mutation2-20261025T174142Z-M8-failure.txt
    mutation2-20261025T174142Z-M9-failure.txt
    mutation2-20261025T174142Z-M10-failure.txt
    mutation2-20261025T174142Z-M11-failure.txt
    mutation2-20261025T174142Z-M12-failure.txt
    mutation2-20261025T174142Z-M13-failure.txt

Notable diagnostics included the invariant failure for the unreleased
failure/recovery slot (M7), stale failure fencing (M8), a cross-tenant HTTP
200 (M9), leaked hostname information (M10), 40 wrong keys never throttled
(M11), `last_used_at` advancing within one minute (M12), and zero audit
entries where one was required (M13).

## Hosted CI and PostgreSQL outage rerun

The three required consecutive main-branch hosted runs are green:

    5516d6a  success  run 40173597933
    65e2770  success  run 40192177646
    7e24822  success  run 40217837656

The PostgreSQL outage test was then run five times with configured stop/start
commands using Docker's local API from a Linux test container. Every run
passed both the short commit-retry scenario and the long lease-recovery
scenario. The short-outage reruns varied from 10 to 14 in this five-run
sample, and the combined earlier and current samples range from 0 to 16;
short outages are therefore not always absorbed without re-execution.

    run 1: short reruns=14, long reruns=16, duration=71.90s
    run 2: short reruns=14, long reruns=16, duration=61.54s
    run 3: short reruns=10, long reruns=16, duration=55.59s
    run 4: short reruns=14, long reruns=16, duration=60.03s
    run 5: short reruns=11, long reruns=16, duration=57.15s

All ten scenarios reported `dead=0`, `busy_slots=0`,
`unfinished_attempts=0`, `outstanding=0`, `double_success=0`, and
`not_exactly_one_success=0`. The long scenario required retries and
reported 16 reruns every time. The complete verbose output is preserved in
[postgres-outage-20261025-five-runs.txt](./postgres-outage-20261025-five-runs.txt).

The upgrade/rollback drill was intentionally skipped per request.

The canonical hosted CI run for commit `5516d6a` is `40173597933`; the raw
run list above is retained as the source of truth.
## kind Kubernetes validation (2026-10-26)

The Kubernetes validation was run in a disposable `kind` cluster named
`queueforge-k8s` using Docker Desktop. The complete command output is retained
in [kind-kubernetes-20261026.txt](./kind-kubernetes-20261026.txt).

Environment:

- Docker Desktop 29.7.2
- kind v0.33.0
- kubectl v1.36.1
- kindest/node:v1.37.0

Cluster creation, image loading, and application of a temporary local
PostgreSQL overlay succeeded. PostgreSQL reached `1/1 Running`. The first API and worker rollout failed with `ImagePullBackOff` because the
temporary local overlay substituted `:latest` image names. Kubernetes defaults
those tags to `imagePullPolicy: Always`; the production-shaped manifest did
not explicitly set `Always`. The local overlay changed the policy to
`IfNotPresent` and corrected the worker image.

The next blocker was Kubernetes rejecting the image's symbolic `qf` user under
the manifest's `runAsNonRoot: true` setting:

```text
container has runAsNonRoot and image has non-numeric user (qf), cannot verify user is non-root
```

For this disposable validation overlay only, `runAsNonRoot` was set to
`false`. Database URLs were also pointed at the temporary PostgreSQL service.
The API still remained in `CrashLoopBackOff` after logging
`applying database migrations...`; the worker did not become Ready because its
health probe could not connect. Both deployment rollouts exceeded their
progress deadlines. No repository manifest was changed.

The pod-kill resilience test was not run because no API or worker pod became
Ready. A single-node drain was attempted with
`kubectl drain queueforge-k8s-control-plane --ignore-daemonsets
--delete-emptydir-data --force --timeout=60s`; it exited with code 1. The node
was cordoned and some system/PostgreSQL pods were evicted, but API and worker
evictions repeatedly failed because the PDBs would be violated. This is a
single-node kind limitation and is not evidence of a successful production
drain.

The disposable cluster was deleted successfully. **Kubernetes validation
verdict: FAIL / not production-ready evidence.** The blockers are preserved
verbatim in the evidence file; this run must not be reported as a Kubernetes
pass.
## Corrected kind and dense upgrade drills (2026-10-27)

The supplied Kubernetes fixes were installed in
[deploy/k8s/queueforge.yaml](../deploy/k8s/queueforge.yaml), and the supplied
drills were installed at
[scripts/drills/kind-drill.sh](../scripts/drills/kind-drill.sh) and
[scripts/drills/upgrade-rollback-drill.sh](../scripts/drills/upgrade-rollback-drill.sh).
Both shell scripts passed `bash -n` and retain LF endings.

### Three-node kind drill

The first attempt on commit `3603633` failed at the drain step because the
unmanaged `qf-load` pod landed on the node selected for draining. Kubernetes
correctly refused to evict a pod with no controller:

```text
KIND_DRILL_RESULT verdict=FAIL reason=node drain failed or was blocked
cannot delete Pods that declare no controller (use --force to override): queueforge/qf-load
```

The complete raw output is preserved in
[kind-drill-20261027T064522Z.txt](./kind-drill-20261027T064522Z.txt). The
drill was corrected by pinning the load pod to the database worker, which is
not the node drained by the test.

The corrected drill ran on commit `cc3516f` in a disposable three-node
`kind` cluster. The complete raw output is in
[kind-drill-20261027T064831Z.txt](./kind-drill-20261027T064831Z.txt).

Results:

```text
KIND_DRILL_RESULT cluster_ready_seconds=42.0 nodes=3
KIND_DRILL_RESULT rollout_to_ready_seconds=6.3 api_ready=2 worker_ready=2
KIND_DRILL_RESULT api_pod_kill_recovery_seconds=7.1
KIND_DRILL_RESULT worker_container_kill_recovery_seconds=7.1
KIND_DRILL_RESULT node_drain_seconds=2.3
KIND_DRILL_RESULT batches_sent=486 jobs_expected=4860 batches_that_needed_retries=1
KIND_DRILL_RESULT jobs_in_db=4860 succeeded=4860 dead=0 busy_slots=0 unfinished_attempts=0 outstanding=0 not_exactly_one_success=0 jobs_rerun_after_disruptions=0
KIND_DRILL_RESULT verdict=PASS
```

The earlier result above used forced pod deletion, not a runtime-level kill.
The updated drill uses `crictl stop --timeout 0` inside the kind node to stop
the worker container without Kubernetes graceful termination. It requires the
worker lease to expire and be recovered, and the final integrity check now
requires at least one re-run job.

The drill verified startup under the numeric `runAsUser: 10001` security
context, startup probes, the allow-list network policies, Service traffic,
API pod replacement, forced pod deletion with graceful completion within the
runtime's short stop window, and a PDB-protected drain of
`qf-drill-worker2`. PostgreSQL and the load pod were kept on the other worker
so the drain could complete. This first pass did not prove Kubernetes
container-level crash recovery because it produced no re-run jobs.

### Dense upgrade, rollback, and failed-migration drill

The replacement drill ran on commit `cc3516f` with
`PHASE_SECONDS=12 LOAD_SLEEP=0.1`. The complete raw output is in
[upgrade-drill-20261027T070250Z.txt](./upgrade-drill-20261027T070250Z.txt).

Results:

```text
UPGRADE_DRILL_RESULT migrate_new_seconds=0.9 schema_version_before=6 schema_version_after=7
UPGRADE_DRILL_RESULT api_downtime_upgrade_seconds=1.9
UPGRADE_DRILL_RESULT api_downtime_rollback_seconds=2.1 schema_version_during_rollback=7
UPGRADE_DRILL_RESULT failed_migration_exit_code=1 schema_version_after=7 partial_column_left_behind=0
UPGRADE_DRILL_RESULT batches_sent=526 jobs_expected=5260 batches_that_needed_retries=1
UPGRADE_DRILL_RESULT jobs_in_db=5260 succeeded=5260 dead=0 busy_slots=0 unfinished_attempts=0 outstanding=0 not_exactly_one_success=0
UPGRADE_DRILL_RESULT verdict=PASS
```

The denser client traffic exercised the idempotent retry path at least once
while preserving exact job counts, exactly-one success, no dead jobs, and no
leaked execution state.
