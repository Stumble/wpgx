package testsuite

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nsf/jsondiff"
	"github.com/stretchr/testify/suite"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/stumble/wpgx"
)

type Loader interface {
	Load(data []byte) error
}

type Dumper interface {
	Dump() ([]byte, error)
}

const (
	TestDataDirPath = "testdata"

	// Container configuration constants
	DefaultTimeout          = 20 * time.Second
	containerStartupTimeout = 60 * time.Second
	logOccurrenceCount      = 2

	// Default config constants
	DefaultPostgresImage   = "postgres:14.5"
	DefaultUsername        = "postgres"
	DefaultPassword        = "my-secret"
	DefaultHost            = "localhost"
	DefaultPort            = 5432
	DefaultDBName          = "wpgx_test_db"
	DefaultMaxConns        = int32(20)
	DefaultMinConns        = int32(0)
	DefaultMinIdleConns    = int32(0)
	DefaultMaxConnLifetime = 6 * time.Hour
	DefaultMaxConnIdleTime = 1 * time.Minute
	DefaultAppName         = "WPgxTestSuite"

	// Environment variable names
	EnvUseTestContainers = "USE_TEST_CONTAINERS"

	// Golden file extensions
	GoldenFileSuffix    = ".golden"
	VarGoldenFileSuffix = ".var.golden"

	// Connection string format
	PostgresConnStringFormat = "postgres://%s:%s@%s:%d/postgres"
)

var update = flag.Bool("update", false, "update .golden files")

type WPgxTestSuite struct {
	suite.Suite
	Tables            []string
	config            *wpgx.Config
	Pool              *wpgx.Pool
	postgresContainer *postgres.PostgresContainer
	useContainer      bool
}

// NewWPgxTestSuiteFromEnv initialize WPgxTestSuite from environment variables using default wpgx prefix.
// Use environment variable USE_TEST_CONTAINERS=true to enable testcontainers mode.
// Otherwise, it will use direct connection mode (requires a running PostgreSQL instance).
//
// NOTE: if you use the testcontainers mode, the container is started in SetupSuite and terminated in TearDownSuite.
// You MUST use the GetConfig() method to get the updated config with container connection details after SetupSuite.
//
// Deprecated: This function has several limitations:
//  1. When using direct connection mode (no testcontainers), different packages share the same pg and database,
//     which means tests must be run with `go test -count=1 -p 1` to avoid conflicts.
//  2. Managing configs from environment variables is inconvenient - you must either set environment
//     variables before running tests (using default prefix "postgres"). Additionally, when using
//     testcontainers mode, you must remember to call GetConfig() after SetupSuite to get the
//     updated connection details (host/port).
//  3. For new tests, use NewWPgxTestSuiteTcDefault instead, which creates a dedicated PostgreSQL container
//     for each test suite, allowing packages to run in parallel without interference.
//  4. For even more parallelism within a suite, use the NewIsolation() method to create
//     isolated databases for individual tests.
func NewWPgxTestSuiteFromEnv(tables []string) *WPgxTestSuite {
	useContainer := os.Getenv(EnvUseTestContainers) == "true"
	envKey := strings.ToUpper(fmt.Sprintf("%s_%s", wpgx.DefaultEnvPrefix, "APPNAME"))
	if os.Getenv(envKey) == "" {
		err := os.Setenv(envKey, DefaultAppName)
		if err != nil {
			panic(err)
		}
		defer os.Unsetenv(envKey)
	}
	config := wpgx.ConfigFromEnvPrefix(wpgx.DefaultEnvPrefix)
	return NewWPgxTestSuiteFromConfig(config, tables, useContainer)
}

// NewWPgxTestSuiteFromConfig connect to PostgreSQL Server according to @p config,
// tables are table creation SQL statements. config.DBName will be created, so does tables, on SetupTest.
//
// NOTE: if you use the testcontainers mode, the container is started in SetupSuite and terminated in TearDownSuite.
// You MUST use the GetConfig() method to get the updated config with container connection details after SetupSuite.
//
// Deprecated: This function has several limitations:
//  1. When using direct connection mode (no testcontainers), different packages share the same pg and database,
//     which means tests must be run with `go test -count=1 -p 1` to avoid conflicts.
//  2. Managing configs manually requires constructing the entire config object before creating the suite.
//     Additionally, when using testcontainers mode, you must remember to call GetConfig() after
//     SetupSuite to get the updated connection details (host/port), making it error-prone.
//  3. For new tests, use NewWPgxTestSuiteTcDefault instead, which creates a dedicated container
//     for each test suite with sensible defaults, allowing packages to run in parallel without interference.
//  4. For even more parallelism within a suite, use the NewIsolation() method to create
//     isolated databases for individual tests.
func NewWPgxTestSuiteFromConfig(config *wpgx.Config, tables []string, useContainer bool) *WPgxTestSuite {
	return &WPgxTestSuite{
		Tables:       tables,
		config:       config,
		useContainer: useContainer,
	}
}

