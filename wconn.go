package wpgx

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type WConn struct {
	p *Pool
}

var _ WGConn = (*WConn)(nil)

func (c *WConn) PostExec(fn PostExecFunc) error {
	return fn()
}

func (c *WConn) WQuery(ctx context.Context, name string, unprepared string, args ...interface{}) (r pgx.Rows, err error) {
	if c.p.stats != nil {
		defer c.p.stats.MakeObserver(name, time.Now(), &err)()
	}
	if c.p.tracer != nil {
		ctx = c.p.tracer.TraceStart(ctx, name)
		defer c.p.tracer.TraceEnd(ctx, &err)
	}
	r, err = c.p.pool.Query(ctx, unprepared, args...)
	return
}

func (c *WConn) WQueryRow(ctx context.Context, name string, unprepared string, args ...interface{}) pgx.Row {
	if c.p.stats != nil {
		defer c.p.stats.MakeObserver(name, time.Now(), nil)()
	}
	if c.p.tracer != nil {
		ctx = c.p.tracer.TraceStart(ctx, name)
		defer c.p.tracer.TraceEnd(ctx, nil)
	}
	return c.p.pool.QueryRow(ctx, unprepared, args...)
}

func (c *WConn) WExec(ctx context.Context, name string, unprepared string, args ...interface{}) (cmd pgconn.CommandTag, err error) {
	if c.p.stats != nil {
		defer c.p.stats.MakeObserver(name, time.Now(), &err)()
	}
	if c.p.tracer != nil {
		ctx = c.p.tracer.TraceStart(ctx, name)
		defer c.p.tracer.TraceEnd(ctx, &err)
	}
	cmd, err = c.p.pool.Exec(ctx, unprepared, args...)
	return
}

func (c *WConn) WCopyFrom(
	ctx context.Context, name string, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (n int64, err error) {
	if c.p.stats != nil {
		defer c.p.stats.MakeObserver(name, time.Now(), &err)()
	}
	if c.p.tracer != nil {
		ctx = c.p.tracer.TraceStart(ctx, name)
		defer c.p.tracer.TraceEnd(ctx, &err)
	}
	n, err = c.p.pool.CopyFrom(ctx, tableName, columnNames, rowSrc)
	return
}

func (c *WConn) CountIntent(name string) {
	if c.p.stats != nil {
		c.p.stats.CountIntent(name)
	}
}

// pgx did the right thing: prepare should not be visible to a connection pool.
// func (c *WConn) Prepare(ctx context.Context, query string) (pgconn.StatementDescription, error) {
// 	return c.conn.Prepare(ctx, query)
// }
