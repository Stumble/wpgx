package examples

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	sqlsuite "github.com/stumble/wpgx/testsuite"
)

// ExampleTestSuite 展示如何使用 testsuite 框架
type ExampleTestSuite struct {
	*sqlsuite.WPgxTestSuite
}

// NewExampleTestSuite 创建测试套件
// 会根据环境变量 WPGX_TEST_USE_CONTAINER 自动选择：
// - true: 使用 testcontainers 自动启动 PostgreSQL 容器
// - false 或未设置: 使用直连模式（需要预先启动 PostgreSQL）
func NewExampleTestSuite() *ExampleTestSuite {
	return &ExampleTestSuite{
		WPgxTestSuite: sqlsuite.NewWPgxTestSuiteFromEnv("example_test_db", []string{
			`CREATE TABLE IF NOT EXISTS users (
               id          INT PRIMARY KEY,
               name        VARCHAR(100) NOT NULL,
               email       VARCHAR(100) NOT NULL,
               created_at  TIMESTAMPTZ NOT NULL
             );`,
		}),
	}
}

// 运行测试套件
// go test ./examples/... -v                              # 直连模式
// WPGX_TEST_USE_CONTAINER=true go test ./examples/... -v # 容器模式
func TestExampleTestSuite(t *testing.T) {
	suite.Run(t, NewExampleTestSuite())
}

func (suite *ExampleTestSuite) SetupTest() {
	suite.WPgxTestSuite.SetupTest()
}

// TestInsertAndQuery 示例测试：插入和查询数据
func (suite *ExampleTestSuite) TestInsertAndQuery() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 插入数据
	exec := suite.Pool.WConn()
	_, err := exec.WExec(ctx,
		"insert_user",
		"INSERT INTO users (id, name, email, created_at) VALUES ($1, $2, $3, $4)",
		1, "Alice", "alice@example.com", time.Now())
	suite.Require().NoError(err)

	// 查询数据
	rows, err := exec.WQuery(ctx,
		"select_user",
		"SELECT name, email FROM users WHERE id = $1", 1)
	suite.Require().NoError(err)
	defer rows.Close()

	// 验证结果
	suite.True(rows.Next())
	var name, email string
	err = rows.Scan(&name, &email)
	suite.Require().NoError(err)
	suite.Equal("Alice", name)
	suite.Equal("alice@example.com", email)
}

// TestUsingContainerInfo 展示如何在测试中获取连接信息
func (suite *ExampleTestSuite) TestUsingContainerInfo() {
	// 在容器模式下，Config 会被自动更新为容器的连接信息
	suite.T().Logf("PostgreSQL Host: %s", suite.Config.Host)
	suite.T().Logf("PostgreSQL Port: %d", suite.Config.Port)
	suite.T().Logf("Database Name: %s", suite.Config.DBName)

	// 确认可以连接
	err := suite.Pool.Ping(context.Background())
	suite.NoError(err, "should be able to ping database")
}