// NewWPgxTestSuiteTcDefault creates a new WPgxTestSuite with default testcontainers configuration.
// This is the RECOMMENDED way to create a new WPgxTestSuite since v0.4.
//
// Benefits over deprecated NewWPgxTestSuiteFromEnv/NewWPgxTestSuiteFromConfig:
//  1. Each test suite gets its own dedicated PostgreSQL container, allowing packages to run
//     in parallel without conflicts (no need for `go test -count=1 -p 1`).
//  2. No need to manage environment variables or manually construct configs.
//  3. No need to call GetConfig() after SetupSuite - the suite handles everything automatically.
//  4. Sensible defaults are provided for all configuration options.
//
// For even more parallelism within a test suite, use the suite's NewIsolation() method to
// create isolated databases for individual test cases.
//
// To connect to this testsuite from other code, you should use GetConfig() to get the config
// with container connection details after SetupSuite (typically in SetupTest or BeforeTest).
func NewWPgxTestSuiteTcDefault(tables []string) *WPgxTestSuite {
	config := wpgx.Config{
		Username:         DefaultUsername,
		Password:         DefaultPassword,
		Host:             DefaultHost,
		Port:             DefaultPort,
		DBName:           DefaultDBName,
		MaxConns:         DefaultMaxConns,
		MinConns:         DefaultMinConns,
		MinIdleConns:     DefaultMinIdleConns,
		MaxConnLifetime:  DefaultMaxConnLifetime,
		MaxConnIdleTime:  DefaultMaxConnIdleTime,
		IsProxy:          false,
		EnablePrometheus: false,
		EnableTracing:    false,
		AppName:          DefaultAppName,
	}
	return NewWPgxTestSuiteFromConfig(&config, tables, true)
}

// Config returns the updated config. You MUST use this new config to create pools if you
// use the testcontainers mode. The host and port are updated to the container's host and port.
func (suite *WPgxTestSuite) GetConfig() wpgx.Config {
	return *suite.config
}

// GetRawPool returns a raw *pgx.Pool.
func (suite *WPgxTestSuite) GetRawPool() *pgxpool.Pool {
	return suite.Pool.RawPool()
}

// GetPool returns the *wpgx.Pool.
func (suite *WPgxTestSuite) GetPool() *wpgx.Pool {
	return suite.Pool
}

// setupContainer uses testcontainers to start a PostgreSQL container
func (suite *WPgxTestSuite) setupContainer() {
	ctx, cancel := context.WithTimeout(context.Background(), containerStartupTimeout)
	defer cancel()

	// Start PostgreSQL container
	// Use postgres superuser for reliable authentication across all environments.
	// The default testcontainers credentials (test/test) can have authentication issues.
	container, err := postgres.Run(ctx,
		DefaultPostgresImage,
		postgres.WithDatabase(suite.config.DBName),
		postgres.WithUsername(suite.config.Username),
		postgres.WithPassword(suite.config.Password),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(logOccurrenceCount).
				WithStartupTimeout(containerStartupTimeout)),
	)
	suite.Require().NoError(err, "failed to start postgres container")
	suite.postgresContainer = container

	// Update config with container connection details
	host, err := container.Host(ctx)
	suite.Require().NoError(err, "failed to get container host")
	port, err := container.MappedPort(ctx, "5432")
	suite.Require().NoError(err, "failed to get container port")

	suite.config.Host = host
	suite.config.Port = port.Int()
}

// SetupSuite runs once before all tests in the suite.
// For container mode, it starts the PostgreSQL container.
func (suite *WPgxTestSuite) SetupSuite() {
	if suite.useContainer {
		suite.setupContainer()
	}
}

