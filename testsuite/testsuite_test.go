package testsuite_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/stumble/wpgx"
	sqlsuite "github.com/stumble/wpgx/testsuite"
)

type metaTestSuite struct {
	*sqlsuite.WPgxTestSuite
}

type Doc struct {
	Id          int             `json:"id"`
	Rev         float64         `json:"rev"`
	Content     string          `json:"content"`
	CreatedAt   time.Time       `json:"created_at"`
	Description json.RawMessage `json:"description"`
}

// iteratorForBulkInsert implements pgx.CopyFromSource.
type iteratorForBulkInsert struct {
	rows                 []Doc
	skippedFirstNextCall bool
}

func (r *iteratorForBulkInsert) Next() bool {
	if len(r.rows) == 0 {
		return false
	}
	if !r.skippedFirstNextCall {
		r.skippedFirstNextCall = true
		return true
	}
	r.rows = r.rows[1:]
	return len(r.rows) > 0
}

func (r iteratorForBulkInsert) Values() ([]interface{}, error) {
	return []interface{}{
		r.rows[0].Id,
		r.rows[0].Rev,
		r.rows[0].Content,
		r.rows[0].CreatedAt,
		r.rows[0].Description,
	}, nil
}

func (r iteratorForBulkInsert) Err() error {
	return nil
}

type loaderDumper struct {
	exec wpgx.WGConn
}

func (m *loaderDumper) Dump() ([]byte, error) {
	rows, err := m.exec.WQuery(
		context.Background(),
		"dump",
		"SELECT id,rev,content,created_at,description FROM docs")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := make([]Doc, 0)
	for rows.Next() {
		row := Doc{}
		err := rows.Scan(&row.Id, &row.Rev, &row.Content, &row.CreatedAt, &row.Description)
		if err != nil {
			return nil, err
		}
		row.CreatedAt = row.CreatedAt.UTC()
		results = append(results, row)
	}
	bytes, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return nil, err
	}
	return bytes, nil
}

func (m *loaderDumper) Load(data []byte) error {
	docs := make([]Doc, 0)
	err := json.Unmarshal(data, &docs)
	if err != nil {
		return err
	}
	for _, doc := range docs {
		_, err := m.exec.WExec(
			context.Background(),
			"load",
			"INSERT INTO docs (id,rev,content,created_at,description) VALUES ($1,$2,$3,$4,$5)",
			doc.Id, doc.Rev, doc.Content, doc.CreatedAt, doc.Description)
		if err != nil {
			return err
		}
	}
	return nil
}

func NewMetaTestSuite() *metaTestSuite {
	return &metaTestSuite{
		WPgxTestSuite: sqlsuite.NewWPgxTestSuiteFromEnv([]string{
			`CREATE TABLE IF NOT EXISTS docs (
              id          INT NOT NULL,
              rev         DOUBLE PRECISION NOT NULL,
              content     VARCHAR(200) NOT NULL,
              created_at  TIMESTAMPTZ NOT NULL,
              description JSON NOT NULL,
              PRIMARY KEY(id)
            );`,
		}),
	}
}

func TestMetaTestSuite(t *testing.T) {
	suite.Run(t, NewMetaTestSuite())
}

func (suite *metaTestSuite) SetupTest() {
	suite.WPgxTestSuite.SetupTest()
}

func (suite *metaTestSuite) TestInsertQuery() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	exec := suite.Pool.WConn()
	rst, err := exec.WExec(ctx,
		"insert",
		"INSERT INTO docs (id, rev, content, created_at, description) VALUES ($1,$2,$3,$4,$5)",
		33, 666.7777, "hello world", time.Now(), json.RawMessage("{}"))
	suite.Nil(err)
	n := rst.RowsAffected()
	suite.Equal(int64(1), n)

	exec = suite.Pool.WConn()
	rows, err := exec.WQuery(ctx, "select_content",
		"SELECT content FROM docs WHERE id = $1", 33)
	suite.Nil(err)
	defer rows.Close()

	content := ""
	suite.True(rows.Next())
	err = rows.Scan(&content)
	suite.Nil(err)
	suite.Equal("hello world", content)
}

