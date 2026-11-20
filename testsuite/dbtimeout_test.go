package testsuite_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/suite"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/stumble/wpgx"
	sqlsuite "github.com/stumble/wpgx/testsuite"
)

// dbTimeoutTestSuite checks statements run with wpgx.WithDBTimeout against
// PostgreSQL. Each test uses its own single-connection pool, so holding that
// connection makes a statement wait deterministically.
type dbTimeoutTestSuite struct {
	*sqlsuite.WPgxTestSuite
}

func TestDBTimeoutTestSuite(t *testing.T) {
	suite.Run(t, &dbTimeoutTestSuite{
		WPgxTestSuite: sqlsuite.NewWPgxTestSuiteFromEnv([]string{
			`CREATE TABLE IF NOT EXISTS budget_items (id INT PRIMARY KEY);`,
		}),
	})
}

type poolOption func(*wpgx.Config)

// newPool returns a one-connection pool that is closed when the test ends,
// after checking that no connection is left acquired.
func (suite *dbTimeoutTestSuite) newPool(opts ...poolOption) *wpgx.Pool {
	config := suite.GetConfig()
	config.MaxConns = 1
	config.MinConns = 0
	config.MinIdleConns = 0
	config.EnablePrometheus = false
	config.EnableTracing = false
	for _, opt := range opts {
		opt(&config)
	}
	pool, err := wpgx.NewPool(context.Background(), &config)
	suite.Require().NoError(err)
	suite.T().Cleanup(func() {
		suite.Eventually(func() bool { return pool.RawPrimaryPool().Stat().AcquiredConns() == 0 },
			5*time.Second, 10*time.Millisecond, "a statement left its connection acquired")
		pool.Close()
	})
	return pool
}

// hold acquires the pool's only connection and returns its release function.
func (suite *dbTimeoutTestSuite) hold(pool *wpgx.Pool) func() {
	conn, err := pool.RawPrimaryPool().Acquire(context.Background())
	suite.Require().NoError(err)
	return conn.Release
}

func releaseAfter(release func(), d time.Duration) {
	time.AfterFunc(d, release)
}

func scanInt(row pgx.Row) (int, error) {
	var n int
	err := row.Scan(&n)
	return n, err
}

func timed(f func() error) (time.Duration, error) {
	start := time.Now()
	err := f()
	return time.Since(start), err
}

func (suite *dbTimeoutTestSuite) TestDBTimeoutStartsAfterAcquire() {
	pool := suite.newPool(func(c *wpgx.Config) { c.AcquireTimeout = 2 * time.Second })
	releaseAfter(suite.hold(pool), 300*time.Millisecond)
	ctx := wpgx.WithDBTimeout(context.Background(), "after_acquire", 200*time.Millisecond)
	elapsed, err := timed(func() error {
		_, err := scanInt(pool.WConn().WQueryRow(ctx, "after_acquire", "SELECT 1 FROM pg_sleep(0.1)"))
		return err
	})
	suite.NoError(err, "300ms of waiting must not count against the 200ms database timeout")
	suite.GreaterOrEqual(elapsed, 350*time.Millisecond)
}