// TearDownSuite runs once after all tests in the suite.
// For container mode, it terminates the PostgreSQL container.
func (suite *WPgxTestSuite) TearDownSuite() {
	if suite.postgresContainer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
		defer cancel()
		if err := suite.postgresContainer.Terminate(ctx); err != nil {
			suite.T().Logf("failed to terminate postgres container: %v", err)
		}
		suite.postgresContainer = nil
	}
}

// SetupTest sets up the database to a clean state: tables have been created according to the
// schema, empty.
func (suite *WPgxTestSuite) SetupTest() {
	if suite.useContainer {
		suite.setupWithContainer()
	} else {
		suite.setupWithDirectConnection()
	}
}

// TearDownTest closes the pool and sets it to nil.
func (suite *WPgxTestSuite) TearDownTest() {
	if suite.Pool != nil {
		suite.Pool.Close()
		suite.Pool = nil
	}
}

// Isolation is a wrapper around a PostgreSQL database that is isolated from the main suite database.
type Isolation struct {
	config wpgx.Config
	close  func()
}

// GetConfig returns the config for the isolated database
func (t *Isolation) GetConfig() wpgx.Config {
	return t.config
}

// Close drops the isolated database
func (t *Isolation) Close() {
	if t.close != nil {
		t.close()
	}
}

// NewPool creates a new pool for the isolated database
func (t *Isolation) NewPool(ctx context.Context) (*wpgx.Pool, error) {
	return wpgx.NewPool(ctx, &t.config)
}

// connectToPostgresDB creates a connection to the postgres database (not a specific database)
// This is used for database management operations like CREATE DATABASE and DROP DATABASE
func (suite *WPgxTestSuite) connectToPostgresDB(ctx context.Context) (*pgx.Conn, error) {
	return pgx.Connect(ctx, fmt.Sprintf(
		PostgresConnStringFormat,
		suite.config.Username, suite.config.Password, suite.config.Host, suite.config.Port))
}

// createDatabase creates a database with the given name using the provided connection
func createDatabase(ctx context.Context, conn *pgx.Conn, dbName string) error {
	dbIdentifier := pgx.Identifier{dbName}
	_, err := conn.Exec(ctx, "CREATE DATABASE "+dbIdentifier.Sanitize())
	return err
}

// dropDatabase drops a database with the given name using the provided connection
func dropDatabase(ctx context.Context, conn *pgx.Conn, dbName string) error {
	dbIdentifier := pgx.Identifier{dbName}
	_, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+dbIdentifier.Sanitize()+" WITH (FORCE)")
	return err
}

// NewIsolation creates a new isolated database within the test suite's PostgreSQL instance.
// This allows individual test cases to run in complete isolation with their own database,
// enabling maximum parallelism within a test suite.
//
// Each isolated database:
//   - Has a unique randomly-generated name (e.g., "wpgx_test_db_iso_a1b2c3d4e5f6g7h8")
//   - Contains all tables defined in the suite's Tables field
//   - Is automatically cleaned up when Close() is called
//   - Is completely independent from other isolated databases and the main suite database
//
// Usage example:
//
//	func (suite *MyTestSuite) TestSomething() {
//	    ctx := context.Background()
//	    isolation, err := suite.NewIsolation(ctx)
//	    suite.Require().NoError(err)
//	    defer isolation.Close()
//
//	    pool, err := isolation.NewPool(ctx)
//	    suite.Require().NoError(err)
//	    defer pool.Close()
//
//	    // Use pool for isolated testing...
//	}
//
// This is particularly useful when:
//   - Running tests in parallel within a suite (t.Parallel())
//   - Tests need complete data isolation from each other
//   - You want to avoid SetupTest/TearDownTest overhead for individual test cases
func (suite *WPgxTestSuite) NewIsolation(ctx context.Context) (*Isolation, error) {
	// Generate a unique database name using random suffix
	randomSuffix := make([]byte, 8)
	if _, err := rand.Read(randomSuffix); err != nil {
		return nil, fmt.Errorf("failed to generate random suffix: %w", err)
	}
	uniqueDBName := fmt.Sprintf("%s_iso_%x", suite.config.DBName, randomSuffix)

	// Connect to postgres database to create the new isolated database
	conn, err := suite.connectToPostgresDB(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to postgres database: %w", err)
	}
	defer conn.Close(ctx)

	// Create the new isolated database
	if err = createDatabase(ctx, conn, uniqueDBName); err != nil {
		return nil, fmt.Errorf("failed to create isolated database %s: %w", uniqueDBName, err)
	}

	// Create a new config for the isolated database
	isolatedConfig := *suite.config
	isolatedConfig.DBName = uniqueDBName

	// Create a pool for the isolated database and create tables
	pool, err := wpgx.NewPool(ctx, &isolatedConfig)
	if err != nil {
		// Cleanup the database if pool creation fails
		if dropErr := dropDatabase(ctx, conn, uniqueDBName); dropErr != nil {
			return nil, fmt.Errorf("failed to cleanup database %s after pool creation failure: %w", uniqueDBName, dropErr)
		}
		return nil, fmt.Errorf("failed to create pool for isolated database: %w", err)
	}

	// Create tables in the isolated database
	for _, tableSQL := range suite.Tables {
		exec := pool.WConn()
		_, err := exec.WExec(ctx, "create_table", tableSQL)
		if err != nil {
			pool.Close()
			if dropErr := dropDatabase(ctx, conn, uniqueDBName); dropErr != nil {
				return nil, fmt.Errorf("failed to cleanup database %s after table creation failure: %w", uniqueDBName, dropErr)
			}
			return nil, fmt.Errorf("failed to create table in isolated database: %w", err)
		}
	}

	pool.Close()

	// Return Isolation with cleanup function
	return &Isolation{
		config: isolatedConfig,
		close: sync.OnceFunc(func() {
			// Create context with timeout for cleanup operations
			cleanupCtx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
			defer cancel()

			// Connect to postgres database to drop the isolated database
			cleanupConn, err := suite.connectToPostgresDB(cleanupCtx)
			if err != nil {
				log.Printf("failed to connect to postgres database for cleanup: %v", err)
				return
			}
			defer cleanupConn.Close(cleanupCtx)

			// Drop the isolated database
			if err = dropDatabase(cleanupCtx, cleanupConn, uniqueDBName); err != nil {
				log.Printf("failed to drop isolated database %s: %v", uniqueDBName, err)
			}
		}),
	}, nil
}

