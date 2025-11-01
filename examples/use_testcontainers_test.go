package examples

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/stumble/wpgx/testsuite"
)

// ExampleTestSuite demonstrates how to use the testsuite framework
type ExampleTestSuite struct {
	*testsuite.WPgxTestSuite
}

// NewExampleTestSuite creates a test suite using the recommended approach.
// Uses testcontainers to automatically create a dedicated PostgreSQL container for this suite.
// Each suite gets its own container, allowing parallel test execution across packages.
func NewExampleTestSuite() *ExampleTestSuite {
	return &ExampleTestSuite{
		WPgxTestSuite: testsuite.NewWPgxTestSuiteTcDefault([]string{
			`CREATE TABLE IF NOT EXISTS users (
              id          INT PRIMARY KEY,
              name        VARCHAR(100) NOT NULL,
              email       VARCHAR(100) NOT NULL,
              created_at  TIMESTAMPTZ NOT NULL
            );`,
		}),
	}
}

// TestExampleTestSuite runs the test suite with its own dedicated PostgreSQL container.
// Run with: go test ./examples/... -v
func TestExampleTestSuite(t *testing.T) {
	suite.Run(t, NewExampleTestSuite())
}

// func (suite *ExampleTestSuite) Setup(suiteName, testName string) {

func (suite *ExampleTestSuite) SetupTest() {
	suite.WPgxTestSuite.SetupTest()
}

// TestInsertAndQuery demonstrates inserting and querying data
func (suite *ExampleTestSuite) TestInsertAndQuery() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Insert data
	exec := suite.Pool.WConn()
	_, err := exec.WExec(ctx,
		"insert_user",
		"INSERT INTO users (id, name, email, created_at) VALUES ($1, $2, $3, $4)",
		1, "Alice", "alice@example.com", time.Now())
	suite.Require().NoError(err)

	// Query data
	rows, err := exec.WQuery(ctx,
		"select_user",
		"SELECT name, email FROM users WHERE id = $1", 1)
	suite.Require().NoError(err)
	defer rows.Close()

	// Verify results
	suite.True(rows.Next())
	var name, email string
	err = rows.Scan(&name, &email)
	suite.Require().NoError(err)
	suite.Equal("Alice", name)
	suite.Equal("alice@example.com", email)
}

// TestUsingContainerInfo demonstrates how to access connection information in tests
func (suite *ExampleTestSuite) TestUsingContainerInfo() {
	// In container mode, Config is automatically updated with container connection details
	config := suite.GetConfig()
	suite.T().Logf("PostgreSQL Host: %s", config.Host)
	suite.T().Logf("PostgreSQL Port: %d", config.Port)
	suite.T().Logf("Database Name: %s", config.DBName)

	// Verify database connectivity
	err := suite.Pool.Ping(context.Background())
	suite.NoError(err, "should be able to ping database")
}
