package wpgx

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DefaultAcquireTimeout is the AcquireTimeout of a pool configured with zero.
const DefaultAcquireTimeout = 2 * time.Second

var (
	// ErrAcquireTimeout is matched by the error of a statement run with a
	// database timeout that ran out of time waiting for a pooled connection.
	// Such an error also matches context.DeadlineExceeded.
	ErrAcquireTimeout = errors.New("wpgx: timed out waiting for a database connection")
	// ErrDBTimeout is matched by the error of a statement that exceeded its
	// database timeout. Such an error also matches context.DeadlineExceeded.
	ErrDBTimeout = errors.New("wpgx: database timeout exceeded")
	// ErrTooManyAcquireWaiters is returned by a statement run with a database
	// timeout when MaxAcquireWaiters statements already wait for a connection.
	ErrTooManyAcquireWaiters = errors.New("wpgx: too many statements waiting for a database connection")

	// errStatementPanicked finishes a statement abandoned by a panic.
	errStatementPanicked = errors.New("wpgx: statement panicked")
)

// budgetError reports which wpgx budget expired. It matches both the budget's
// sentinel error and the error it wraps.
type budgetError struct {
	budget error
	err    error
}

func (e *budgetError) Error() string   { return e.budget.Error() + ": " + e.err.Error() }
func (e *budgetError) Unwrap() []error { return []error{e.budget, e.err} }

// The causes of the contexts wpgx creates, so context.Cause tells which budget
// expired. A context whose parent expired first reports the parent's cause.
var (
	acquireTimeoutCause = &budgetError{budget: ErrAcquireTimeout, err: context.DeadlineExceeded}
	dbTimeoutCause      = &budgetError{budget: ErrDBTimeout, err: context.DeadlineExceeded}
)

// classify returns @p err wrapped in @p cause's budget error when @p err is the
// deadline of @p ctx, a context created with @p cause, rather than of its
// parent.
func classify(ctx context.Context, cause *budgetError, err error) error {
	if err == nil || context.Cause(ctx) != cause || !errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, cause.budget) {
		return err
	}
	return &budgetError{budget: cause.budget, err: err}
}

type dbTimeoutKey struct{}

type dbTimeout struct {
	name    string
	timeout time.Duration
}

// WithDBTimeout returns a copy of @p ctx that gives the statement named @p name
// a database timeout. A WConn or WTx operation with that name and context then
// runs as follows; operations with another name are not affected.
//
//   - On a pool connection (WConn), the statement first waits for a connection,
//     at most the larger of @p timeout and the pool's AcquireTimeout.
//   - @p timeout then starts once the statement has a connection, or at the
//     call inside a transaction (WTx), and lasts until the connection is free
//     again: when Exec or CopyFrom returns, when Row.Scan returns, or when Rows
//     are read to the end or closed.
//
// Both bounds also end with @p ctx, whose deadline and cancellation always
// apply. Errors from an expired bound match ErrAcquireTimeout or ErrDBTimeout,
// and context.DeadlineExceeded. Rows returned under a database timeout have a
// Context method that returns the statement's context.
//
// A non-positive @p timeout returns @p ctx unchanged.
func WithDBTimeout(ctx context.Context, name string, timeout time.Duration) context.Context {
	if timeout <= 0 {
		return ctx
	}
	return context.WithValue(ctx, dbTimeoutKey{}, dbTimeout{name: name, timeout: timeout})
}

// StatementErr returns the error to report for work done under @p ctx, a
// statement context (Rows.Context under WithDBTimeout) or one derived from it,
// that ended with @p err:
//
//   - a non-nil @p err, also matching ErrDBTimeout when it is the deadline of
//     the statement's database timeout;
//   - for a nil @p err, an error matching ErrDBTimeout and
//     context.DeadlineExceeded if that database timeout has expired, and nil
//     otherwise. Finishing the rows cancels the statement context; that is
//     not an error.
//
// For example, a row visitor checks StatementErr(ctx, ctx.Err()), and a reader
// that stopped early checks StatementErr(ctx, rows.Err()) after closing rows.
func StatementErr(ctx context.Context, err error) error {
	if err != nil {
		return classify(ctx, dbTimeoutCause, err)
	}
	if context.Cause(ctx) == dbTimeoutCause {
		return dbTimeoutCause
	}
	return nil
}

// dbTimeoutOf returns the database timeout that @p ctx gives to statement @p name.
func dbTimeoutOf(ctx context.Context, name string) (time.Duration, bool) {
	v, ok := ctx.Value(dbTimeoutKey{}).(dbTimeout)
	if !ok || v.name != name {
		return 0, false
	}
	return v.timeout, true
}

// acquirer acquires pool connections for statements run with a database timeout.
type acquirer struct {
	pool       *pgxpool.Pool
	timeout    time.Duration // the shortest wait for a connection
	maxWaiters int32         // zero means no cap
	waiting    atomic.Int32
	rejected   atomic.Int64
}

func newAcquirer(pool *pgxpool.Pool, timeout time.Duration, maxWaiters int32) *acquirer {
	if timeout <= 0 {
		timeout = DefaultAcquireTimeout
	}
	return &acquirer{pool: pool, timeout: timeout, maxWaiters: maxWaiters}
}

