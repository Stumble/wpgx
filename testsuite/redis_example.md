# Redis Configuration Usage Examples

## Overview

wpgx testsuite now supports Redis configuration, allowing you to use both PostgreSQL and Redis in your tests.

## Configuration

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

## Usage Examples

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

- `GetRedis()` - Get Redis client
- `ClearRedis(ctx)` - Clear all keys from the current Redis database

## Notes

1. Ensure Redis server is running
2. Tests automatically clear Redis database to avoid interference between tests
3. You can isolate different tests by configuring different Redis database numbers
4. All Redis operations support timeout control
