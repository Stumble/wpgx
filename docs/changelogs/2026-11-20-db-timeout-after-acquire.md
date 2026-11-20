# feat: start the database timeout after a connection is acquired

Primary record for the cross-repository change. Local records:
Stumble/sqlc `docs/changelogs/2026-11-20-db-timeout-after-acquire.md` and
Stumble/bookstore `docs/changelogs/2026-11-20-db-timeout-after-acquire.md`.

## 1. Background and Current State

- Problem/outcome: every wicked-sqlc query carries a mandatory
  `-- -- timeout: T` annotation (about 2,985 annotations in eight Alva
  services; 1s is the most common value, 250ms/500ms are frequent). The owner
  wants T to protect the database: it should bound the time a statement holds
  a database connection, not the time the caller waits in the client pool.
- Current implementation (wpgx v0.5.1, sqlc v2.5.0, dcache v0.5.0):
  - Generated methods start `context.WithTimeout(ctx, T)` before the cache
    lookup and before the pool `Acquire`. wpgx creates no deadline;
    `pgxpool.Query/QueryRow/Exec/CopyFrom` acquire internally with the same
    context. `wpgx_latency_milliseconds` also starts before `Acquire`.
  - pgxpool `Acquire` covers waiting for an idle connection or a free slot,
    construction of a new connection (puddle detaches construction from the
    caller's cancellation; the caller only stops waiting), a ping after one
    second idle, and the `PrepareConn` (`BeforeAcquire`) hook.
  - Inside `Pool.Transact`, statements run on the held connection; their T is
    already almost pure database time. `BeginTx`, `Commit` and the gaps between
    statements are bounded only by the caller's context.
  - dcache receives the generated context. go-redis runs with
    `ContextTimeoutEnabled=false`, so Redis I/O is bounded by the client's
    `ReadTimeout`/`WriteTimeout` (gglib default 3s), not by T.
- Runtime evidence: the 2026-11-04/05 PRD surge of `context deadline
  exceeded` on worldline was largely pool waiting (app pool full 65–100% in
  the spike windows; worldline-server gitops notes that sqlc's timeouts count
  the wait for a connection). Errors were cheap point operations, the
  connection-wait signature. Source: owner's read-only investigation record.
- Constraints: upgrades must be painless (no required configuration, good
  defaults); old generated code must keep its behavior with new wpgx; no
  production database access; Stumble repositories are released by the owner.
- Unknowns: PgBouncer's own queue (transaction pooling) sits after `Acquire`
  and stays invisible to the client; role-level server `statement_timeout`
  settings on live databases were not checked.

## 2. Problem Model and End-to-End Behavior

- Root cause: the only deadline is created before cache and pool waiting, so
  local queueing consumes the database budget. Under contention a query can
  start late and be cancelled mid-execution, wasting database work and
  destroying the connection, and pool exhaustion is reported as a database
  timeout.
- Goals: T bounds database occupancy; pool waiting has its own finite bound
  with a default that suits short timeouts; caller deadlines and cancellation
  always apply; phase-level metrics; no required configuration.
- Non-goals: `Pool.Transact` semantics, server-side `statement_timeout` or
  cancel requests, dcache changes, PgBouncer queue visibility.
- B1 — A statement started with `WithDBTimeout(ctx, name, T)` on a pool
  connection waits for a connection at most `max(T, AcquireTimeout)`
  (default `AcquireTimeout` 2s), then runs under a context derived from the
  caller's context with timeout T that starts once the connection is ready and
  ends when the connection is released: `Exec`/`CopyFrom` return, `Row.Scan`
  returns, or `Rows` reach the end or are closed (row streaming and drain
  included).
- B2 — In a transaction (`WTx`), T starts at the call, as before.
- B3 — Without `WithDBTimeout` for that statement name (old generated code,
  hand-written calls, `Transact`), behavior is identical to v0.5.1.
- B4 — Errors identify the expired budget: an acquire timeout matches
  `ErrAcquireTimeout` and `context.DeadlineExceeded`; a database timeout
  matches `ErrDBTimeout` and `context.DeadlineExceeded`; caller deadline or
  cancellation errors are returned unchanged.
- B5 — `Rows` returned under a database timeout implement
  `Context() context.Context`, the statement context, so a row visitor can
  observe the same deadline. `StatementErr(ctx, err)` marks a deadline error
  of that context with `ErrDBTimeout` (a caller's own cancellation cause is not
  substituted) and, for a nil `err`, reports an expired database timeout while
  ignoring the cancellation that finishing the rows causes.
- B6 — Existing metrics keep their meaning. New: `wpgx_acquire_wait_milliseconds`
  {app, replica}, `wpgx_db_latency_milliseconds` {app, op, replica},
  `wpgx_timeout_total` {app, op, replica, phase=acquire|db}, and new
  `wpgx_conn_pool` op values (`acquire_waiting`, `acquire_rejected_count`,
  `empty_acquire_count`, `empty_acquire_wait_seconds`,
  `canceled_acquire_count`, `new_conns_count`).
- B7 — Optional `MaxAcquireWaiters` (default 0, unlimited): when that many
  statements already wait for a connection, a new one fails immediately with
  `ErrTooManyAcquireWaiters`.
- B8 — `AcquireTimeout` and `MaxAcquireWaiters` exist on `Config` and
  `ReadReplicaConfig` (env `POSTGRES_ACQUIRETIMEOUT`, default `2s`;
  `POSTGRES_MAXACQUIREWAITERS`, default 0). Zero `AcquireTimeout` in a struct
  literal means 2s.
- F1 — Pool exhausted past the wait bound: `ErrAcquireTimeout`, no SQL sent,
  no connection or goroutine leaked.
- F2 — Slow statement: `ErrDBTimeout` after T from connection readiness; the
  connection is released (pgx destroys it when the timeout broke it).
- F3 — Caller deadline or cancellation during waiting or execution: the
  caller's error, promptly, with no leak.
- F4 — Negative `AcquireTimeout` or `MaxAcquireWaiters`: `Config.Valid` fails.
- F5 — A connection that pgxpool hands over after the wait ended (a slow
  `BeforeAcquire` hook) is released and the statement fails with the wait's
  error; a caller canceled before admission gets its own error, not
  `ErrTooManyAcquireWaiters`.
- F6 — A scan or `Values` error on budgeted rows frees the connection without
  `Close`, as pgxpool rows do; a panic in an argument encoder or row scanner
  frees it too.
- F7 — Rows already buffered before T expires can still be read and the rows
  report no error; the statement context reports the expiry (`StatementErr`
  matches `ErrDBTimeout`) and `wpgx_timeout_total{phase="db"}` counts every
  statement whose T expired before it finished.
- Compatibility: `WQuerier`/`WGConn` and all signatures are unchanged.
  Wrappers that forward the context (for example connection diagnostics)
  get the new behavior automatically.

## 3. Research, Findings, and Architecture Decision

- Repository evidence: pgxpool v5.11 `Acquire`, puddle constructor context,
  pgconn `DeadlineContextWatcherHandler` default (no server cancel), go-redis
  v9 `ContextTimeoutEnabled`, alfs `dbdiag` measuring connection readiness in
  `BeforeAcquire`, worldline per-transaction `set_config('statement_timeout')`.
- External: OpenTelemetry database client semantic conventions
  (`db.client.connection.wait_time`, `use_time`, `pending_requests`,
  `timeouts`, experimental) for the metric split; Envoy-style
  `max_pending_requests` for a bounded wait queue; PgBouncer closes the server
  connection when a client disconnects mid-statement; PostgreSQL
  `client_connection_check_interval` defaults to 0.
- Approaches considered: (a) one clock covering cache and database, paused
  while acquiring; (b) separate clocks per phase; (c) server-side only.
  (a) lets a slow cache shrink the database budget; (c) does not stop pool
  waits from being reported as database timeouts. Within (b), a pre-database
  clock equal to T was rejected because 250ms is far too short as a queueing
  allowance.
- D1 — Owner-approved model (2026-11-19, US Pacific): the annotation is the
  database budget; pool waiting is bounded by `max(T, AcquireTimeout)` with a
  2s default (owner raised it from 1s); cache work is bounded by the Redis
  client's own timeouts; `MaxAcquireWaiters` exists but is off by default;
  `Transact` is untouched; the new sqlc minor generates this by default;
  existing metrics stay, new phase metrics are added.
- D2 — Carry the budget in the context, keyed by the statement name
  (`WithDBTimeout`). The `WQuerier` interface stays; a value for another name
  is ignored, so nested statements do not inherit it. Rejected: new optional
  interface methods (wrappers would silently fall back to old semantics) and
  pgx-style option arguments (mocks and argument logging would see them).
- D3 — For pool connections wpgx calls `pgxpool.Acquire` explicitly under a
  wait context, then derives the statement context from the caller's context.
  Release and cancellation happen when the result is consumed.
- D4 — Use `context.WithTimeoutCause` for both budgets; wrap timeouts in an
  error that matches the wpgx sentinel and the original error, so existing
  `errors.Is(err, context.DeadlineExceeded)` handling keeps working.
- D5 — Pool-wide waiting is reported per pool (no op label); database time is
  reported per op. Pool counters come from `pgxpool.Stat` every 3s.
- R1 — Worst-case latency grows to `max(T, 2s) + T` (plus cache) for callers
  without deadlines; mitigated by caller deadlines, metrics and the optional
  waiter cap.
- R2 — PgBouncer queueing after `Acquire` still counts as database time.
- R3 — A client-side timeout does not cancel the server statement (pgx default);
  unchanged here.
- R4 — Until wpgx is tagged, sqlc fixtures and bookstore pin a wpgx
  pseudo-version from this branch; merge order matters (section 4).
- R5 — Wrappers that hide `Rows.Context()` make generated Each visitors fall
  back to the caller's context; the row stream is still bounded by wpgx.
- R6 — Cache lookups are bounded by the Redis client's timeouts only with
  dcache v0.3.0+ and Redis single flight off (the default; every Alva service
  is on dcache v0.4.0 and none enables `EnableRedisSingleFlightOption`). With
  Redis single flight (always on before v0.3.0, e.g. Stumble/bookstore's
  v0.1.3), an unresponsive Redis is retried until the caller's context ends,
  which the query timeout used to cut short. Found while running the bookstore
  contracts (2026-11-19 US Pacific). Stumble/bookstore moves to dcache v0.4.0
  so its contract proves the Redis-client bound (round-1 review, R1-06).

## 4. Implementation Design

- Change map (wpgx):
  - `budget.go` (new): `DefaultAcquireTimeout`, `WithDBTimeout`, sentinel
    errors, budget error type, per-pool `acquirer` (wait bound, waiter count,
    rejection count), statement state (`stmt`) that owns the statement context,
    the connection to release and phase metrics, and wrappers for rows/row
    that release on completion.
  - `wconn.go`, `wtx.go`: branch on `dbTimeoutOf(ctx, name)`; legacy branch is
    the unchanged v0.5.1 code.
  - `pool.go`: create one acquirer per pgx pool (broken replicas share the
    primary's); pass it to `WConn`; report its counters with pool stats.
  - `config.go`: `AcquireTimeout`, `MaxAcquireWaiters`, validation.
  - `prometheus.go`, `otel.go`/`observed.go`: new collectors, span attribute
    `wpgx.acquire_wait_ms`.
  - `README.md`: document the semantics, defaults, errors and metrics.
- Critical algorithm (pool connection):

  ```go
  wait := max(T, acquirer.timeout)              // timeout 0 -> 2s
  if limit > 0 && waiting+1 > limit -> ErrTooManyAcquireWaiters
  actx := context.WithTimeoutCause(ctx, wait, acquireCause)
  conn, err := pool.Acquire(actx)               // caller cancel still applies
  sctx := context.WithTimeoutCause(ctx, T, dbCause) // starts now
  run on conn with sctx; on completion: cancel(sctx); conn.Release()
  ```

  Classification: wrap only when `errors.Is(err, context.DeadlineExceeded)`
  and `context.Cause(<budget ctx>)` is that budget's cause.
- Migration/rollout: release wpgx as a new minor (owner tags). Old generated
  code is unaffected. sqlc's new minor requires it. Merge order: wpgx → tag →
  bookstore bumps to the tag → sqlc bumps fixtures and the bookstore ref → tag.
  Rollback: regenerate with the old sqlc; wpgx keeps the legacy path.

### Serial Implementation Checklist

- [x] wpgx: config fields, validation and tests.
- [x] wpgx: budget primitives (context value, errors, acquirer, stmt, wrappers).
- [x] wpgx: budgeted WConn/WTx paths with PostgreSQL tests (B1–B5, B7, F1–F3).
- [x] wpgx: metrics and span attribute with tests (B6).
- [x] wpgx: README and lint/test verification.
- [x] sqlc: templates/imports, fixtures, runtime contract tests, docs.
- [x] bookstore: regenerate with the new sqlc and bump wpgx.
- [x] Cross-agent review; publication and CI tracked in section 7.

## 5. Verification and E2E Design

- Testability: PostgreSQL-backed tests use the existing testsuite with a
  dedicated pool (`MaxConns: 1`) so a held connection makes waiting
  deterministic; timing assertions use wide margins.
- E2E Required: no — no Alva local-stack E2E was requested. wpgx PostgreSQL
  tests, sqlc generated-code tests and bookstore runtime contracts cover the
  contract.
- Commands (GOMAXPROCS=2, `-p 1`, one at a time on the shared host):
  - wpgx: `go build ./...`, `go vet ./...`, `make test-cmd` against a
    disposable PostgreSQL container, golangci-lint per CI version if runnable.
  - sqlc / bookstore commands in their local records.

| Behavior | Evidence |
|----------|----------|
| B1 | waits past T and succeeds when a connection frees; wait bound = max(T, AcquireTimeout) |
| B2 | transaction statement times out after T without acquiring |
| B3 | legacy path keeps acquire inside the caller deadline; other-name value ignored |
| B4/F1/F2/F3 | error classification tests for acquire, database, caller deadline and cancel |
| B5 | rows `Context()` deadline |
| B6 | collectors gathered after budgeted calls |
| B7 | waiter cap rejects without waiting |
| B8/F4 | config defaults and validation tests |

## 6. Human Decisions and Interaction

- 2026-11-19 (US Pacific): the owner asked to exclude pool waiting from the
  sqlc timeout and to keep good defaults with almost no configuration; raised
  the pool-wait default to 2s; agreed to leave `Transact` alone; approved the
  rest of the summarized model; asked to implement through code review and
  publish all PRs ready, contacting them only after code review. This
  instruction replaces the separate plan-approval round.
- Main implementation session: Forge `425998b5-747f-44a4-b7fe-52d8f4eabf6f`
  (Claude Code) on env-woddv6j0.

## 7. Outcome and Evidence

- Result: statements given `WithDBTimeout` wait for a pooled connection at most
  `max(T, AcquireTimeout)` (default 2s) and then run under `T` from the moment
  they have a connection until it is free; sqlc generates this for every query
  (Stumble/sqlc local record); Stumble/bookstore's runtime contracts prove it
  end to end. Old generated code is unaffected.
- B/F/D/R reconciliation:

| ID | Status | Evidence |
|----|--------|----------|
| B1 | DONE | `TestDBTimeoutStartsAfterAcquire`, `TestAcquireWaitIsTheLargerOfTimeouts`, `TestZeroAcquireTimeoutMeansDefault`; sqlc `waiting_for_a_connection_does_not_count`; bookstore `TestWickedTimeoutExcludesPoolWait` |
| B2 | DONE | `TestTransactionStatementTimeout` |
| B3 | DONE | `TestWithoutDBTimeoutWaitingCountsAgainstTheContext`, `TestDBTimeoutForAnotherStatementIsIgnored`, unchanged legacy branches |
| B4 | DONE | `TestEveryOperationReportsAcquireTimeout`, `TestDBTimeout`, `TestCallerDeadlineAndCancelWin`, `TestClassifyOnlyWrapsTheBudgetsOwnDeadline` |
| B5 | DONE | `TestRowsCarryTheStatementContext`, `TestStatementErr`, `TestStatementErrAfterNormalFinish`; sqlc Each tests |
| B6 | DONE | `TestPhaseMetrics`, `TestBufferedRowsPastTheTimeout`, `TestSpanRecordsAcquireWait` |
| B7 | DONE | `TestMaxAcquireWaiters`, `TestCanceledCallerIsNotRejectedAsBusy` |
| B8 | DONE | `TestAcquireDefaultsFromEnv`, `TestNewAcquirerDefaultsTheTimeout` |
| F1–F4 | DONE | tests above; `TestValidRejectsNegativeAcquireLimits` |
| F5 | DONE | `TestSlowBeforeAcquireCannotOutlastTheWait`, `TestCanceledCallerIsNotRejectedAsBusy` |
| F6 | DONE | `TestScanErrorReleasesTheConnection`, `TestPanicsReleaseTheConnection` |
| F7 | DONE | `TestBufferedRowsPastTheTimeout`; sqlc `draining_past_the_timeout_after_an_early_stop_fails` |
| D1–D5 | DONE | as designed; `ContextErr` became `StatementErr` (below) |
| R1–R3, R5 | ACCEPTED | documented, unchanged by design |
| R4 | DONE | pins in sqlc/bookstore at the wpgx branch commit until a tag exists |
| R6 | DONE for Alva/bookstore | Alva services use dcache v0.4.0 without Redis single flight; bookstore moved to it; sqlc GUIDE states the precondition |

- Deviations from section 4 (non-material, reviewed): `ContextErr` was replaced
  by `StatementErr(ctx, err)` so Each can also mark a visitor's deadline error
  and a database timeout that expired while draining buffered rows; buffered
  rows read after `T` keep succeeding (F7) while the expiry is counted and
  visible through `StatementErr`; the generator reserves the identifier `wpgx`.
- Verification (final content; `GOMAXPROCS=2`, `-p 1`, disposable PostgreSQL 16
  at 127.0.0.1:55491; Go 1.27.3 host toolchain):
  - wpgx at 4b29cac: `go build ./...`, `go vet ./...` pass;
    `ENV=test POSTGRES_APPNAME=wpgx POSTGRES_HOST=127.0.0.1 POSTGRES_PORT=55491 go test -race -count=1 -p 1 ./...`
    pass (wpgx, examples, testsuite); `make lint` with golangci-lint v2.14.0
    (CI version, checksum-verified release binary): 0 issues.
  - Mutation checks: starting T before acquire fails
    `TestDBTimeoutStartsAfterAcquire`; reverting the round-1 fixes fails all
    five round-1 regressions without hanging.
  - sqlc and bookstore commands and results: their local records, section 7.
- Cross-agent review: Codex reviewer session
  `5e2002b6-dea5-4c53-96c9-cde8cdd6a84b` (env-woddv6j0), created by this main
  session `425998b5-747f-44a4-b7fe-52d8f4eabf6f` with `--parent`; verified
  agent codex, same environment, `treeParentId` = main session, listed in the
  main session's tree. Reports (sha256):

| Round | wpgx / sqlc / bookstore | Verdict | Report sha256 |
|-------|-------------------------|---------|---------------|
| 1 | 41c11ed / ef2ba2e49 / 5ed193b | changes (R1-01…07) | f6cafc8362049a943f98e83f0c81d81e636bcb40fa709116b0574ea1085d59af |
| 2 | 542c6a2 / 8a232006a / 7c570eb | changes (R2-01…03) | 0c0e6dd4a703a1d73a70db2b473660358baf317862640d064d549f017a2f2bbb |
| 3 | 4b29cac / e002f1d3f / c402ccb | changes (R3-01) | 8fd60f07702aaf3cc42b1eeae4c0c5e9b972e199b1fcab0191969b4a78f74f62 |
| 4 | 4b29cac / e002f1d3f / 53d04b4 | ACCEPT | 2fe72bbdba487d3efb14f48b8728a7d2132c3d612174e727db07dfbafce083a6 |
| 5 (pins) | 4b29cac / 5291d2071 / 89b3fbc | ACCEPT | 8431ffd47f85ff6a3992ac4a6906756c1e70557677a734c937515c09adf3fcbc |

  Dispositions: R1-01/02/03/05/06/07, R2-01/02/03 and R3-01 accepted and fixed
  with regressions (each mutation-checked); R1-04 accepted for the metric and
  for Each, rejected for failing ordinary buffered reads (compatibility with
  the previous `:many` and pgxpool), and the reviewer agreed in round 2. The
  author accepts the same final content. Afterwards only these records changed
  (dates corrected to US Pacific, checklist ticked, sections 7–8).
- PR/CI outcome: see section 8 and the PRs.

## 8. Remaining Work

- Owner: review and merge in order — wpgx PR, tag a wpgx minor release;
  bookstore PR after bumping its wpgx pin to the tag; sqlc PR after bumping the
  testdata pin and the wicked CI bookstore ref to the merged bookstore commit;
  then tag sqlc. The pins currently name the wpgx branch commit
  `v0.5.2-0.20261120053742-4b29cac2136d`.
- Rollout (separate, owner-approved later): upgrade one consumer first —
  worldline, whose queueing is in the app pool — and compare
  `wpgx_timeout_total{phase}` and `wpgx_acquire_wait_milliseconds` on STG.
- Known limitations (unchanged by design): PgBouncer queueing after `Acquire`
  counts as database time; a client timeout does not cancel the server
  statement; dcache with Redis single flight retries an unresponsive Redis
  until the caller's deadline.