func (suite *metaTestSuite) TestUseWQuerier() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// load state to db from input
	loader := &loaderDumper{exec: suite.Pool.WConn()}
	suite.LoadState("TestQueryUseLoader.docs.json", loader)

	querier, _ := suite.Pool.WQuerier(nil)
	rows, err := querier.WQuery(ctx,
		"select_all",
		"SELECT content, rev, created_at, description FROM docs WHERE id = $1", 33)
	suite.Nil(err)
	defer rows.Close()
}

func (suite *metaTestSuite) TestInsertUseGolden() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	exec := suite.Pool.WConn()
	rst, err := exec.WExec(ctx,
		"insert_data",
		"INSERT INTO docs (id, rev, content, created_at, description) VALUES ($1,$2,$3,$4,$5)",
		33, 666.7777, "hello world", time.Unix(1000, 0), json.RawMessage("{}"))
	suite.Nil(err)
	n := rst.RowsAffected()
	suite.Equal(int64(1), n)
	dumper := &loaderDumper{exec: exec}
	suite.Golden("docs", dumper)
}

func (suite *metaTestSuite) TestGoldenVarJSON() {
	v := struct {
		A string  `json:"a"`
		B int64   `json:"b"`
		C []byte  `json:"c"`
		D float64 `json:"d"`
		F bool    `json:"f"`
	}{
		A: "str",
		B: 666,
		C: []byte("xxxx"),
		D: 1.11,
		F: true,
	}
	suite.GoldenVarJSON("testvar", v)
}

func (suite *metaTestSuite) TestQueryUseLoader() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	exec := suite.Pool.WConn()

	// load state to db from input
	loader := &loaderDumper{exec: exec}
	suite.LoadState("TestQueryUseLoader.docs.json", loader)

	rows, err := exec.WQuery(ctx,
		"select_all",
		"SELECT content, rev, created_at, description FROM docs WHERE id = $1", 33)
	suite.Nil(err)
	defer rows.Close()

	var content string
	var rev float64
	var createdAt time.Time
	var desc json.RawMessage
	var descObj struct {
		GithubURL string `json:"github_url"`
	}
	suite.True(rows.Next())
	err = rows.Scan(&content, &rev, &createdAt, &desc)
	suite.Nil(err)
	suite.Equal("content read from file", content)
	suite.Equal(float64(66.66), rev)
	suite.Equal(int64(1000), createdAt.Unix())
	suite.Require().Nil(err)
	err = json.Unmarshal(desc, &descObj)
	suite.Nil(err)
	suite.Equal(`github.com/stumble/wpgx`, descObj.GithubURL)
}

func (suite *metaTestSuite) TestQueryUseLoadTemplate() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	exec := suite.Pool.WConn()

	// load state to db from input
	now := time.Now()
	loader := &loaderDumper{exec: exec}
	suite.LoadStateTmpl(
		"TestQueryUseLoaderTemplate.docs.json.tmpl", loader, struct {
			Rev       float64
			CreatedAt string
			GithubURL string
		}{
			Rev:       66.66,
			CreatedAt: now.UTC().Format(time.RFC3339),
			GithubURL: "github.com/stumble/wpgx",
		})

	rows, err := exec.WQuery(ctx,
		"select_one",
		"SELECT content, rev, created_at, description FROM docs WHERE id = $1", 33)
	suite.Nil(err)
	defer rows.Close()

	var content string
	var rev float64
	var createdAt time.Time
	var desc json.RawMessage
	var descObj struct {
		GithubURL string `json:"github_url"`
	}
	suite.True(rows.Next())
	err = rows.Scan(&content, &rev, &createdAt, &desc)
	suite.Nil(err)
	suite.Equal("content read from file", content)
	suite.Equal(float64(66.66), rev)
	suite.Equal(now.Unix(), createdAt.Unix())
	suite.Require().Nil(err)
	err = json.Unmarshal(desc, &descObj)
	suite.Nil(err)
	suite.Equal(`github.com/stumble/wpgx`, descObj.GithubURL)
}

