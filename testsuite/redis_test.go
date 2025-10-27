package testsuite_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/stumble/wpgx"
	sqlsuite "github.com/stumble/wpgx/testsuite"
)

type redisTestSuite struct {
	*sqlsuite.WPgxTestSuite
}

func NewRedisTestSuite() *redisTestSuite {
	config := &wpgx.Config{
		Username:         "postgres",
		Password:         "my-secret",
		Host:             "localhost",
		Port:             5432,
		DBName:           "redistestdb",
		MaxConns:         100,
		MinConns:         0,
		MaxConnLifetime:  6 * time.Hour,
		MaxConnIdleTime:  1 * time.Minute,
		EnablePrometheus: true,
		EnableTracing:    true,
		AppName:          "redis_test",
		Redis: wpgx.RedisConfig{
			Host:         "localhost",
			Port:         6379,
			Password:     "",
			DB:           1, // 使用不同的数据库避免冲突
			MaxRetries:   3,
			PoolSize:     10,
			MinIdleConns: 5,
			PoolTimeout:  4 * time.Second,
		},
	}

	return &redisTestSuite{
		WPgxTestSuite: sqlsuite.NewWPgxTestSuiteFromConfig(config, "redistestdb", []string{
			`CREATE TABLE IF NOT EXISTS users (
               id          SERIAL PRIMARY KEY,
               name        VARCHAR(100) NOT NULL,
               email       VARCHAR(100) NOT NULL,
               created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
             );`,
		}),
	}
}

func TestRedisTestSuite(t *testing.T) {
	suite.Run(t, NewRedisTestSuite())
}

func (suite *redisTestSuite) SetupTest() {
	suite.WPgxTestSuite.SetupTest()
	// 清理Redis数据库
	suite.ClearRedis(context.Background())
}

func (suite *redisTestSuite) TestRedisBasicOperations() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	redis := suite.GetRedis()
	suite.Require().NotNil(redis, "Redis client should not be nil")

	// 测试基本的字符串操作
	err := redis.Set(ctx, "test_key", "test_value", 0).Err()
	suite.NoError(err, "Set operation should succeed")

	val, err := redis.Get(ctx, "test_key").Result()
	suite.NoError(err, "Get operation should succeed")
	suite.Equal("test_value", val, "Retrieved value should match")

	// 测试过期时间
	err = redis.Set(ctx, "expire_key", "expire_value", 1*time.Second).Err()
	suite.NoError(err, "Set with expiration should succeed")

	val, err = redis.Get(ctx, "expire_key").Result()
	suite.NoError(err, "Get before expiration should succeed")
	suite.Equal("expire_value", val, "Value should be correct before expiration")

	// 等待过期
	time.Sleep(2 * time.Second)
	_, err = redis.Get(ctx, "expire_key").Result()
	suite.Error(err, "Key should be expired")
}

func (suite *redisTestSuite) TestRedisHashOperations() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	redis := suite.GetRedis()

	// 测试Hash操作
	userData := map[string]interface{}{
		"name":  "张三",
		"email": "zhangsan@example.com",
		"age":   "25",
	}

	err := redis.HMSet(ctx, "user:1", userData).Err()
	suite.NoError(err, "HMSet operation should succeed")

	// 获取单个字段
	name, err := redis.HGet(ctx, "user:1", "name").Result()
	suite.NoError(err, "HGet operation should succeed")
	suite.Equal("张三", name, "Name should match")

	// 获取所有字段
	allFields, err := redis.HGetAll(ctx, "user:1").Result()
	suite.NoError(err, "HGetAll operation should succeed")
	suite.Equal("张三", allFields["name"], "Name in all fields should match")
	suite.Equal("zhangsan@example.com", allFields["email"], "Email should match")
	suite.Equal("25", allFields["age"], "Age should match")
}

func (suite *redisTestSuite) TestRedisListOperations() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	redis := suite.GetRedis()

	// 测试List操作
	listKey := "test_list"

	// 从右侧推入元素
	err := redis.RPush(ctx, listKey, "item1", "item2", "item3").Err()
	suite.NoError(err, "RPush operation should succeed")

	// 获取列表长度
	length, err := redis.LLen(ctx, listKey).Result()
	suite.NoError(err, "LLen operation should succeed")
	suite.Equal(int64(3), length, "List length should be 3")

	// 获取所有元素
	items, err := redis.LRange(ctx, listKey, 0, -1).Result()
	suite.NoError(err, "LRange operation should succeed")
	suite.Equal([]string{"item1", "item2", "item3"}, items, "List items should match")

	// 从左侧弹出元素
	item, err := redis.LPop(ctx, listKey).Result()
	suite.NoError(err, "LPop operation should succeed")
	suite.Equal("item1", item, "Popped item should be correct")
}

