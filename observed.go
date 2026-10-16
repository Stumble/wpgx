package wpgx

import (
	"errors"

	"github.com/jackc/pgx/v5"
)

// observedRows reports the error of a query that fails after Query returned,
// e.g. during execution or while reading rows; pgx surfaces those through
// rows.Err() rather than from Query.
type observedRows struct {
	pgx.Rows
	onErr func()
	done  bool
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
	if r.Err() != nil {
		r.onErr()
	}
}

// observedRow reports the error returned by Scan, which is where QueryRow
// errors surface. pgx.ErrNoRows is an expected outcome, not a failure.
type observedRow struct {
	pgx.Row
	onErr func()
}

func (r observedRow) Scan(dest ...any) error {
	err := r.Row.Scan(dest...)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		r.onErr()
	}
	return err
}

func observeRows(stats *metricSet, name string, replicaName *ReplicaName, rows pgx.Rows, err error) pgx.Rows {
	if stats == nil || err != nil {
		return rows
	}
	return &observedRows{Rows: rows, onErr: func() { stats.CountError(name, replicaName) }}
}

func observeRow(stats *metricSet, name string, replicaName *ReplicaName, row pgx.Row) pgx.Row {
	if stats == nil {
		return row
	}
	return observedRow{Row: row, onErr: func() { stats.CountError(name, replicaName) }}
}
