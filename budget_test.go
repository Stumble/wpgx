package wpgx

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDBTimeoutAppliesToItsStatementOnly(t *testing.T) {
	ctx := WithDBTimeout(context.Background(), "a", time.Second)
	timeout, ok := dbTimeoutOf(ctx, "a")
	require.True(t, ok)
	require.Equal(t, time.Second, timeout)
	_, ok = dbTimeoutOf(ctx, "b")
	require.False(t, ok, "a nested statement with another name does not inherit it")

	inner := WithDBTimeout(ctx, "b", time.Millisecond)
	timeout, ok = dbTimeoutOf(inner, "b")
	require.True(t, ok)
	require.Equal(t, time.Millisecond, timeout)
	_, ok = dbTimeoutOf(inner, "a")
	require.False(t, ok)

	require.Equal(t, ctx, WithDBTimeout(ctx, "a", 0), "a non-positive timeout changes nothing")
	_, ok = dbTimeoutOf(context.Background(), "a")
	require.False(t, ok)
}

func TestClassifyOnlyWrapsTheBudgetsOwnDeadline(t *testing.T) {
	expired, cancel := context.WithTimeoutCause(context.Background(), time.Nanosecond, dbTimeoutCause)
	defer cancel()
	<-expired.Done()
	err := classify(expired, dbTimeoutCause, context.DeadlineExceeded)
	require.ErrorIs(t, err, ErrDBTimeout)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.NotErrorIs(t, err, ErrAcquireTimeout)

	other := errors.New("syntax error")
	require.Equal(t, other, classify(expired, dbTimeoutCause, other), "errors other than the deadline pass through")
	require.NoError(t, classify(expired, dbTimeoutCause, nil))

	parent, cancelParent := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancelParent()
	child, cancelChild := context.WithTimeoutCause(parent, time.Hour, dbTimeoutCause)
	defer cancelChild()
	<-child.Done()
	err = classify(child, dbTimeoutCause, context.DeadlineExceeded)
	require.Equal(t, context.DeadlineExceeded, err, "the caller's deadline is not the database timeout")

	live, cancelLive := context.WithTimeoutCause(context.Background(), time.Hour, acquireTimeoutCause)
	defer cancelLive()
	require.Equal(t, context.DeadlineExceeded, classify(live, acquireTimeoutCause, context.DeadlineExceeded))
}

func TestNewAcquirerDefaultsTheTimeout(t *testing.T) {
	require.Equal(t, DefaultAcquireTimeout, newAcquirer(nil, 0, 0).timeout)
	require.Equal(t, time.Second, newAcquirer(nil, time.Second, 0).timeout)
}

func TestContextErr(t *testing.T) {
	require.NoError(t, ContextErr(context.Background()))

	canceled, cancel := context.WithCancelCause(context.Background())
	custom := errors.New("caller's cause")
	cancel(custom)
	require.Equal(t, context.Canceled, ContextErr(canceled), "a caller's cause is not reported")

	stmtCtx, stop := context.WithTimeoutCause(context.Background(), time.Nanosecond, dbTimeoutCause)
	defer stop()
	visitor, stopVisitor := context.WithCancel(stmtCtx) // a visitor may derive its own context
	defer stopVisitor()
	<-visitor.Done()
	err := ContextErr(visitor)
	require.ErrorIs(t, err, ErrDBTimeout)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}