func (suite *metaTestSuite) TestCopyFromUseGolden() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	exec := suite.Pool.WConn()
	dumper := &loaderDumper{exec: exec}
	n, err := exec.WCopyFrom(ctx,
		"CopyFrom", []string{"docs"},
		[]string{"id", "rev", "content", "created_at", "description"},
		&iteratorForBulkInsert{rows: []Doc{
			{
				Id:          1,
				Rev:         0.1,
				Content:     "Alice",
				CreatedAt:   time.Unix(0, 1),
				Description: json.RawMessage(`{}`),
			},
			{
				Id:          2,
				Rev:         0.2,
				Content:     "Bob",
				CreatedAt:   time.Unix(100, 0),
				Description: json.RawMessage(`[]`),
			},
			{
				Id:          3,
				Rev:         0.3,
				Content:     "Chris",
				CreatedAt:   time.Unix(1000000, 100),
				Description: json.RawMessage(`{"key":"value"}`),
			},
		}})
	suite.Require().Nil(err)
	suite.Equal(int64(3), n)

	suite.Golden("docs", dumper)
}

// TestGetRawPool tests GetRawPool() method
func (suite *metaTestSuite) TestGetRawPool() {
	rawPool := suite.GetRawPool()
	suite.NotNil(rawPool)
	// Verify it's a valid pool by checking we can ping it
	err := rawPool.Ping(context.Background())
	suite.NoError(err)
}

// TestGetPool tests GetPool() method
func (suite *metaTestSuite) TestGetPool() {
	pool := suite.GetPool()
	suite.NotNil(pool)
	suite.Equal(suite.Pool, pool)
}

// TestDumpState tests DumpState() method which uses writeFile() internally
func (suite *metaTestSuite) TestDumpState() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	exec := suite.Pool.WConn()

	// Clear any existing data first
	_, err := exec.WExec(ctx, "delete_for_dump",
		"DELETE FROM docs WHERE id = $1", 99)
	suite.Require().NoError(err)

	// Insert some test data
	_, err = exec.WExec(ctx,
		"insert_for_dump",
		"INSERT INTO docs (id, rev, content, created_at, description) VALUES ($1,$2,$3,$4,$5)",
		99, 123.456, "test dump", time.Unix(2000, 0), json.RawMessage(`{"test":true}`))
	suite.Require().NoError(err)

	// Dump state to file
	dumper := &loaderDumper{exec: exec}
	suite.DumpState("TestDumpState.docs.json", dumper)

	// Verify file was created by loading it back (but clear first)
	_, err = exec.WExec(ctx, "delete_before_load",
		"DELETE FROM docs WHERE id = $1", 99)
	suite.Require().NoError(err)

	loader := &loaderDumper{exec: exec}
	suite.LoadState("TestDumpState.docs.json", loader)

	// Verify data was loaded correctly
	rows, err := exec.WQuery(ctx, "verify_dump",
		"SELECT id, content FROM docs WHERE id = $1", 99)
	suite.Require().NoError(err)
	defer rows.Close()
	suite.True(rows.Next())
	var id int
	var content string
	err = rows.Scan(&id, &content)
	suite.NoError(err)
	suite.Equal(99, id)
	suite.Equal("test dump", content)
}

// containerTestSuite tests the container-based setup path
type containerTestSuite struct {
	*sqlsuite.WPgxTestSuite
}

func NewContainerTestSuite() *containerTestSuite {
	// use default tc mode.
	return &containerTestSuite{
		WPgxTestSuite: sqlsuite.NewWPgxTestSuiteTcDefault([]string{
			`CREATE TABLE IF NOT EXISTS test_table (
				id INT NOT NULL PRIMARY KEY,
				name VARCHAR(100) NOT NULL
			);`,
		}),
	}
}

func TestContainerTestSuite(t *testing.T) {
	suite.Run(t, NewContainerTestSuite())
}

func (suite *containerTestSuite) SetupTest() {
	suite.WPgxTestSuite.SetupTest()
}

