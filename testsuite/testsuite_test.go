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
		WPgxTestSuite: sqlsuite.NewWPgxTestSuiteFromEnv("metatestdb", []string{
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
	suite.WPgxTestSuite.LoadState("TestQueryUseLoader.docs.json", loader)

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
	suite.WPgxTestSuite.Golden("docs", dumper)
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
	suite.WPgxTestSuite.LoadState("TestQueryUseLoader.docs.json", loader)

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
	suite.WPgxTestSuite.LoadStateTmpl(
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