// acquire waits for a connection at most the larger of @p dbTimeout and the
// pool's AcquireTimeout, and never past @p ctx. A connection is returned only
// while that wait has not ended.
func (a *acquirer) acquire(ctx context.Context, dbTimeout time.Duration) (*pgxpool.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	n := a.waiting.Add(1)
	defer a.waiting.Add(-1)
	if a.maxWaiters > 0 && n > a.maxWaiters {
		a.rejected.Add(1)
		return nil, ErrTooManyAcquireWaiters
	}
	actx, cancel := context.WithTimeoutCause(ctx, max(dbTimeout, a.timeout), acquireTimeoutCause)
	defer cancel()
	conn, err := a.pool.Acquire(actx)
	if ended := actx.Err(); ended != nil {
		switch {
		case err == nil:
			// pgxpool runs PrepareConn (BeforeAcquire) after taking a
			// connection, so a slow hook can return one after the wait ended.
			conn.Release()
			conn, err = nil, ended
		case !errors.Is(err, ended):
			// For example pgxpool giving up after hooks rejected connections.
			err = fmt.Errorf("%w: %w", ended, err)
		}
	}
	return conn, classify(actx, acquireTimeoutCause, err)
}

// stmt is a statement running under a database timeout.
type stmt struct {
	ctx       context.Context // carries the database timeout
	cancel    context.CancelFunc
	conn      *pgxpool.Conn // released when the statement finishes; nil in a transaction
	stats     *metricSet
	name      string
	replica   *ReplicaName
	startedAt time.Time
	finished  bool
}

// startStmt starts the database timeout of statement @p name on @p conn now.
func startStmt(ctx context.Context, timeout time.Duration, conn *pgxpool.Conn,
	stats *metricSet, name string, replica *ReplicaName) *stmt {
	sctx, cancel := context.WithTimeoutCause(ctx, timeout, dbTimeoutCause)
	return &stmt{ctx: sctx, cancel: cancel, conn: conn, stats: stats, name: name,
		replica: replica, startedAt: time.Now()}
}

func (s *stmt) classify(err error) error {
	return classify(s.ctx, dbTimeoutCause, err)
}

// finish ends the statement with @p err, the first time it is called, and
// returns @p err classified. It frees the connection.
func (s *stmt) finish(err error) error {
	err = s.classify(err)
	if s.finished {
		return err
	}
	s.finished = true
	// Whether the database timeout ended the statement context before the
	// statement finished, even if every row was already buffered.
	timedOut := context.Cause(s.ctx) == dbTimeoutCause
	s.cancel()
	if s.conn != nil {
		s.conn.Release()
	}
	s.stats.observeDB(s.name, s.replica, s.startedAt, timedOut)
	return err
}

// guard returns a function to defer while the caller owns the statement: unless
// disarmed by setting *@p handedOff, it finishes the statement, so a panic
// does not keep the connection.
func (s *stmt) guard(handedOff *bool) func() {
	return func() {
		if !*handedOff && !s.finished {
			_ = s.finish(errStatementPanicked)
		}
	}
}

// stmtRows are the rows of a statement running under a database timeout. The
// statement finishes when they are read to the end or closed.
type stmtRows struct {
	pgx.Rows
	s *stmt
}

func (r *stmtRows) Next() bool {
	if r.Rows.Next() {
		return true
	}
	_ = r.s.finish(r.Rows.Err()) // Err reports the classified error
	return false
}

func (r *stmtRows) Close() {
	r.Rows.Close()
	_ = r.s.finish(r.Rows.Err()) // Err reports the classified error
}

// Scan and Values close the rows on error, as pgxpool rows do, so the
// connection is not kept by a caller that stops at the error.
func (r *stmtRows) Scan(dest ...any) error {
	err := r.Rows.Scan(dest...)
	if err != nil {
		r.Close()
	}
	return err
}

func (r *stmtRows) Values() ([]any, error) {
	values, err := r.Rows.Values()
	if err != nil {
		r.Close()
	}
	return values, err
}

func (r *stmtRows) Err() error {
	return r.s.classify(r.Rows.Err())
}

// Context returns the statement's context, which carries its database timeout.
// It is canceled when the rows are read to the end or closed.
func (r *stmtRows) Context() context.Context {
	return r.s.ctx
}

// stmtRow is the row of a statement running under a database timeout. The
// statement finishes when the row is scanned.
type stmtRow struct {
	pgx.Row
	s *stmt
}

func (r *stmtRow) Scan(dest ...any) error {
	scanned := false
	defer r.s.guard(&scanned)()
	err := r.Row.Scan(dest...)
	scanned = true
	return r.s.finish(err)
}

// errRows are the rows of a statement that failed before it was sent.
type errRows struct{ err error }

func (r errRows) Close()                                       {}
func (r errRows) Err() error                                   { return r.err }
func (r errRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r errRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r errRows) Next() bool                                   { return false }
func (r errRows) Scan(...any) error                            { return r.err }
func (r errRows) Values() ([]any, error)                       { return nil, r.err }
func (r errRows) RawValues() [][]byte                          { return nil }
func (r errRows) Conn() *pgx.Conn                              { return nil }
func (r errRows) TypeMap() *pgtype.Map                         { return nil }

// errRow is the row of a statement that failed before it was sent.
type errRow struct{ err error }

func (r errRow) Scan(...any) error { return r.err }
