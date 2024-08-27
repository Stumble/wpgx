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
	stats       *metricSet
	tracer      *tracer
	replicaName *ReplicaName
}

var _ WGConn = (*WConn)(nil)

func (c *WConn) PostExec(fn PostExecFunc) error {
	return fn()
}

func (c *WConn) WQuery(ctx context.Context, name string, unprepared string, args ...interface{}) (r pgx.Rows, err error) {
	if c.stats != nil {
		defer c.stats.MakeObserver(name, c.replicaName, time.Now(), &err)()
	}
	if c.tracer != nil {
		ctx = c.tracer.TraceStart(ctx, name, c.replicaName)
		defer c.tracer.TraceEnd(ctx, &err)
	}
	r, err = c.p.Query(ctx, unprepared, args...)
	return
}

func (c *WConn) WQueryRow(ctx context.Context, name string, unprepared string, args ...interface{}) pgx.Row {
	if c.stats != nil {
		defer c.stats.MakeObserver(name, c.replicaName, time.Now(), nil)()
	}
	if c.tracer != nil {
		ctx = c.tracer.TraceStart(ctx, name, c.replicaName)
		defer c.tracer.TraceEnd(ctx, nil)
	}
	return c.p.QueryRow(ctx, unprepared, args...)
}

func (c *WConn) WExec(ctx context.Context, name string, unprepared string, args ...interface{}) (cmd pgconn.CommandTag, err error) {
	if c.stats != nil {
		defer c.stats.MakeObserver(name, c.replicaName, time.Now(), &err)()
	}
	if c.tracer != nil {
		ctx = c.tracer.TraceStart(ctx, name, c.replicaName)
		defer c.tracer.TraceEnd(ctx, &err)
	}
	cmd, err = c.p.Exec(ctx, unprepared, args...)
	return
}

func (c *WConn) WCopyFrom(
	ctx context.Context, name string, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (n int64, err error) {
	if c.stats != nil {
		defer c.stats.MakeObserver(name, c.replicaName, time.Now(), &err)()
	}
	if c.tracer != nil {
		ctx = c.tracer.TraceStart(ctx, name, c.replicaName)
		defer c.tracer.TraceEnd(ctx, &err)
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
