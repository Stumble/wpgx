package wpgx

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	// ReservedReplicaNamePrimary is the name of the primary replica.
	ReservedReplicaNamePrimary = "primary"
)

var (
	// ErrReplicaNotFound is the error when the replica is not found.
	ErrReplicaNotFound = fmt.Errorf("replica not found")
)

// ReplicaName is the name of the replica instance.
type ReplicaName string

// toLabel is used to convert the replica name to a label.
func toLabel(replicaName *ReplicaName) string {
	if replicaName == nil {
		return ReservedReplicaNamePrimary
	}
	return string(*replicaName)
}

// PostExecFunc is the function that must be ran after a successful CRUD.
// NOTE: context should have been captured into the function.
type PostExecFunc = func() error

// TxFunc is the body of a transaction.
// ctx must be used to generate proper tracing spans.
// If not, you might see incorrect parallel spans.
type TxFunc = func(ctx context.Context, tx *WTx) (any, error)

// WQuerier is the abstraction of connections that are read-only.
type WQuerier interface {
	WQuery(
		ctx context.Context, name string, unprepared string, args ...interface{}) (pgx.Rows, error)
	WQueryRow(
		ctx context.Context, name string, unprepared string, args ...interface{}) pgx.Row
	// CountIntent is used to count the number of query intents.
	CountIntent(name string)
}

// WExecer is the abstraction of connections that can execute mutations.
type WExecer interface {
	// WExec is used to run a CRUD operation.
	WExec(
		ctx context.Context, name string, unprepared string, args ...interface{}) (pgconn.CommandTag, error)
	// PostExec is used to run a function after a successful CRUD, like invalidating a cache.
	PostExec(f PostExecFunc) error
}

// WCopyFromer is the abstraction of connections that can use PostgreSQL's copyfrom.
type WCopyFromer interface {
	WCopyFrom(
		ctx context.Context, name string, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (int64, error)
}

// WGConn is the abstraction over wrapped connections and transactions.
type WGConn interface {
	WQuerier
	WExecer
	WCopyFromer
}