// TestContainerSetup tests setupWithContainer() path
func (suite *containerTestSuite) TestContainerSetup() {
	// Verify pool is set up correctly
	suite.NotNil(suite.Pool)
	err := suite.Pool.Ping(context.Background())
	suite.NoError(err)

	// Verify we can use the pool
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	exec := suite.Pool.WConn()
	_, err = exec.WExec(ctx, "test_insert",
		"INSERT INTO test_table (id, name) VALUES ($1, $2)", 1, "test")
	suite.NoError(err)
}

// TestContainerTeardown tests TearDownTest() container cleanup path
func (suite *containerTestSuite) TestContainerTeardown() {
	// Setup creates a container
	suite.NotNil(suite.Pool)

	// TearDown should clean up the container without panicking
	// This verifies the container cleanup path in TearDownTest()
	// Note: Pool.Close() is called but Pool is not set to nil, so we just verify no panic
	suite.TearDownTest()

	// Verify teardown completed successfully (no panic)
	// The pool is closed but not set to nil in TearDownTest
	suite.NotPanics(func() {
		suite.TearDownTest()
	})
}

// TestTearDownTestErrorHandling tests the error handling path in TearDownTest
// This tests the case where container termination might fail (line 199-200)
func (suite *containerTestSuite) TestTearDownTestErrorHandling() {
	// Setup creates a container
	suite.NotNil(suite.Pool)

	// First teardown should succeed
	suite.TearDownTest()

	// Second teardown should handle the case where container is already nil
	// This tests the if suite.postgresContainer != nil check
	suite.NotPanics(func() {
		suite.TearDownTest()
	})

	// Also test the case where Pool is nil
	suite.Pool = nil
	suite.NotPanics(func() {
		suite.TearDownTest()
	})
}

// dataIsolationTestSuite tests that data doesn't leak between tests
// Each test in this suite inserts data with a specific ID and verifies
// that no other data exists, proving database isolation between tests
type dataIsolationTestSuite struct {
	*sqlsuite.WPgxTestSuite
}

func NewDataIsolationTestSuite() *dataIsolationTestSuite {
	return &dataIsolationTestSuite{
		WPgxTestSuite: sqlsuite.NewWPgxTestSuiteTcDefault([]string{
			`CREATE TABLE IF NOT EXISTS isolation_test (
				id INT NOT NULL PRIMARY KEY,
				test_name VARCHAR(200) NOT NULL,
				value VARCHAR(200) NOT NULL
			);`,
		}),
	}
}

func TestDataIsolationTestSuite(t *testing.T) {
	suite.Run(t, NewDataIsolationTestSuite())
}

func (suite *dataIsolationTestSuite) SetupTest() {
	suite.WPgxTestSuite.SetupTest()
}

// TestIsolation_FirstTest inserts data with ID 1
// This test should never see data from other tests
func (suite *dataIsolationTestSuite) TestIsolation_FirstTest() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	exec := suite.Pool.WConn()

	// Verify table is empty (no leftover data)
	rows, err := exec.WQuery(ctx, "count_all", "SELECT COUNT(*) FROM isolation_test")
	suite.Require().NoError(err)
	defer rows.Close()
	suite.True(rows.Next())
	var count int
	err = rows.Scan(&count)
	suite.NoError(err)
	suite.Equal(0, count, "Table should be empty at start of test - found leftover data!")

	// Insert data for this test
	_, err = exec.WExec(ctx, "insert_test1",
		"INSERT INTO isolation_test (id, test_name, value) VALUES ($1, $2, $3)",
		1, "TestIsolation_FirstTest", "first_value")
	suite.NoError(err)

	// Verify only our data exists
	rows2, err := exec.WQuery(ctx, "verify_only_our_data",
		"SELECT COUNT(*) FROM isolation_test WHERE id = 1")
	suite.Require().NoError(err)
	defer rows2.Close()
	suite.True(rows2.Next())
	err = rows2.Scan(&count)
	suite.NoError(err)
	suite.Equal(1, count)
}

