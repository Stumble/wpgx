package wpgx

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type WConn struct {
	p           *pgxpool.Pool
	stats       *metricSet
	tracer      *tracer
	replicaName *ReplicaName
}

var _ WGConn = (*WConn)(nil)

func (c *WConn) PostExec(fn PostExecFunc) error {
	return fn()
}

func (c *WConn) WQuery(ctx context.Context, name string, unprepared string, args ...interface{}) (pgx.Rows, error) {
	ctx, o := startOp(ctx, c.stats, c.tracer, name, c.replicaName)
	rows, err := c.p.Query(ctx, unprepared, args...)
	return o.rows(rows, err), err
}

func (c *WConn) WQueryRow(ctx context.Context, name string, unprepared string, args ...interface{}) pgx.Row {
	ctx, o := startOp(ctx, c.stats, c.tracer, name, c.replicaName)
	return o.row(c.p.QueryRow(ctx, unprepared, args...))
}

func (c *WConn) WExec(ctx context.Context, name string, unprepared string, args ...interface{}) (cmd pgconn.CommandTag, err error) {
	ctx, o := startOp(ctx, c.stats, c.tracer, name, c.replicaName)
	defer func() { o.end(err) }()
	cmd, err = c.p.Exec(ctx, unprepared, args...)
	return
}

func (c *WConn) WCopyFrom(
	ctx context.Context, name string, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (n int64, err error) {
	ctx, o := startOp(ctx, c.stats, c.tracer, name, c.replicaName)
	defer func() { o.end(err) }()
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