// ensureCleanDatabase drops the database if it exists and creates it again to ensure a clean state
func (suite *WPgxTestSuite) ensureCleanDatabase(ctx context.Context) {
	// Connect to postgres database (not the test database) to drop/create the test database
	conn, err := suite.connectToPostgresDB(ctx)
	suite.Require().NoError(err, "failed to connect to postgres database")
	defer conn.Close(ctx)

	err = dropDatabase(ctx, conn, suite.config.DBName)
	suite.Require().NoError(err, "failed to drop database")
	err = createDatabase(ctx, conn, suite.config.DBName)
	suite.Require().NoError(err, "failed to create database")
}

// setupDatabase creates a clean database, pool, and tables
func (suite *WPgxTestSuite) setupDatabase() {
	if suite.Pool != nil {
		suite.Pool.Close()
	}

	ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
	defer cancel()

	// Ensure database is clean
	suite.ensureCleanDatabase(ctx)

	// Create pool
	pool, err := wpgx.NewPool(ctx, suite.config)
	suite.Require().NoError(err, "wpgx NewPool failed")
	suite.Pool = pool
	suite.Require().NoError(suite.Pool.Ping(ctx), "wpgx ping failed")

	// Create tables
	for _, v := range suite.Tables {
		exec := suite.Pool.WConn()
		_, err := exec.WExec(ctx, "make_table", v)
		suite.Require().NoError(err, "failed to create table when executing: %s", v)
	}
}

// setupWithContainer creates a pool and tables using the existing container
func (suite *WPgxTestSuite) setupWithContainer() {
	suite.Require().NotNil(suite.postgresContainer, "postgres container must be started in SetupSuite before SetupTest")
	suite.setupDatabase()
}

// setupWithDirectConnection uses direct connection to an existing PostgreSQL instance
func (suite *WPgxTestSuite) setupWithDirectConnection() {
	suite.setupDatabase()
}

// load bytes from file
func (suite *WPgxTestSuite) loadFile(file string) []byte {
	suite.Require().FileExists(file)
	f, err := os.Open(file)
	suite.Require().NoError(err, "cannot open %s", file)
	defer f.Close()
	data, err := io.ReadAll(f)
	suite.Require().NoError(err, "ReadAll on file failed: %s", file)
	return data
}

