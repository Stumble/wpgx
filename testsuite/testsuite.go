package testsuite

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/template"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nsf/jsondiff"
	"github.com/stretchr/testify/suite"

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
)

var update = flag.Bool("update", false, "update .golden files")

type WPgxTestSuite struct {
	suite.Suite
	Testdb string
	Tables []string
	Config *wpgx.Config
	Pool   *wpgx.Pool
}

// NewWPgxTestSuiteFromEnv @p db is the name of test db and tables are table creation
// SQL statements. DB will be created, so does tables, on SetupTest.
// If you pass different @p db for suites in different packages, you can test them in parallel.
func NewWPgxTestSuiteFromEnv(db string, tables []string) *WPgxTestSuite {
	config := wpgx.ConfigFromEnv()
	config.DBName = db
	return NewWPgxTestSuiteFromConfig(config, db, tables)
}

// NewWPgxTestSuiteFromConfig connect to PostgreSQL Server according to @p config,
// @p db is the name of test db and tables are table creation
// SQL statements. DB will be created, so does tables, on SetupTest.
// If you pass different @p db for suites in different packages, you can test them in parallel.
func NewWPgxTestSuiteFromConfig(config *wpgx.Config, db string, tables []string) *WPgxTestSuite {
	return &WPgxTestSuite{
		Testdb: db,
		Tables: tables,
		Config: config,
	}
}

// GetRawPool returns a raw *pgx.Pool.
func (suite *WPgxTestSuite) GetRawPool() *pgxpool.Pool {
	return suite.Pool.RawPool()
}

// GetPool returns the *wpgx.Pool.
func (suite *WPgxTestSuite) GetPool() *wpgx.Pool {
	return suite.Pool
}

// setup the database to a clean state: tables have been created according to the
// schema, empty.
func (suite *WPgxTestSuite) SetupTest() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if suite.Pool != nil {
		suite.Pool.Close()
	}

	// create DB
	conn, err := pgx.Connect(context.Background(), fmt.Sprintf(
		"postgres://%s:%s@%s:%d",
		suite.Config.Username, suite.Config.Password, suite.Config.Host, suite.Config.Port))
	suite.Require().NoError(err, "failed to connect to pg")
	defer conn.Close(context.Background())
	_, err = conn.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE);", suite.Testdb))
	suite.Require().NoError(err, "failed to drop DB")
	_, err = conn.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s;", suite.Testdb))
	suite.Require().NoError(err, "failed to create DB")

	// create manager
	pool, err := wpgx.NewPool(context.Background(), suite.Config)
	suite.Require().NoError(err, "wgpx NewPool failed")
	suite.Pool = pool
	suite.Require().NoError(suite.Pool.Ping(context.Background()), "wpgx ping failed")

	// create tables
	for _, v := range suite.Tables {
		exec := suite.Pool.WConn()
		_, err := exec.WExec(ctx, "make_table", v)
		suite.Require().NoError(err, "failed to create table when executing: %s", v)
	}
}

func (suite *WPgxTestSuite) TearDownTest() {
	if suite.Pool != nil {
		suite.Pool.Close()
	}
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
	goldenFile := fmt.Sprintf("%s.%s.golden", suite.T().Name(), tableName)
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
	goldenFile := fmt.Sprintf("%s.%s.var.golden", suite.T().Name(), varName)
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
