# Wrapped pgx

Wrapped pgx is a simple wrap on the PostgreSQL driver library [pgx](https://github.com/jackc/pgx).
It is used by [sqlc](https://github.com/Stumble/sqlc), providing telementry for generated code.

## Components

- **Pool**: a wrapper of pgxpool.Pool. It manages a set of connection pools, including a primary pool 
  and a set of replica pools. We assume replica pools are heterogeneous read-only replicas, meaning
  some replicas can be a partial copy of the primary database, using logical replication.
- **WConn**: a connection wrapper, implementing "WGConn".
- **WTx**: a transaction wrapper, implementing "WGConn".
- **TestSuite**: a comprehensive testing framework for PostgreSQL database tests.

## Database timeouts

A database timeout bounds how long a statement holds a database connection,
not how long it waits for one. sqlc generates `wpgx.WithDBTimeout(ctx, name, T)`
for each query's `timeout` option; hand-written code can do the same:

```go
ctx = wpgx.WithDBTimeout(ctx, "GetBook", 250*time.Millisecond)
row := pool.WConn().WQueryRow(ctx, "GetBook", sql, id)
```

- On a pool connection the statement first waits for a connection, at most the
  larger of `T` and the pool's `AcquireTimeout` (default 2s). A statement in a
  transaction already has its connection.
- `T` then starts and lasts until the connection is free again: `Exec` or
  `CopyFrom` returns, `Row.Scan` returns, or the `Rows` are read to the end or
  closed. Reading rows counts against `T`.
- The context's own deadline and cancellation always apply.
- An expired wait matches `wpgx.ErrAcquireTimeout` and an expired `T` matches
  `wpgx.ErrDBTimeout`; both also match `context.DeadlineExceeded`.
- `Rows` returned under a database timeout have a `Context() context.Context`
  method returning the statement's context.
- The timeout applies only to operations named `name`; nested statements with
  other names are not affected. Without it, operations behave as before.

Pool settings (also on read replicas; environment names with the default
`postgres` prefix):

| Field | Environment | Default | Meaning |
|-------|-------------|---------|---------|
| `AcquireTimeout` | `POSTGRES_ACQUIRETIMEOUT` | `2s` | shortest wait for a connection; zero means the default |
| `MaxAcquireWaiters` | `POSTGRES_MAXACQUIREWAITERS` | `0` | statements that may wait at once; more fail with `ErrTooManyAcquireWaiters`; zero means no cap |

Metrics for statements with a database timeout (existing metrics are unchanged;
`wpgx_latency_milliseconds` still covers the wait):

- `wpgx_acquire_wait_milliseconds{app,replica}`: time waited for a connection.
- `wpgx_db_latency_milliseconds{app,op,replica}`: time the connection was held.
- `wpgx_timeout_total{app,op,replica,phase}`: expired budgets, `phase` is
  `acquire` or `db`.
- `wpgx_conn_pool{op=...}` adds `acquire_waiting` (statements waiting now) and
  the cumulative `acquire_rejected_count`, `empty_acquire_count`,
  `empty_acquire_wait_seconds`, `canceled_acquire_count` and `new_conns_count`.
- Spans record the wait as `wpgx.acquire_wait_ms`.

## Testing

This project includes a powerful test suite framework that supports two testing modes:

### 1. Direct Connection Mode (Traditional)

Connect to an existing PostgreSQL instance:

```bash
# Start PostgreSQL
make docker-postgres-start

# Run tests
make test

# Stop PostgreSQL
make docker-postgres-stop
```

### 2. Testcontainers Mode (Recommended for CI/CD)

Automatically manages PostgreSQL containers using [testcontainers-go](https://github.com/testcontainers/testcontainers-go):

```bash
# Run tests with testcontainers (no manual setup needed!)
make test-container
```

**Benefits:**
- ✅ No manual PostgreSQL setup required
- ✅ Automatic container lifecycle management
- ✅ Perfect test isolation
- ✅ Works seamlessly in CI/CD (GitHub Actions, etc.)
- ✅ Only requires Docker to be running

**PostgreSQL version:** `testsuite.NewWPgxTestSuiteTcDefault` starts
`postgres:14.5` (`testsuite.DefaultPostgresImage`). Test against the major
version your production database runs:

```go
testsuite.NewWPgxTestSuiteTcDefault(tables, testsuite.WithPostgresImage("postgres:18.6"))
```

### Environment Variables

- `USE_TEST_CONTAINERS=true` - Enable testcontainers mode
- See the [`testsuite` package documentation](https://pkg.go.dev/github.com/stumble/wpgx/testsuite)

### CI/CD Integration

Two GitHub Actions workflows are provided:

1. **test-with-containers.yml** - Uses testcontainers (recommended)
2. **go.yml** - Uses GitHub Actions services (traditional)

For more details, see the [`testsuite` package documentation](https://pkg.go.dev/github.com/stumble/wpgx/testsuite).
