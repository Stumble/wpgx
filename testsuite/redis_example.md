# Redis Configuration Usage Examples

## Overview

wpgx testsuite now supports optional Redis configuration, allowing you to use both PostgreSQL and Redis in your tests. Redis configuration is completely optional - if not configured, tests will run with PostgreSQL only.

## Configuration

### Using Docker Container

For testing purposes, you can easily start Redis using Docker:

```bash
# Start Redis container
docker run -d --name redis-test -p 6379:6379 redis:7-alpine

# Or with custom configuration
docker run -d --name redis-test -p 6379:6379 redis:7-alpine redis-server --appendonly yes

# Stop and remove container when done
docker stop redis-test && docker rm redis-test
```

### Environment Variable Configuration

You can configure Redis connection through environment variables:

```bash
# PostgreSQL configuration
export POSTGRES_APPNAME="my_app"
export POSTGRES_HOST="localhost"
export POSTGRES_PORT="5432"
export POSTGRES_USERNAME="postgres"
export POSTGRES_PASSWORD="my-secret"
export POSTGRES_DBNAME="test_db"

# Redis configuration
export POSTGRES_REDIS_HOST="localhost"
export POSTGRES_REDIS_PORT="6379"
export POSTGRES_REDIS_PASSWORD=""
export POSTGRES_REDIS_DB="0"
export POSTGRES_REDIS_POOLSIZE="10"
export POSTGRES_REDIS_MINIDLECONNS="5"
export POSTGRES_REDIS_MAXRETRIES="3"
export POSTGRES_REDIS_POOLTIMEOUT="4s"
```

### Code Configuration

**With Redis (optional):**
```go
config := &wpgx.Config{
    Username:        "postgres",
    Password:        "my-secret",
    Host:            "localhost",
    Port:            5432,
    DBName:          "test_db",
    MaxConns:        100,
    MinConns:        0,
    MaxConnLifetime: 6 * time.Hour,
    MaxConnIdleTime: 1 * time.Minute,
    EnablePrometheus: true,
    EnableTracing:   true,
    AppName:         "test_app",
    Redis: wpgx.RedisConfig{
        Host:         "localhost",
        Port:         6379,
        Password:     "",
        DB:           0,
        MaxRetries:   3,
        PoolSize:     10,
        MinIdleConns: 5,
        PoolTimeout:  4 * time.Second,
    },
}
```

**Without Redis (PostgreSQL only):**
```go
config := &wpgx.Config{
    Username:        "postgres",
    Password:        "my-secret",
    Host:            "localhost",
    Port:            5432,
    DBName:          "test_db",
    MaxConns:        100,
    MinConns:        0,
    MaxConnLifetime: 6 * time.Hour,
    MaxConnIdleTime: 1 * time.Minute,
    EnablePrometheus: true,
    EnableTracing:   true,
    AppName:         "test_app",
    // Redis config is not set (zero values) - Redis will be disabled
    Redis: wpgx.RedisConfig{},
}
```

## Usage Examples

### Using with Docker Compose

For a complete testing environment, you can use Docker Compose to run both PostgreSQL and Redis:

```yaml
# docker-compose.test.yml
services:
  postgres:
    image: postgres:15-alpine
    environment:
      POSTGRES_USER: postgres
      POSTGRES_PASSWORD: my-secret
      POSTGRES_DB: test_db
    ports:
      - "5432:5432"
    volumes:
      - postgres_data:/var/lib/postgresql/data

  redis:
    image: redis:7-alpine
    ports:
      - "6379:6379"
    volumes:
      - redis_data:/data

volumes:
  postgres_data:
  redis_data:
```

```bash
# Start services
docker-compose -f docker-compose.test.yml up -d

# Run tests
POSTGRES_APPNAME=test go test ./...

# Stop services
docker-compose -f docker-compose.test.yml down
```

**Quick Start Script**: You can also use the provided script to start the test environment:

```bash
# Make script executable (first time only)
chmod +x start-test-env.sh

# Start test environment
./start-test-env.sh

# Run tests
POSTGRES_APPNAME=test go test ./...

# Stop environment
docker-compose -f docker-compose.test.yml down
```

### Basic Usage

```go
type myTestSuite struct {
    *sqlsuite.WPgxTestSuite
}

func NewMyTestSuite() *myTestSuite {
    config := &wpgx.Config{
        // ... configuration as shown above
    }
    
    return &myTestSuite{
        WPgxTestSuite: sqlsuite.NewWPgxTestSuiteFromConfig(config, "testdb", []string{
            `CREATE TABLE IF NOT EXISTS users (
               id          SERIAL PRIMARY KEY,
               name        VARCHAR(100) NOT NULL,
               email       VARCHAR(100) NOT NULL
             );`,
        }),
    }
}

func (suite *myTestSuite) TestWithRedis() {
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    // Get Redis client
    redis := suite.GetRedis()
    
    // Basic operations
    err := redis.Set(ctx, "key", "value", 0).Err()
    suite.NoError(err)
    
    val, err := redis.Get(ctx, "key").Result()
    suite.NoError(err)
    suite.Equal("value", val)
    
    // Clear Redis database
    suite.ClearRedis(ctx)
}
```

### Database and Redis Integration

```go
func (suite *myTestSuite) TestDatabaseRedisIntegration() {
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    redis := suite.GetRedis()
    pool := suite.GetPool()

    // Insert data into database
    exec := pool.WConn()
    _, err := exec.WExec(ctx, "insert_user",
        "INSERT INTO users (name, email) VALUES ($1, $2)",
        "John Doe", "john@example.com")
    suite.NoError(err)

    // Cache user ID to Redis
    userID := 1
    cacheKey := "user_id:john@example.com"
    err = redis.Set(ctx, cacheKey, userID, 10*time.Minute).Err()
    suite.NoError(err)

    // Get cached user ID from Redis
    cachedID, err := redis.Get(ctx, cacheKey).Int()
    suite.NoError(err)
    suite.Equal(userID, cachedID)

    // Query database using cached ID
    rows, err := exec.WQuery(ctx, "get_user",
        "SELECT name, email FROM users WHERE id = $1", cachedID)
    suite.NoError(err)
    defer rows.Close()

    suite.True(rows.Next())
    var name, email string
    err = rows.Scan(&name, &email)
    suite.NoError(err)
    suite.Equal("John Doe", name)
    suite.Equal("john@example.com", email)
}
```

## Available Redis Operations

The testsuite provides the following Redis-related methods:

- `GetRedis()` - Get Redis client (returns `nil` if Redis is not configured)
- `ClearRedis(ctx)` - Clear all keys from the current Redis database (does nothing if Redis is not configured)

## Notes

1. **Optional Redis**: Redis configuration is completely optional. If not configured (zero values), tests will run with PostgreSQL only and `GetRedis()` will return `nil`.
2. **Redis Server**: When using Redis, ensure Redis server is running. You can use:
   - Docker: `docker run -d --name redis-test -p 6379:6379 redis:7-alpine`
   - Docker Compose: See the example above
   - Local installation: Install Redis locally and start the service
3. **Test Isolation**: Tests automatically clear Redis database to avoid interference between tests
4. **Database Isolation**: You can isolate different tests by configuring different Redis database numbers
5. **Timeout Control**: All Redis operations support timeout control
6. **Safe Redis Methods**: `GetRedis()` and `ClearRedis()` are safe to call even when Redis is not configured
7. **Container Management**: When using containers, remember to stop them after testing:
   ```bash
   # For Docker
   docker stop redis-test && docker rm redis-test
   
   # For Docker Compose
   docker-compose -f docker-compose.test.yml down
   ```