// LoadState load state from the file to DB.
// For example LoadState(ctx, "sample1.input.json") will load (insert) from
// "testdata/sample1.input.json" to table
func (suite *WPgxTestSuite) LoadState(filename string, loader Loader) {
	input := testDirFilePath(filename)
	data := suite.loadFile(input)
	suite.Require().NoError(loader.Load(data), "LoadState failed: %s", filename)
}

// LoadStateTmpl load state go-template from the file to DB.
// For example,
// data := struct{ID int64}{ID:1}
// LoadState(ctx, "sample1.input.json.tmpl", data)
// will load (insert) from "testdata/sample1.input.json.tmpl", execute it with @p data
// and use loader to populate the table.
func (suite *WPgxTestSuite) LoadStateTmpl(filename string, loader Loader, templateData any) {
	inputFile := testDirFilePath(filename)
	tmplData := suite.loadFile(inputFile)
	tmpl, err := template.New(inputFile).Parse(string(tmplData))
	suite.Require().NoError(err, "LoadStateTemplate failed to parse template: %s", filename)
	var data bytes.Buffer
	suite.Require().NoError(tmpl.Execute(&data, templateData),
		"LoadStateTemplate failed to execute template: %s, %+v", filename, templateData)
	suite.Require().NoError(loader.Load(data.Bytes()), "LoadStateT failed to use loader: %s", filename)
}

func (suite *WPgxTestSuite) writeFile(filename string, data []byte) {
	outputFile := testDirFilePath(filename)
	dir, _ := filepath.Split(outputFile)
	suite.Require().NoError(ensureDir(dir), "ensure(Dir) failed: %s", dir)
	f, err := os.Create(outputFile)
	suite.Require().NoError(err, "create file failed: %s", outputFile)
	defer f.Close()
	_, err = f.Write(data)
	suite.Require().NoError(err, "Failed to dump to file: %s", filename)
	suite.Require().NoError(f.Sync())
}

// DumpState dump state to the file.
// For example DumpState(ctx, "sample1.golden.json") will dump (insert) bytes from
// dumper.dump() to "testdata/${suitename}/${filename}".
func (suite *WPgxTestSuite) DumpState(filename string, dumper Dumper) {
	bytes, err := dumper.Dump()
	suite.Require().NoError(err, "Failed to dump: %s", filename)
	suite.writeFile(filename, bytes)
}

// Golden compares db state dumped by @p dumper with the golden file
// {TestName}.{tableName}.golden. For the first time, you can run
// `go test -update` to automatically generate the golden file.
func (suite *WPgxTestSuite) Golden(tableName string, dumper Dumper) {
	goldenFile := fmt.Sprintf("%s.%s"+GoldenFileSuffix, suite.T().Name(), tableName)
	if *update {
		fmt.Printf("Updating golden file: %s\n", goldenFile)
		suite.DumpState(goldenFile, dumper)
		return
	}
	golden := suite.loadFile(testDirFilePath(goldenFile))
	state, err := dumper.Dump()
	suite.Require().NoError(err, "Failed to dump: %s", tableName)
	suite.Equal(string(golden), string(state), diffOutputJSON(golden, state))
}

// GoldenVarJSON compares the JSON string representation of @p v
// with @p varName.golden file with the test case name as prefix:
// {TestName}.{varName}.var.golden. For the first time, you can run
// `go test -update` to automatically generate the golden file.
func (suite *WPgxTestSuite) GoldenVarJSON(varName string, v any) {
	bs, err := json.MarshalIndent(v, "", "  ")
	suite.Require().NoError(err, "Failed to JSON marshal: %s", varName)
	goldenFile := fmt.Sprintf("%s.%s"+VarGoldenFileSuffix, suite.T().Name(), varName)
	if *update {
		fmt.Printf("Updating golden file: %s\n", goldenFile)
		suite.writeFile(goldenFile, bs)
		return
	}
	golden := suite.loadFile(testDirFilePath(goldenFile))
	suite.Equal(string(golden), string(bs), diffOutputJSON(golden, bs))
}

func diffOutputJSON(a []byte, b []byte) string {
	diffOpts := jsondiff.DefaultConsoleOptions()
	_, diffstr := jsondiff.Compare(a, b, &diffOpts)
	return diffstr
}

func testDirFilePath(filename string) string {
	return filepath.Join(TestDataDirPath, filename)
}

func ensureDir(dirName string) error {
	err := os.MkdirAll(dirName, 0700)
	if err == nil || os.IsExist(err) {
		return nil
	} else {
		return err
	}
}
