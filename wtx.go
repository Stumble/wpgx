package wpgx

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rs/zerolog/log"
)

// WTx is a wrapped pgx.Tx. The main reason is to overwrite the PostExec method that
// run all of them until the transaction is successfully committed.
type WTx struct {
	tx            pgx.Tx
	stats         *metricSet
	tracer        *tracer
	postExecFuncs []PostExecFunc
	committed     bool
	mutex         sync.Mutex
}

var _ WGConn = (*WTx)(nil)

func (t *WTx) runPostExecFuncs() {
	t.mutex.Lock()
	funcs := t.postExecFuncs
	t.mutex.Unlock()
	var wg sync.WaitGroup
	for _, v := range funcs {
		wg.Add(1)
		go func(fn PostExecFunc) {
			defer wg.Done()
			err := fn()
			if err != nil {
				log.Err(err).Msgf("Failed to run post exec function")
			}
		}(v)
	}
	wg.Wait()
}

func (t *WTx) PostExec(f PostExecFunc) error {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.postExecFuncs = append(t.postExecFuncs, f)
	return nil
}

// start starts the database timeout of statement @p name, see WithDBTimeout.
// The transaction already holds its connection.
func (t *WTx) start(ctx context.Context, name string, timeout time.Duration) *stmt {
	return startStmt(ctx, timeout, nil, t.stats, name, nil)
}

func (t *WTx) WQuery(ctx context.Context, name string, unprepared string, args ...interface{}) (pgx.Rows, error) {
	ctx, o := startOp(ctx, t.stats, t.tracer, name, nil)
	if timeout, ok := dbTimeoutOf(ctx, name); ok {
		s := t.start(ctx, name, timeout)
		rows, err := t.tx.Query(s.ctx, unprepared, args...)
		if err != nil {
			err = s.finish(err)
			o.end(err)
			return errRows{err: err}, err
		}
		return &stmtRows{Rows: o.rows(rows, nil), s: s}, nil
	}
	rows, err := t.tx.Query(ctx, unprepared, args...)
	return o.rows(rows, err), err
}

func (t *WTx) WQueryRow(ctx context.Context, name string, unprepared string, args ...interface{}) pgx.Row {
	ctx, o := startOp(ctx, t.stats, t.tracer, name, nil)
	if timeout, ok := dbTimeoutOf(ctx, name); ok {
		s := t.start(ctx, name, timeout)
		return &stmtRow{Row: o.row(t.tx.QueryRow(s.ctx, unprepared, args...)), s: s}
	}
	return o.row(t.tx.QueryRow(ctx, unprepared, args...))
}

func (t *WTx) WExec(ctx context.Context, name string, unprepared string, args ...interface{}) (cmd pgconn.CommandTag, err error) {
	ctx, o := startOp(ctx, t.stats, t.tracer, name, nil)
	defer func() { o.end(err) }()
	if timeout, ok := dbTimeoutOf(ctx, name); ok {
		s := t.start(ctx, name, timeout)
		cmd, err = t.tx.Exec(s.ctx, unprepared, args...)
		return cmd, s.finish(err)
	}
	cmd, err = t.tx.Exec(ctx, unprepared, args...)
	return
}

func (t *WTx) WCopyFrom(
	ctx context.Context, name string, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (n int64, err error) {
	ctx, o := startOp(ctx, t.stats, t.tracer, name, nil)
	defer func() { o.end(err) }()
	if timeout, ok := dbTimeoutOf(ctx, name); ok {
		s := t.start(ctx, name, timeout)
		n, err = t.tx.CopyFrom(s.ctx, tableName, columnNames, rowSrc)
		return n, s.finish(err)
	}
	n, err = t.tx.CopyFrom(ctx, tableName, columnNames, rowSrc)
	return
}

func (t *WTx) CountIntent(name string) {
	if t.stats != nil {
		t.stats.CountIntent(name, nil)
	}
}

// isCommitted reports whether Commit has succeeded.
func (t *WTx) isCommitted() bool {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	return t.committed
}

func (t *WTx) Rollback(ctx context.Context) error {
	return t.tx.Rollback(ctx)
}

func (t *WTx) Commit(ctx context.Context) error {
	err := t.tx.Commit(ctx)
	if err != nil {
		return err
	}
	t.mutex.Lock()
	t.committed = true
	t.mutex.Unlock()
	t.runPostExecFuncs()
	return nil
}