func (suite *dbTimeoutTestSuite) TestWithoutDBTimeoutWaitingCountsAgainstTheContext() {
	pool := suite.newPool(func(c *wpgx.Config) { c.AcquireTimeout = 2 * time.Second })
	releaseAfter(suite.hold(pool), 300*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := scanInt(pool.WConn().WQueryRow(ctx, "legacy", "SELECT 1"))
	suite.ErrorIs(err, context.DeadlineExceeded)
	suite.NotErrorIs(err, wpgx.ErrAcquireTimeout, "the legacy path has no acquire budget")
}

func (suite *dbTimeoutTestSuite) TestDBTimeoutForAnotherStatementIsIgnored() {
	pool := suite.newPool()
	ctx := wpgx.WithDBTimeout(context.Background(), "outer", 50*time.Millisecond)
	n, err := scanInt(pool.WConn().WQueryRow(ctx, "inner", "SELECT 1 FROM pg_sleep(0.2)"))
	suite.NoError(err)
	suite.Equal(1, n)
}

func (suite *dbTimeoutTestSuite) TestAcquireWaitIsTheLargerOfTimeouts() {
	pool := suite.newPool(func(c *wpgx.Config) { c.AcquireTimeout = 300 * time.Millisecond })
	defer suite.hold(pool)()

	ctx := wpgx.WithDBTimeout(context.Background(), "short", 50*time.Millisecond)
	elapsed, err := timed(func() error {
		_, err := scanInt(pool.WConn().WQueryRow(ctx, "short", "SELECT 1"))
		return err
	})
	suite.ErrorIs(err, wpgx.ErrAcquireTimeout)
	suite.ErrorIs(err, context.DeadlineExceeded)
	suite.NotErrorIs(err, wpgx.ErrDBTimeout)
	suite.GreaterOrEqual(elapsed, 280*time.Millisecond, "a 50ms statement waits AcquireTimeout")
	suite.Less(elapsed, 2*time.Second)

	ctx = wpgx.WithDBTimeout(context.Background(), "long", 600*time.Millisecond)
	elapsed, err = timed(func() error {
		_, err := scanInt(pool.WConn().WQueryRow(ctx, "long", "SELECT 1"))
		return err
	})
	suite.ErrorIs(err, wpgx.ErrAcquireTimeout)
	suite.GreaterOrEqual(elapsed, 580*time.Millisecond, "a 600ms statement waits its own timeout")
	suite.Less(elapsed, 2*time.Second)
}

func (suite *dbTimeoutTestSuite) TestZeroAcquireTimeoutMeansDefault() {
	pool := suite.newPool(func(c *wpgx.Config) { c.AcquireTimeout = 0 })
	releaseAfter(suite.hold(pool), time.Second)
	ctx := wpgx.WithDBTimeout(context.Background(), "default_wait", 50*time.Millisecond)
	_, err := scanInt(pool.WConn().WQueryRow(ctx, "default_wait", "SELECT 1"))
	suite.NoError(err, "the default wait (%s) outlasts the one-second hold", wpgx.DefaultAcquireTimeout)
}

func (suite *dbTimeoutTestSuite) TestEveryOperationReportsAcquireTimeout() {
	pool := suite.newPool(func(c *wpgx.Config) { c.AcquireTimeout = 100 * time.Millisecond })
	defer suite.hold(pool)()
	conn := pool.WConn()
	ctxFor := func(name string) context.Context {
		return wpgx.WithDBTimeout(context.Background(), name, 10*time.Millisecond)
	}

	rows, err := conn.WQuery(ctxFor("q"), "q", "SELECT 1")
	suite.ErrorIs(err, wpgx.ErrAcquireTimeout)
	suite.False(rows.Next())
	suite.ErrorIs(rows.Err(), wpgx.ErrAcquireTimeout)
	rows.Close()

	_, err = conn.WExec(ctxFor("e"), "e", "SELECT 1")
	suite.ErrorIs(err, wpgx.ErrAcquireTimeout)

	_, err = conn.WCopyFrom(ctxFor("c"), "c", pgx.Identifier{"budget_items"}, []string{"id"},
		pgx.CopyFromRows([][]any{{1}}))
	suite.ErrorIs(err, wpgx.ErrAcquireTimeout)
}

func (suite *dbTimeoutTestSuite) TestCallerDeadlineAndCancelWin() {
	pool := suite.newPool(func(c *wpgx.Config) { c.AcquireTimeout = 5 * time.Second })
	defer suite.hold(pool)()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	ctx = wpgx.WithDBTimeout(ctx, "caller_deadline", time.Second)
	elapsed, err := timed(func() error {
		_, err := scanInt(pool.WConn().WQueryRow(ctx, "caller_deadline", "SELECT 1"))
		return err
	})
	suite.ErrorIs(err, context.DeadlineExceeded)
	suite.NotErrorIs(err, wpgx.ErrAcquireTimeout, "the caller's deadline expired, not wpgx's")
	suite.Less(elapsed, time.Second)

	ctx, cancel = context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	ctx = wpgx.WithDBTimeout(ctx, "caller_cancel", time.Second)
	elapsed, err = timed(func() error {
		_, err := scanInt(pool.WConn().WQueryRow(ctx, "caller_cancel", "SELECT 1"))
		return err
	})
	suite.ErrorIs(err, context.Canceled)
	suite.Less(elapsed, time.Second)
}

func (suite *dbTimeoutTestSuite) TestDBTimeout() {
	pool := suite.newPool()
	conn := pool.WConn()
	ctxFor := func(name string) context.Context {
		return wpgx.WithDBTimeout(context.Background(), name, 100*time.Millisecond)
	}

	elapsed, err := timed(func() error {
		_, err := scanInt(conn.WQueryRow(ctxFor("row_sleep"), "row_sleep", "SELECT 1 FROM pg_sleep(2)"))
		return err
	})
	suite.ErrorIs(err, wpgx.ErrDBTimeout)
	suite.ErrorIs(err, context.DeadlineExceeded)
	suite.Less(elapsed, 1500*time.Millisecond)

	rows, err := conn.WQuery(ctxFor("rows_sleep"), "rows_sleep", "SELECT 1 FROM pg_sleep(2)")
	if err == nil {
		suite.False(rows.Next())
		err = rows.Err()
		rows.Close()
	}
	suite.ErrorIs(err, wpgx.ErrDBTimeout)

	_, err = conn.WExec(ctxFor("exec_sleep"), "exec_sleep", "SELECT pg_sleep(2)")
	suite.ErrorIs(err, wpgx.ErrDBTimeout)

	// The pool recovers: a broken connection is replaced.
	n, err := scanInt(conn.WQueryRow(ctxFor("after"), "after", "SELECT 7"))
	suite.NoError(err)
	suite.Equal(7, n)
}

func (suite *dbTimeoutTestSuite) TestRowsCarryTheStatementContext() {
	pool := suite.newPool()
	const timeout = 300 * time.Millisecond
	ctx := wpgx.WithDBTimeout(context.Background(), "stream", timeout)
	start := time.Now()
	// Large enough that reading it needs the network after the deadline.
	rows, err := pool.WConn().WQuery(ctx, "stream", "SELECT repeat('x', 1000) FROM generate_series(1, 200000)")
	suite.Require().NoError(err)
	rc, ok := rows.(interface{ Context() context.Context })
	suite.Require().True(ok, "rows under a database timeout expose the statement context")
	deadline, ok := rc.Context().Deadline()
	suite.Require().True(ok)
	suite.WithinDuration(start.Add(timeout), deadline, 200*time.Millisecond)

	suite.True(rows.Next())
	time.Sleep(timeout + 100*time.Millisecond) // a slow reader keeps the statement running
	for rows.Next() {
	}
	suite.ErrorIs(rows.Err(), wpgx.ErrDBTimeout, "reading rows counts against the database timeout")
	rows.Close()
	suite.Error(rc.Context().Err(), "finished rows cancel the statement context")
}

func (suite *dbTimeoutTestSuite) TestRowsReadToTheEndReleaseTheConnection() {
	pool := suite.newPool()
	ctx := wpgx.WithDBTimeout(context.Background(), "series", time.Second)
	rows, err := pool.WConn().WQuery(ctx, "series", "SELECT generate_series(1, 3)")
	suite.Require().NoError(err)
	sum := 0
	for rows.Next() {
		var n int
		suite.Require().NoError(rows.Scan(&n))
		sum += n
	}
	suite.NoError(rows.Err())
	suite.Equal(6, sum)
	suite.Equal(int32(0), pool.RawPrimaryPool().Stat().AcquiredConns(), "released without Close")
	rows.Close()
}

func (suite *dbTimeoutTestSuite) TestExecAndCopyFromWithDBTimeout() {
	pool := suite.newPool()
	conn := pool.WConn()
	ctx := context.Background()
	_, err := conn.WExec(wpgx.WithDBTimeout(ctx, "insert", time.Second), "insert",
		"INSERT INTO budget_items (id) VALUES (1)")
	suite.Require().NoError(err)
	n, err := conn.WCopyFrom(wpgx.WithDBTimeout(ctx, "copy", time.Second), "copy",
		pgx.Identifier{"budget_items"}, []string{"id"}, pgx.CopyFromRows([][]any{{2}, {3}}))
	suite.Require().NoError(err)
	suite.Equal(int64(2), n)
	count, err := scanInt(conn.WQueryRow(wpgx.WithDBTimeout(ctx, "count", time.Second), "count",
		"SELECT count(*) FROM budget_items"))
	suite.NoError(err)
	suite.Equal(3, count)
}

func (suite *dbTimeoutTestSuite) TestTransactionStatementTimeout() {
	pool := suite.newPool()
	ctx := context.Background()
	_, err := pool.Transact(ctx, pgx.TxOptions{}, func(ctx context.Context, tx *wpgx.WTx) (any, error) {
		n, err := scanInt(tx.WQueryRow(wpgx.WithDBTimeout(ctx, "tx_ok", time.Second), "tx_ok", "SELECT 5"))
		suite.NoError(err)
		suite.Equal(5, n)
		_, err = tx.WExec(wpgx.WithDBTimeout(ctx, "tx_insert", time.Second), "tx_insert",
			"INSERT INTO budget_items (id) VALUES (10)")
		return nil, err
	})
	suite.Require().NoError(err, "statements under a database timeout keep the transaction usable")

	var stmtErr error
	elapsed, _ := timed(func() error {
		_, err = pool.Transact(ctx, pgx.TxOptions{}, func(ctx context.Context, tx *wpgx.WTx) (any, error) {
			_, stmtErr = scanInt(tx.WQueryRow(wpgx.WithDBTimeout(ctx, "tx_sleep", 100*time.Millisecond),
				"tx_sleep", "SELECT 1 FROM pg_sleep(2)"))
			return nil, stmtErr
		})
		return err
	})
	suite.ErrorIs(stmtErr, wpgx.ErrDBTimeout)
	suite.Error(err)
	suite.Less(elapsed, 1500*time.Millisecond)
}

func (suite *dbTimeoutTestSuite) TestMaxAcquireWaiters() {
	pool := suite.newPool(func(c *wpgx.Config) {
		c.AcquireTimeout = 5 * time.Second
		c.MaxAcquireWaiters = 1
	})
	release := suite.hold(pool)
	waiter := make(chan error, 1)
	go func() {
		ctx := wpgx.WithDBTimeout(context.Background(), "waiter", time.Second)
		_, err := scanInt(pool.WConn().WQueryRow(ctx, "waiter", "SELECT 1"))
		waiter <- err
	}()
	time.Sleep(200 * time.Millisecond) // the first statement is waiting now

	ctx := wpgx.WithDBTimeout(context.Background(), "rejected", time.Second)
	elapsed, err := timed(func() error {
		_, err := scanInt(pool.WConn().WQueryRow(ctx, "rejected", "SELECT 1"))
		return err
	})
	suite.ErrorIs(err, wpgx.ErrTooManyAcquireWaiters)
	suite.Less(elapsed, 100*time.Millisecond, "rejected without waiting")

	release()
	suite.NoError(<-waiter, "the waiter within the cap still gets the connection")
}

// histogramCount returns the sample count of histogram @p metric for the
// series with all of @p labels, and whether that series exists.
func histogramCount(metric string, labels map[string]string) (uint64, bool) {
	m, ok := gatheredMetric(metric, labels)
	if !ok {
		return 0, false
	}
	return m.GetHistogram().GetSampleCount(), true
}

func gatheredMetric(metric string, labels map[string]string) (*dto.Metric, bool) {
	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		return nil, false
	}
	for _, mf := range mfs {
		if mf.GetName() != metric {
			continue
		}
	next:
		for _, m := range mf.GetMetric() {
			got := map[string]string{}
			for _, l := range m.GetLabel() {
				got[l.GetName()] = l.GetValue()
			}
			for k, v := range labels {
				if got[k] != v {
					continue next
				}
			}
			return m, true
		}
	}
	return nil, false
}