// TestIsolation_SecondTest inserts data with ID 2
// This test should never see data from TestIsolation_FirstTest
func (suite *dataIsolationTestSuite) TestIsolation_SecondTest() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	exec := suite.Pool.WConn()

	// Verify table is empty (no leftover data from TestIsolation_FirstTest)
	rows, err := exec.WQuery(ctx, "count_all", "SELECT COUNT(*) FROM isolation_test")
	suite.Require().NoError(err)
	defer rows.Close()
	suite.True(rows.Next())
	var count int
	err = rows.Scan(&count)
	suite.NoError(err)
	suite.Equal(0, count, "Table should be empty - no data should leak from other tests!")

	// Specifically verify data from FirstTest doesn't exist
	rows2, err := exec.WQuery(ctx, "check_id_1",
		"SELECT COUNT(*) FROM isolation_test WHERE id = 1")
	suite.Require().NoError(err)
	defer rows2.Close()
	suite.True(rows2.Next())
	err = rows2.Scan(&count)
	suite.NoError(err)
	suite.Equal(0, count, "Data from TestIsolation_FirstTest should not exist!")

	// Insert data for this test
	_, err = exec.WExec(ctx, "insert_test2",
		"INSERT INTO isolation_test (id, test_name, value) VALUES ($1, $2, $3)",
		2, "TestIsolation_SecondTest", "second_value")
	suite.NoError(err)
}

// TestIsolation_ThirdTest inserts data with ID 3
// This test should never see data from other tests
func (suite *dataIsolationTestSuite) TestIsolation_ThirdTest() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	exec := suite.Pool.WConn()

	// Verify table is empty (no leftover data)
	rows, err := exec.WQuery(ctx, "count_all", "SELECT COUNT(*) FROM isolation_test")
	suite.Require().NoError(err)
	defer rows.Close()
	suite.True(rows.Next())
	var count int
	err = rows.Scan(&count)
	suite.NoError(err)
	suite.Equal(0, count, "Table should be empty - database should be recreated for each test!")

	// Verify no data from previous tests exists
	rows2, err := exec.WQuery(ctx, "check_no_previous_data",
		"SELECT COUNT(*) FROM isolation_test WHERE id IN (1, 2)")
	suite.Require().NoError(err)
	defer rows2.Close()
	suite.True(rows2.Next())
	err = rows2.Scan(&count)
	suite.NoError(err)
	suite.Equal(0, count, "No data from previous tests should exist!")

	// Insert multiple rows for this test
	_, err = exec.WExec(ctx, "insert_test3_a",
		"INSERT INTO isolation_test (id, test_name, value) VALUES ($1, $2, $3)",
		3, "TestIsolation_ThirdTest", "third_value_a")
	suite.NoError(err)

	_, err = exec.WExec(ctx, "insert_test3_b",
		"INSERT INTO isolation_test (id, test_name, value) VALUES ($1, $2, $3)",
		4, "TestIsolation_ThirdTest", "third_value_b")
	suite.NoError(err)

	// Verify we have exactly 2 rows
	rows3, err := exec.WQuery(ctx, "count_our_data",
		"SELECT COUNT(*) FROM isolation_test")
	suite.Require().NoError(err)
	defer rows3.Close()
	suite.True(rows3.Next())
	err = rows3.Scan(&count)
	suite.NoError(err)
	suite.Equal(2, count, "Should have exactly 2 rows from this test only")
}

// TestIsolation_VerifyCleanState verifies that even after multiple tests,
// the database is still in a clean state
func (suite *dataIsolationTestSuite) TestIsolation_VerifyCleanState() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	exec := suite.Pool.WConn()

	// Verify table is empty
	rows, err := exec.WQuery(ctx, "count_all", "SELECT COUNT(*) FROM isolation_test")
	suite.Require().NoError(err)
	defer rows.Close()
	suite.True(rows.Next())
	var count int
	err = rows.Scan(&count)
	suite.NoError(err)
	suite.Equal(0, count, "Table should be empty - all previous test data should be gone!")

	// Verify no rows exist at all
	rows2, err := exec.WQuery(ctx, "select_all", "SELECT id FROM isolation_test")
	suite.Require().NoError(err)
	defer rows2.Close()
	suite.False(rows2.Next(), "No rows should exist in the table")
}

