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

### Environment Variables

- `USE_TEST_CONTAINERS=true` - Enable testcontainers mode
- See [testsuite/README.md](testsuite/README.md) for detailed documentation

### CI/CD Integration

Two GitHub Actions workflows are provided:

1. **test-with-containers.yml** - Uses testcontainers (recommended)
2. **go.yml** - Uses GitHub Actions services (traditional)

For more details, see the [TestSuite Documentation](testsuite/README.md).