func (suite *redisTestSuite) TestRedisSetOperations() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	redis := suite.GetRedis()

	// 测试Set操作
	setKey := "test_set"

	// 添加元素到集合
	err := redis.SAdd(ctx, setKey, "member1", "member2", "member3").Err()
	suite.NoError(err, "SAdd operation should succeed")

	// 检查成员是否存在
	exists, err := redis.SIsMember(ctx, setKey, "member1").Result()
	suite.NoError(err, "SIsMember operation should succeed")
	suite.True(exists, "Member should exist")

	// 获取集合大小
	size, err := redis.SCard(ctx, setKey).Result()
	suite.NoError(err, "SCard operation should succeed")
	suite.Equal(int64(3), size, "Set size should be 3")

	// 获取所有成员
	members, err := redis.SMembers(ctx, setKey).Result()
	suite.NoError(err, "SMembers operation should succeed")
	suite.Len(members, 3, "Should have 3 members")
}

func (suite *redisTestSuite) TestRedisWithDatabaseIntegration() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	redis := suite.GetRedis()
	pool := suite.GetPool()

	// 在数据库中插入用户数据
	exec := pool.WConn()
	_, err := exec.WExec(ctx, "insert_user",
		"INSERT INTO users (name, email) VALUES ($1, $2)",
		"李四", "lisi@example.com")
	suite.NoError(err, "Database insert should succeed")

	// 将用户ID缓存到Redis
	userID := 1
	cacheKey := "user_id:lisi@example.com"
	err = redis.Set(ctx, cacheKey, userID, 10*time.Minute).Err()
	suite.NoError(err, "Redis cache should succeed")

	// 从Redis获取缓存的用户ID
	cachedID, err := redis.Get(ctx, cacheKey).Int()
	suite.NoError(err, "Redis get should succeed")
	suite.Equal(userID, cachedID, "Cached ID should match")

	// 使用缓存的ID查询数据库
	rows, err := exec.WQuery(ctx, "get_user",
		"SELECT name, email FROM users WHERE id = $1", cachedID)
	suite.NoError(err, "Database query should succeed")
	defer rows.Close()

	suite.True(rows.Next(), "Should have a row")
	var name, email string
	err = rows.Scan(&name, &email)
	suite.NoError(err, "Row scan should succeed")
	suite.Equal("李四", name, "Name should match")
	suite.Equal("lisi@example.com", email, "Email should match")
}

func (suite *redisTestSuite) TestRedisPipeline() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	redis := suite.GetRedis()

	// 使用Pipeline批量执行命令
	pipe := redis.Pipeline()

	setCmd1 := pipe.Set(ctx, "key1", "value1", 0)
	setCmd2 := pipe.Set(ctx, "key2", "value2", 0)
	setCmd3 := pipe.Set(ctx, "key3", "value3", 0)

	_, err := pipe.Exec(ctx)
	suite.NoError(err, "Pipeline execution should succeed")

	// 验证结果
	val1, err := setCmd1.Result()
	suite.NoError(err, "First set command should succeed")
	suite.Equal("OK", val1, "First set should return OK")

	val2, err := setCmd2.Result()
	suite.NoError(err, "Second set command should succeed")
	suite.Equal("OK", val2, "Second set should return OK")

	val3, err := setCmd3.Result()
	suite.NoError(err, "Third set command should succeed")
	suite.Equal("OK", val3, "Third set should return OK")

	// 验证值是否正确设置
	value1, err := redis.Get(ctx, "key1").Result()
	suite.NoError(err, "Get key1 should succeed")
	suite.Equal("value1", value1, "Key1 value should match")

	value2, err := redis.Get(ctx, "key2").Result()
	suite.NoError(err, "Get key2 should succeed")
	suite.Equal("value2", value2, "Key2 value should match")

	value3, err := redis.Get(ctx, "key3").Result()
	suite.NoError(err, "Get key3 should succeed")
	suite.Equal("value3", value3, "Key3 value should match")
}