// TestIsolation_VerifySchemaPresent ensures that while data is cleared,
// the schema (tables) is properly recreated
func (suite *dataIsolationTestSuite) TestIsolation_VerifySchemaPresent() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	exec := suite.Pool.WConn()

	// Verify the table exists and has the correct structure
	rows, err := exec.WQuery(ctx, "check_schema",
		`SELECT column_name, data_type 
		 FROM information_schema.columns 
		 WHERE table_name = 'isolation_test' 
		 ORDER BY ordinal_position`)
	suite.Require().NoError(err)
	defer rows.Close()

	expectedColumns := []struct {
		name string
		typ  string
	}{
		{"id", "integer"},
		{"test_name", "character varying"},
		{"value", "character varying"},
	}

	i := 0
	for rows.Next() {
		var colName, dataType string
		err = rows.Scan(&colName, &dataType)
		suite.NoError(err)
		suite.Less(i, len(expectedColumns), "More columns than expected")
		suite.Equal(expectedColumns[i].name, colName)
		suite.Equal(expectedColumns[i].typ, dataType)
		i++
	}
	suite.Equal(len(expectedColumns), i, "Schema should have all expected columns")
}

// testIsolationSuite tests the NewIsolation functionality
type testIsolationSuite struct {
	*sqlsuite.WPgxTestSuite
}

func NewTestIsolationSuite() *testIsolationSuite {
	return &testIsolationSuite{
		WPgxTestSuite: sqlsuite.NewWPgxTestSuiteTcDefault([]string{
			`CREATE TABLE IF NOT EXISTS isolation_demo (
				id INT NOT NULL PRIMARY KEY,
				value VARCHAR(200) NOT NULL
			);`,
		}),
	}
}

func TestIsolationSuite(t *testing.T) {
	suite.Run(t, NewTestIsolationSuite())
}

func (suite *testIsolationSuite) SetupTest() {
	suite.WPgxTestSuite.SetupTest()
}

// TestNewIsolation_Basic tests basic NewIsolation functionality
func (suite *testIsolationSuite) TestNewIsolation_Basic() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a new isolation
	isolation, err := suite.NewIsolation(ctx)
	suite.Require().NoError(err)
	suite.NotNil(isolation)
	defer isolation.Close()

	// Verify we can create a pool for the isolated database
	pool, err := isolation.NewPool(ctx)
	suite.Require().NoError(err)
	suite.NotNil(pool)
	defer pool.Close()

	// Verify we can ping the pool
	err = pool.Ping(ctx)
	suite.NoError(err)

	// Verify table exists by inserting data
	exec := pool.WConn()
	_, err = exec.WExec(ctx, "insert_test",
		"INSERT INTO isolation_demo (id, value) VALUES ($1, $2)", 1, "test_value")
	suite.NoError(err)

	// Query the data back
	rows, err := exec.WQuery(ctx, "select_test",
		"SELECT value FROM isolation_demo WHERE id = $1", 1)
	suite.Require().NoError(err)
	defer rows.Close()

	suite.True(rows.Next())
	var value string
	err = rows.Scan(&value)
	suite.NoError(err)
	suite.Equal("test_value", value)
}

