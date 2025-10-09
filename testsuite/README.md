# WPGX Test Suite

A comprehensive testing framework for PostgreSQL database tests, supporting two testing modes: **Direct Connection** and **Container Mode**.

## Test Modes

### 1. Direct Connection Mode

Connect directly to an existing PostgreSQL instance for testing.

**Advantages:**
- Fast test execution (reuses existing instance)
- Ideal for rapid local development iteration

**Disadvantages:**
- Requires manual PostgreSQL startup
- Requires environment variable configuration
- Weaker test isolation

**Usage:**

```bash
# 1. Start PostgreSQL (using Docker)
make docker-postgres-start

# 2. Run tests
make test-cmd

# 3. Stop PostgreSQL
make docker-postgres-stop

# Or run with automatic start/stop
make test
```

**Required Environment Variables:**
```bash
export PGHOST=localhost
export PGPORT=5432
export PGUSER=postgres
export PGPASSWORD=my-secret
export POSTGRES_APPNAME=wpgx
export ENV=test
```

### 2. Container Mode (Testcontainers) - **Recommended for CI/CD**

Uses [testcontainers-go](https://github.com/testcontainers/testcontainers-go) to automatically manage PostgreSQL containers.

**Advantages:**
- ✅ No manual PostgreSQL startup required
- ✅ Complete test isolation
- ✅ Automatic cleanup, no residuals
- ✅ Perfect for CI/CD environments
- ✅ Only requires Docker, no other dependencies

**Disadvantages:**
- Container startup overhead (first-time image pull)

**Usage:**

```bash
# Set environment variable to enable container mode
export WPGX_TEST_USE_CONTAINER=true

# Run tests
make test-container

# Or run directly (environment variable included in Makefile)
make test-container
```

**Only Docker Required:**
- Ensure Docker daemon is running
- Testcontainers automatically pulls and starts PostgreSQL container
- Automatically cleans up containers after tests

## Usage in Code

### Basic Usage

```go
package mytest

import (
    "testing"
    "github.com/stretchr/testify/suite"
    sqlsuite "github.com/stumble/wpgx/testsuite"
)

type MyTestSuite struct {
    *sqlsuite.WPgxTestSuite
}

func NewMyTestSuite() *MyTestSuite {
    return &MyTestSuite{
        // Automatically selects mode based on WPGX_TEST_USE_CONTAINER environment variable
        WPgxTestSuite: sqlsuite.NewWPgxTestSuiteFromEnv("mytestdb", []string{
            `CREATE TABLE users (
                id INT PRIMARY KEY,
                name VARCHAR(100)
            );`,
        }),
    }
}

func TestMyTestSuite(t *testing.T) {
    suite.Run(t, NewMyTestSuite())
}

func (suite *MyTestSuite) SetupTest() {
    suite.WPgxTestSuite.SetupTest()
}

func (suite *MyTestSuite) TestSomething() {
    // Your test code here
    exec := suite.Pool.WConn()
    // ...
}
```

### Force Specific Mode

If you want to explicitly specify which mode to use in code:

```go
// Force container mode
suite := sqlsuite.NewWPgxTestSuiteFromConfig(
    config, 
    "mytestdb", 
    tables,
    true, // useContainer = true
)

// Force direct connection mode
suite := sqlsuite.NewWPgxTestSuiteFromConfig(
    config, 
    "mytestdb", 
    tables,
    false, // useContainer = false
)
```

## CI/CD Integration

### GitHub Actions

We provide two workflow examples:

#### 1. Using Testcontainers (Recommended)

`.github/workflows/test-with-containers.yml`:
```yaml
- name: Test with Testcontainers
  run: make test-container
```

**Advantages:**
- Simple configuration, no need to define services
- More flexible, easy to switch PostgreSQL versions
- Consistent with local development environment

#### 2. Using GitHub Actions Services (Traditional)

`.github/workflows/go.yml`:
```yaml
services:
  postgres:
    image: postgres:14.5
    env:
      POSTGRES_PASSWORD: my-secret
```

**Use Cases:**
- If you already have existing configuration
- Need multiple services running simultaneously

## Golden File Testing

The test framework supports Golden File mode for snapshot testing:

```go
func (suite *MyTestSuite) TestWithGolden() {
    // ... perform some operations ...
    
    // Compare database state with golden file
    dumper := &myDumper{exec: suite.Pool.WConn()}
    suite.Golden("tablename", dumper)
}

// First run or update golden files
go test -update
```

## Data Loading

### Load from JSON File

```go
func (suite *MyTestSuite) TestLoadData() {
    loader := &myLoader{exec: suite.Pool.WConn()}
    suite.LoadState("testdata.json", loader)
    
    // Test using loaded data
}
```

### Dynamically Generate Data with Templates

```go
func (suite *MyTestSuite) TestLoadTemplate() {
    loader := &myLoader{exec: suite.Pool.WConn()}
    suite.LoadStateTmpl("testdata.json.tmpl", loader, struct{
        UserID int
        Name   string
    }{
        UserID: 123,
        Name:   "Alice",
    })
}
```

## Environment Variables Reference

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `WPGX_TEST_USE_CONTAINER` | Enable container mode | `false` | No |
| `PGHOST` | PostgreSQL host | - | Required for direct mode |
| `PGPORT` | PostgreSQL port | - | Required for direct mode |
| `PGUSER` | PostgreSQL username | - | Required for direct mode |
| `PGPASSWORD` | PostgreSQL password | - | Required for direct mode |
| `POSTGRES_APPNAME` | Application name | - | Optional |
| `ENV` | Environment identifier | - | Optional |

## FAQ

### Q: Tests are slow in container mode?

A: The first run will pull the PostgreSQL image. Subsequent runs will be much faster. You can also pre-pull the image:
```bash
docker pull postgres:14.5
```

### Q: How to use container mode locally?

A: Simply set the environment variable:
```bash
export WPGX_TEST_USE_CONTAINER=true
go test ./...
```

### Q: Which mode is recommended for CI/CD?

A: Container mode (`make test-container`) is recommended - simpler configuration and consistent with local environment.

### Q: Can both modes be used simultaneously?

A: Yes. Control via the `WPGX_TEST_USE_CONTAINER` environment variable. Different test commands can use different modes.

## References

- [testcontainers-go](https://github.com/testcontainers/testcontainers-go)
- [testcontainers-go PostgreSQL Module](https://github.com/testcontainers/testcontainers-go/tree/main/modules/postgres)
- [Testcontainers in CI Pipelines](https://github.com/filipsnastins/testcontainers-github-actions)

