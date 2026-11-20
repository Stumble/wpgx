package wpgx

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type WConn struct {
	p           *pgxpool.Pool
	acq         *acquirer
	stats       *metricSet
	tracer      *tracer
	replicaName *ReplicaName
}

var _ WGConn = (*WConn)(nil)

func (c *WConn) PostExec(fn PostExecFunc) error {
	return fn()
}

// start waits for a connection for statement @p name and starts its database
// timeout, see WithDBTimeout.
func (c *WConn) start(ctx context.Context, o *op, name string, timeout time.Duration) (*stmt, error) {
	startedAt := time.Now()
	conn, err := c.acq.acquire(ctx, timeout)
	wait := time.Since(startedAt)
	c.stats.observeAcquire(name, c.replicaName, wait, err)
	o.acquired(wait)
	if err != nil {
		return nil, err
	}
	return startStmt(ctx, timeout, conn, c.stats, name, c.replicaName), nil
}

func (c *WConn) WQuery(ctx context.Context, name string, unprepared string, args ...interface{}) (pgx.Rows, error) {
	ctx, o := startOp(ctx, c.stats, c.tracer, name, c.replicaName)
	if timeout, ok := dbTimeoutOf(ctx, name); ok {
		s, err := c.start(ctx, o, name, timeout)
		if err != nil {
			o.end(err)
			return errRows{err: err}, err
		}
		rows, err := s.conn.Query(s.ctx, unprepared, args...)
		if err != nil {
			err = s.finish(err)
			o.end(err)
			return errRows{err: err}, err
		}
		return &stmtRows{Rows: o.rows(rows, nil), s: s}, nil
	}
	rows, err := c.p.Query(ctx, unprepared, args...)
	return o.rows(rows, err), err
}

func (c *WConn) WQueryRow(ctx context.Context, name string, unprepared string, args ...interface{}) pgx.Row {
	ctx, o := startOp(ctx, c.stats, c.tracer, name, c.replicaName)
	if timeout, ok := dbTimeoutOf(ctx, name); ok {
		s, err := c.start(ctx, o, name, timeout)
		if err != nil {
			return o.row(errRow{err: err})
		}
		return &stmtRow{Row: o.row(s.conn.QueryRow(s.ctx, unprepared, args...)), s: s}
	}
	return o.row(c.p.QueryRow(ctx, unprepared, args...))
}

func (c *WConn) WExec(ctx context.Context, name string, unprepared string, args ...interface{}) (cmd pgconn.CommandTag, err error) {
	ctx, o := startOp(ctx, c.stats, c.tracer, name, c.replicaName)
	defer func() { o.end(err) }()
	if timeout, ok := dbTimeoutOf(ctx, name); ok {
		var s *stmt
		if s, err = c.start(ctx, o, name, timeout); err != nil {
			return cmd, err
		}
		cmd, err = s.conn.Exec(s.ctx, unprepared, args...)
		return cmd, s.finish(err)
	}
	cmd, err = c.p.Exec(ctx, unprepared, args...)
	return
}

func (c *WConn) WCopyFrom(
	ctx context.Context, name string, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (n int64, err error) {
	ctx, o := startOp(ctx, c.stats, c.tracer, name, c.replicaName)
	defer func() { o.end(err) }()
	if timeout, ok := dbTimeoutOf(ctx, name); ok {
		var s *stmt
		if s, err = c.start(ctx, o, name, timeout); err != nil {
			return n, err
		}
		n, err = s.conn.CopyFrom(s.ctx, tableName, columnNames, rowSrc)
		return n, s.finish(err)
	}
	n, err = c.p.CopyFrom(ctx, tableName, columnNames, rowSrc)
	return
}

func (c *WConn) CountIntent(name string) {
	if c.stats != nil {
		c.stats.CountIntent(name, c.replicaName)
	}
}

// pgx did the right thing: prepare should not be visible to a connection pool.
// func (c *WConn) Prepare(ctx context.Context, query string) (pgconn.StatementDescription, error) {
// 	return c.conn.Prepare(ctx, query)
// }