func (suite *dbTimeoutTestSuite) TestPhaseMetrics() {
	const app = "budget_metrics"
	pool := suite.newPool(func(c *wpgx.Config) {
		c.AppName = app
		c.EnablePrometheus = true
		c.AcquireTimeout = 100 * time.Millisecond
	})
	conn := pool.WConn()
	bg := context.Background()

	_, err := scanInt(conn.WQueryRow(wpgx.WithDBTimeout(bg, "m_ok", time.Second), "m_ok", "SELECT 1"))
	suite.Require().NoError(err)
	_, err = scanInt(conn.WQueryRow(wpgx.WithDBTimeout(bg, "m_db", 50*time.Millisecond), "m_db",
		"SELECT 1 FROM pg_sleep(1)"))
	suite.Require().ErrorIs(err, wpgx.ErrDBTimeout)
	release := suite.hold(pool)
	_, err = scanInt(conn.WQueryRow(wpgx.WithDBTimeout(bg, "m_wait", 10*time.Millisecond), "m_wait", "SELECT 1"))
	suite.Require().ErrorIs(err, wpgx.ErrAcquireTimeout)
	release()

	waits, ok := histogramCount("wpgx_acquire_wait_milliseconds", map[string]string{"app": app, "replica": "primary"})
	suite.True(ok)
	suite.Equal(uint64(3), waits, "every statement records its wait, including the one that timed out")
	for _, op := range []string{"m_ok", "m_db"} {
		n, ok := histogramCount("wpgx_db_latency_milliseconds", map[string]string{"app": app, "op": op})
		suite.True(ok, op)
		suite.Equal(uint64(1), n, op)
	}
	_, ok = histogramCount("wpgx_db_latency_milliseconds", map[string]string{"app": app, "op": "m_wait"})
	suite.False(ok, "a statement that never got a connection has no database time")
	for op, phase := range map[string]string{"m_db": "db", "m_wait": "acquire"} {
		m, ok := gatheredMetric("wpgx_timeout_total", map[string]string{"app": app, "op": op, "phase": phase})
		suite.True(ok, op)
		suite.Equal(1.0, m.GetCounter().GetValue(), op)
	}
	_, ok = gatheredMetric("wpgx_timeout_total", map[string]string{"app": app, "op": "m_ok"})
	suite.False(ok)
	n, ok := histogramCount("wpgx_latency_milliseconds", map[string]string{"app": app, "op": "m_wait"})
	suite.True(ok, "the end-to-end latency keeps covering the wait")
	suite.Equal(uint64(1), n)

	// Pool counters are refreshed every few seconds.
	for _, op := range []string{"acquire_waiting", "acquire_rejected_count", "empty_acquire_count",
		"empty_acquire_wait_seconds", "canceled_acquire_count", "new_conns_count"} {
		suite.Eventually(func() bool {
			_, ok := gatheredMetric("wpgx_conn_pool", map[string]string{"app": app, "op": op, "replica": "primary"})
			return ok
		}, 10*time.Second, 100*time.Millisecond, op)
	}
	m, _ := gatheredMetric("wpgx_conn_pool", map[string]string{"app": app, "op": "empty_acquire_count"})
	suite.GreaterOrEqual(m.GetGauge().GetValue(), 1.0, "the timed-out statement found the pool empty")
}

func (suite *dbTimeoutTestSuite) TestSpanRecordsAcquireWait() {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	defer otel.SetTracerProvider(prev)
	pool := suite.newPool(func(c *wpgx.Config) { c.EnableTracing = true })

	ctx, parent := tp.Tracer("test").Start(context.Background(), "parent")
	defer parent.End()
	_, err := scanInt(pool.WConn().WQueryRow(wpgx.WithDBTimeout(ctx, "traced", time.Second), "traced", "SELECT 1"))
	suite.Require().NoError(err)
	var found bool
	for _, s := range recorder.Ended() {
		if s.Name() != "traced" {
			continue
		}
		for _, kv := range s.Attributes() {
			if kv.Key == "wpgx.acquire_wait_ms" {
				found = true
				suite.GreaterOrEqual(kv.Value.AsFloat64(), 0.0)
			}
		}
	}
	suite.True(found)
}
