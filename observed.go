package wpgx

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/trace"
)

// op observes one database operation: it counts the request and any error,
// records the latency, and ends the client span, all when the operation ends.
// A nil *op observes nothing.
type op struct {
	stats       *metricSet
	name        string
	replicaName *ReplicaName
	startedAt   time.Time
	span        trace.Span // nil when no span was started
}

// startOp starts observing operation @p name. The returned context carries the
// operation's span, if one was started, and must be used to run the operation.
func startOp(ctx context.Context, stats *metricSet, tracer *tracer, name string, replicaName *ReplicaName) (context.Context, *op) {
	if stats == nil && tracer == nil {
		return ctx, nil
	}
	o := &op{stats: stats, name: name, replicaName: replicaName, startedAt: time.Now()}
	if tracer != nil {
		ctx, o.span = tracer.start(ctx, name, replicaName)
	}
	return ctx, o
}

// end ends the operation with @p err, which is nil on success.
func (o *op) end(err error) {
	if o == nil {
		return
	}
	if o.stats != nil {
		o.stats.observe(o.name, o.replicaName, o.startedAt, err)
	}
	if o.span != nil {
		endSpan(o.span, err)
	}
}

// rows ends the operation when @p rows are finished, i.e. read to the end or
// closed. pgx reports most query failures, such as execution and scan errors,
// through rows.Err() rather than from Query.
func (o *op) rows(rows pgx.Rows, err error) pgx.Rows {
	if o == nil {
		return rows
	}
	if err != nil {
		o.end(err)
		return rows
	}
	return &observedRows{Rows: rows, op: o}
}

// row ends the operation when @p row is scanned, which is where QueryRow
// errors surface. pgx.ErrNoRows is an expected outcome, not a failure.
func (o *op) row(row pgx.Row) pgx.Row {
	if o == nil {
		return row
	}
	return &observedRow{Row: row, op: o}
}

type observedRows struct {
	pgx.Rows
	op   *op
	done bool
}

func (r *observedRows) Next() bool {
	if r.Rows.Next() {
		return true
	}
	r.finish()
	return false
}

func (r *observedRows) Close() {
	r.Rows.Close()
	r.finish()
}

func (r *observedRows) finish() {
	if r.done {
		return
	}
	r.done = true
	r.op.end(r.Err())
}

type observedRow struct {
	pgx.Row
	op   *op
	done bool
}

func (r *observedRow) Scan(dest ...any) error {
	err := r.Row.Scan(dest...)
	if !r.done {
		r.done = true
		if errors.Is(err, pgx.ErrNoRows) {
			r.op.end(nil)
		} else {
			r.op.end(err)
		}
	}
	return err
}
