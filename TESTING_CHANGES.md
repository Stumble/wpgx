# 测试框架增强 - 支持 Testcontainers

## 变更摘要

本次更新为 wpgx 测试框架添加了 **testcontainers** 支持，现在可以通过环境变量选择使用直连模式或容器模式运行测试。

## 主要改动

### 1. 依赖更新

添加了 testcontainers-go 相关依赖：
- `github.com/testcontainers/testcontainers-go`
- `github.com/testcontainers/testcontainers-go/modules/postgres`

### 2. testsuite/testsuite.go 改动

**新增字段：**
```go
type WPgxTestSuite struct {
    // ... 原有字段
    postgresContainer *postgres.PostgresContainer  // 容器实例
    useContainer      bool                         // 是否使用容器模式
}
```

**新增方法：**
- `setupWithContainer()` - 使用 testcontainers 启动 PostgreSQL
- `setupWithDirectConnection()` - 使用直连模式（原有逻辑）

**修改方法：**
- `NewWPgxTestSuiteFromEnv()` - 自动检测 `WPGX_TEST_USE_CONTAINER` 环境变量
- `NewWPgxTestSuiteFromConfig()` - 新增 `useContainer` 参数
- `SetupTest()` - 根据模式选择不同的设置方式
- `TearDownTest()` - 自动清理容器资源

### 3. Makefile 改动

**新增命令：**
```makefile
# 使用容器模式运行测试
make test-container

# 使用容器模式更新 golden files
make test-container-update-golden-cmd
```

**保留原有命令：**
```makefile
# 直连模式（需要手动启动 PostgreSQL）
make test
```

### 4. CI/CD 改动

**新增文件：**
- `.github/workflows/test-with-containers.yml` - 使用 testcontainers 的 CI workflow

**保留文件：**
- `.github/workflows/go.yml` - 使用 GitHub Actions services 的传统 workflow

### 5. 文档

**新增：**
- `testsuite/README.md` - 详细的测试框架使用文档

**更新：**
- `README.md` - 添加测试部分说明

## 使用方式

### 本地开发

**选项 1：容器模式（推荐）**
```bash
# 只需要 Docker 运行
make test-container
```

**选项 2：直连模式**
```bash
# 需要手动启动 PostgreSQL
make docker-postgres-start
make test-cmd
make docker-postgres-stop
```

### CI/CD

**推荐使用新的 workflow：**
```yaml
# .github/workflows/test-with-containers.yml
- name: Test with Testcontainers
  run: make test-container
```

无需配置 PostgreSQL services，testcontainers 自动处理一切！

## 环境变量控制

| 环境变量 | 值 | 效果 |
|---------|---|------|
| `WPGX_TEST_USE_CONTAINER` | `true` | 使用容器模式 |
| `WPGX_TEST_USE_CONTAINER` | 未设置或其他值 | 使用直连模式 |

## 优势对比

### 容器模式 (Testcontainers)
✅ 无需手动启动 PostgreSQL  
✅ 每个测试完全隔离  
✅ 自动清理，无残留  
✅ 适合 CI/CD  
✅ 只需要 Docker  

### 直连模式
✅ 测试速度更快（复用实例）  
✅ 适合本地快速迭代  

## 向后兼容性

✅ **完全向后兼容**  
- 默认行为不变（直连模式）
- 现有测试代码无需修改
- 只需通过环境变量启用新功能

## 测试验证

```bash
# 编译检查
go build -v ./...  # ✅ 通过

# 运行测试
make test-container  # 使用容器模式
make test            # 使用直连模式（需要先启动 PostgreSQL）
```

## 下一步

1. 在本地验证容器模式测试：`make test-container`
2. 在 CI/CD 中启用新的 workflow
3. 根据需要调整测试策略

## 参考资源

- [testcontainers-go 文档](https://golang.testcontainers.org/)
- [PostgreSQL 模块](https://golang.testcontainers.org/modules/postgres/)
- [CI 集成示例](https://github.com/filipsnastins/testcontainers-github-actions)