// TestNewIsolation_MultipleIsolations tests that multiple isolations can coexist
func (suite *testIsolationSuite) TestNewIsolation_MultipleIsolations() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create two isolations
	isolation1, err := suite.NewIsolation(ctx)
	suite.Require().NoError(err)
	suite.NotNil(isolation1)
	defer isolation1.Close()

	isolation2, err := suite.NewIsolation(ctx)
	suite.Require().NoError(err)
	suite.NotNil(isolation2)
	defer isolation2.Close()

	// Verify they have different database names
	config1 := isolation1.GetConfig()
	config2 := isolation2.GetConfig()
	suite.NotEqual(config1.DBName, config2.DBName)

	// Create pools for both
	pool1, err := isolation1.NewPool(ctx)
	suite.Require().NoError(err)
	defer pool1.Close()

	pool2, err := isolation2.NewPool(ctx)
	suite.Require().NoError(err)
	defer pool2.Close()

	// Insert different data in each
	exec1 := pool1.WConn()
	_, err = exec1.WExec(ctx, "insert_iso1",
		"INSERT INTO isolation_demo (id, value) VALUES ($1, $2)", 1, "isolation1_value")
	suite.NoError(err)

	exec2 := pool2.WConn()
	_, err = exec2.WExec(ctx, "insert_iso2",
		"INSERT INTO isolation_demo (id, value) VALUES ($1, $2)", 1, "isolation2_value")
	suite.NoError(err)

	// Verify data is isolated
	rows1, err := exec1.WQuery(ctx, "select_iso1",
		"SELECT value FROM isolation_demo WHERE id = $1", 1)
	suite.Require().NoError(err)
	defer rows1.Close()

	suite.True(rows1.Next())
	var value1 string
	err = rows1.Scan(&value1)
	suite.NoError(err)
	suite.Equal("isolation1_value", value1)

	rows2, err := exec2.WQuery(ctx, "select_iso2",
		"SELECT value FROM isolation_demo WHERE id = $1", 1)
	suite.Require().NoError(err)
	defer rows2.Close()

	suite.True(rows2.Next())
	var value2 string
	err = rows2.Scan(&value2)
	suite.NoError(err)
	suite.Equal("isolation2_value", value2)
}

// TestNewIsolation_Cleanup tests that cleanup works properly
func (suite *testIsolationSuite) TestNewIsolation_Cleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create an isolation
	isolation, err := suite.NewIsolation(ctx)
	suite.Require().NoError(err)
	suite.NotNil(isolation)

	// Get the database name
	dbName := isolation.GetConfig().DBName

	// Create a pool and verify it works
	pool, err := isolation.NewPool(ctx)
	suite.Require().NoError(err)

	// Insert some data to verify database exists
	exec := pool.WConn()
	_, err = exec.WExec(ctx, "insert_before_cleanup",
		"INSERT INTO isolation_demo (id, value) VALUES ($1, $2)", 1, "test")
	suite.NoError(err)
	pool.Close()

	// Close the isolation (this should drop the database)
	isolation.Close()

	// Verify the database no longer exists by querying pg_database
	mainExec := suite.Pool.WConn()
	rows, err := mainExec.WQuery(ctx, "check_db_exists",
		"SELECT COUNT(*) FROM pg_database WHERE datname = $1", dbName)
	suite.Require().NoError(err)
	defer rows.Close()

	suite.True(rows.Next())
	var count int
	err = rows.Scan(&count)
	suite.NoError(err)
	suite.Equal(0, count, "Database %s should be dropped after Close()", dbName)
}

// TestNewIsolation_MainSuiteUnaffected verifies that the main suite database is not affected
func (suite *testIsolationSuite) TestNewIsolation_MainSuiteUnaffected() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Insert data in main suite database
	exec := suite.Pool.WConn()
	_, err := exec.WExec(ctx, "insert_main",
		"INSERT INTO isolation_demo (id, value) VALUES ($1, $2)", 1, "main_suite_value")
	suite.Require().NoError(err)

	// Create an isolation
	isolation, err := suite.NewIsolation(ctx)
	suite.Require().NoError(err)
	suite.NotNil(isolation)
	defer isolation.Close()

	// Create pool for isolation
	isolatedPool, err := isolation.NewPool(ctx)
	suite.Require().NoError(err)
	defer isolatedPool.Close()

	// Verify isolation database is empty
	isolatedExec := isolatedPool.WConn()
	rows, err := isolatedExec.WQuery(ctx, "count_isolated",
		"SELECT COUNT(*) FROM isolation_demo")
	suite.Require().NoError(err)
	defer rows.Close()
	suite.True(rows.Next())
	var count int
	err = rows.Scan(&count)
	suite.NoError(err)
	suite.Equal(0, count, "Isolated database should be empty")

	// Verify main suite database still has data
	rows2, err := exec.WQuery(ctx, "verify_main",
		"SELECT value FROM isolation_demo WHERE id = $1", 1)
	suite.Require().NoError(err)
	defer rows2.Close()
	suite.True(rows2.Next())
	var value string
	err = rows2.Scan(&value)
	suite.NoError(err)
	suite.Equal("main_suite_value", value)
}
