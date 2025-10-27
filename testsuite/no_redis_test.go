package testsuite_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/stumble/wpgx"
	sqlsuite "github.com/stumble/wpgx/testsuite"
)

type noRedisTestSuite struct {
	*sqlsuite.WPgxTestSuite
}

func NewNoRedisTestSuite() *noRedisTestSuite {
	config := &wpgx.Config{
		Username:         "postgres",
		Password:         "my-secret",
		Host:             "localhost",
		Port:             5432,
		DBName:           "noredistestdb",
		MaxConns:         100,
		MinConns:         0,
		MaxConnLifetime:  6 * time.Hour,
		MaxConnIdleTime:  1 * time.Minute,
		EnablePrometheus: true,
		EnableTracing:    true,
		AppName:          "no_redis_test",
		// Redis config is not set (zero values)
		Redis: wpgx.RedisConfig{},
	}

	return &noRedisTestSuite{
		WPgxTestSuite: sqlsuite.NewWPgxTestSuiteFromConfig(config, "noredistestdb", []string{
			`CREATE TABLE IF NOT EXISTS users (
               id          SERIAL PRIMARY KEY,
               name        VARCHAR(100) NOT NULL,
               email       VARCHAR(100) NOT NULL,
               created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
             );`,
		}),
	}
}

func TestNoRedisTestSuite(t *testing.T) {
	suite.Run(t, NewNoRedisTestSuite())
}

func (suite *noRedisTestSuite) SetupTest() {
	suite.WPgxTestSuite.SetupTest()
}

func (suite *noRedisTestSuite) TestWithoutRedis() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Test that Redis client is nil when not configured
	redis := suite.GetRedis()
	suite.Nil(redis, "Redis client should be nil when not configured")

	// Test that ClearRedis does nothing when Redis is not configured
	suite.ClearRedis(ctx) // Should not panic or fail

	// Test that database operations still work
	pool := suite.GetPool()
	exec := pool.WConn()

	_, err := exec.WExec(ctx, "insert_user",
		"INSERT INTO users (name, email) VALUES ($1, $2)",
		"Test User", "test@example.com")
	suite.NoError(err, "Database insert should work without Redis")

	rows, err := exec.WQuery(ctx, "get_user",
		"SELECT name, email FROM users WHERE name = $1", "Test User")
	suite.NoError(err, "Database query should work without Redis")
	defer rows.Close()

	suite.True(rows.Next(), "Should have a row")
	var name, email string
	err = rows.Scan(&name, &email)
	suite.NoError(err, "Row scan should work")
	suite.Equal("Test User", name)
	suite.Equal("test@example.com", email)
}

func (suite *noRedisTestSuite) TestRedisMethodsWithNilClient() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// These should not panic even when Redis is not configured
	redis := suite.GetRedis()
	suite.Nil(redis)

	// ClearRedis should be safe to call
	suite.ClearRedis(ctx)

	// Test that we can check for nil safely
	if redis != nil {
		// This branch should not be taken
		suite.Fail("Redis should be nil")
	}
}
