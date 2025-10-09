# WPGX Test Suite

这是一个为 PostgreSQL 数据库测试设计的测试框架工具包，支持两种测试模式：**直连模式**和**容器模式**。

## 测试模式

### 1. 直连模式（Direct Connection）

直接连接到已存在的 PostgreSQL 实例进行测试。

**优点：**
- 测试速度快（复用已有实例）
- 适合本地开发快速迭代

**缺点：**
- 需要手动启动 PostgreSQL
- 需要配置环境变量
- 测试隔离性较弱

**使用方法：**

```bash
# 1. 启动 PostgreSQL（使用 Docker）
make docker-postgres-start

# 2. 运行测试
make test-cmd

# 3. 停止 PostgreSQL
make docker-postgres-stop

# 或者一键运行（自动启动和停止）
make test
```

**所需环境变量：**
```bash
export PGHOST=localhost
export PGPORT=5432
export PGUSER=postgres
export PGPASSWORD=my-secret
export POSTGRES_APPNAME=wpgx
export ENV=test
```

### 2. 容器模式（Testcontainers）- **推荐用于 CI/CD**

使用 [testcontainers-go](https://github.com/testcontainers/testcontainers-go) 自动管理 PostgreSQL 容器。

**优点：**
- ✅ 无需手动启动 PostgreSQL
- ✅ 每个测试完全隔离
- ✅ 自动清理，无残留
- ✅ 适合 CI/CD 环境
- ✅ 只需要 Docker，无其他依赖

**缺点：**
- 容器启动有一定开销（首次拉取镜像）

**使用方法：**

```bash
# 设置环境变量启用容器模式
export WPGX_TEST_USE_CONTAINER=true

# 运行测试
make test-container

# 或者直接运行（环境变量已包含在 Makefile 中）
make test-container
```

**仅需 Docker：**
- 确保 Docker daemon 正在运行
- testcontainers 会自动拉取并启动 PostgreSQL 容器
- 测试结束后自动清理容器

## 在代码中使用

### 基本使用

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
        // 会自动根据环境变量 WPGX_TEST_USE_CONTAINER 选择模式
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
    // 你的测试代码
    exec := suite.Pool.WConn()
    // ...
}
```

### 强制指定模式

如果你想在代码中强制指定使用哪种模式：

```go
// 强制使用容器模式
suite := sqlsuite.NewWPgxTestSuiteFromConfig(
    config, 
    "mytestdb", 
    tables,
    true, // useContainer = true
)

// 强制使用直连模式
suite := sqlsuite.NewWPgxTestSuiteFromConfig(
    config, 
    "mytestdb", 
    tables,
    false, // useContainer = false
)
```

## CI/CD 集成

### GitHub Actions

我们提供了两个 workflow 示例：

#### 1. 使用 Testcontainers（推荐）

`.github/workflows/test-with-containers.yml`:
```yaml
- name: Test with Testcontainers
  run: make test-container
```

**优点：**
- 配置简单，无需定义 services
- 更灵活，可以轻松切换 PostgreSQL 版本
- 与本地开发环境一致

#### 2. 使用 GitHub Actions Services（传统方式）

`.github/workflows/go.yml`:
```yaml
services:
  postgres:
    image: postgres:14.5
    env:
      POSTGRES_PASSWORD: my-secret
```

**适用场景：**
- 如果你已经有现成的配置
- 需要多个服务同时运行

## Golden File 测试

测试框架支持 Golden File 模式进行快照测试：

```go
func (suite *MyTestSuite) TestWithGolden() {
    // ... 执行一些操作 ...
    
    // 对比数据库状态与 golden file
    dumper := &myDumper{exec: suite.Pool.WConn()}
    suite.Golden("tablename", dumper)
}

// 首次运行或更新 golden files
go test -update
```

## 数据加载

### 从 JSON 文件加载

```go
func (suite *MyTestSuite) TestLoadData() {
    loader := &myLoader{exec: suite.Pool.WConn()}
    suite.LoadState("testdata.json", loader)
    
    // 测试使用加载的数据
}
```

### 使用模板动态生成数据

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

## 环境变量参考

| 变量名 | 说明 | 默认值 | 必需 |
|--------|------|--------|------|
| `WPGX_TEST_USE_CONTAINER` | 启用容器模式 | `false` | 否 |
| `PGHOST` | PostgreSQL 主机 | - | 直连模式必需 |
| `PGPORT` | PostgreSQL 端口 | - | 直连模式必需 |
| `PGUSER` | PostgreSQL 用户名 | - | 直连模式必需 |
| `PGPASSWORD` | PostgreSQL 密码 | - | 直连模式必需 |
| `POSTGRES_APPNAME` | 应用名称 | - | 可选 |
| `ENV` | 环境标识 | - | 可选 |

## 常见问题

### Q: 容器模式下测试很慢？

A: 首次运行会拉取 PostgreSQL 镜像。之后的运行会快很多。你也可以提前拉取镜像：
```bash
docker pull postgres:14.5
```

### Q: 如何在本地使用容器模式？

A: 只需设置环境变量：
```bash
export WPGX_TEST_USE_CONTAINER=true
go test ./...
```

### Q: CI/CD 中推荐使用哪种模式？

A: 推荐使用容器模式（`make test-container`），配置更简单，与本地环境一致。

### Q: 两种模式可以同时使用吗？

A: 可以。通过环境变量 `WPGX_TEST_USE_CONTAINER` 控制，不同的测试命令可以使用不同的模式。

## 参考资源

- [testcontainers-go](https://github.com/testcontainers/testcontainers-go)
- [testcontainers-go PostgreSQL 模块](https://github.com/testcontainers/testcontainers-go/tree/main/modules/postgres)
- [Testcontainers in CI Pipelines](https://github.com/filipsnastins/testcontainers-github-actions)

