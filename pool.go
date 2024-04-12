package wpgx

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

const (
	transactionTraceSpanName = "$TX$"
)

// Pool is the wrapped pgx pool that registers Prometheus.
type Pool struct {
	pool   *pgxpool.Pool
	stats  *metricSet
	tracer *tracer

	// graceful shutdown utilities
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newRawPgxPool(ctx context.Context, config *Config) (*pgxpool.Pool, error) {
	if config.MaxConns < HighQPSMaxOpenConns {
		log.Warn().Msgf(
			"WPgx pool config has MaxOpenConns = %d,"+
				"which may be too low to handle high QPS.", config.MaxConns)
	}
	pgConfig, err := pgxpool.ParseConfig(fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
		config.Username,
		config.Password,
		config.Host,
		config.Port,
		config.DBName))
	if err != nil {
		return nil, err
	}
	pgConfig.MaxConns = config.MaxConns
	pgConfig.MinConns = config.MinConns
	pgConfig.MaxConnLifetime = config.MaxConnLifetime
	pgConfig.MaxConnIdleTime = config.MaxConnIdleTime
	if config.BeforeAcquire != nil {
		pgConfig.BeforeAcquire = config.BeforeAcquire
	}
	return pgxpool.NewWithConfig(ctx, pgConfig)
}

func NewPool(ctx context.Context, config *Config) (*Pool, error) {
	if err := config.Valid(); err != nil {
		return nil, err
	}
	pgxpool, err := newRawPgxPool(ctx, config)
	if err != nil {
		return nil, err
	}
	pool := &Pool{
		pool: pgxpool,
	}
	if config.EnableTracing {
		pool.tracer = newTracer()
	}
	pool.ctx, pool.cancel = context.WithCancel(context.Background())
	if config.EnablePrometheus {
		pool.stats = newMetricSet(config.AppName)
		pool.stats.Register()
		pool.wg.Add(1)
		go pool.updateMetrics(ctx)
	}
	return pool, nil
}

func (p *Pool) updateMetrics(ctx context.Context) {
	defer p.wg.Done()
	ticker := time.NewTicker(connPoolUpdateInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
		case <-p.ctx.Done():
			return
		}
		if p.stats != nil {
			p.stats.UpdateConnPoolGauge(p.pool.Stat())
		}
	}
}

func (p *Pool) Close() {
	p.pool.Close()
	p.cancel()
	p.wg.Wait()

	// unregister after all	go routines are closed.
	if p.stats != nil {
		p.stats.Unregister()
	}
}

func (p *Pool) Ping(ctx context.Context) error {
	return p.pool.Ping(ctx)
}

func (p *Pool) WConn() *WConn {
	return &WConn{p: p}
}

// Transact is a wrapper of pgx.Transaction
// It acquires a connection from the Pool and starts a transaction with pgx.TxOptions determining the transaction mode.
// The context will be used when executing the transaction control statements (BEGIN, ROLLBACK, and COMMIT),
// and when if tracing is enabled, the context with transaction span will be passed down to @p fn.
func (p *Pool) Transact(ctx context.Context, txOptions pgx.TxOptions, fn TxFunc) (resp interface{}, err error) {
	if p.tracer != nil {
		ctx = p.tracer.TraceStart(ctx, transactionTraceSpanName)
		defer p.tracer.TraceEnd(ctx, err)
	}
	pgxTx, err := p.pool.BeginTx(ctx, txOptions)
	if err != nil {
		return nil, err
	}
	tx := &WTx{
		tx:     pgxTx,
		stats:  p.stats,
		tracer: p.tracer,
	}
	defer func() {
		rollbackErr := tx.Rollback(ctx)
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			err = rollbackErr
		}
	}()
	resp, err = fn(ctx, tx)
	if err != nil {
		return nil, err
	}
	err = tx.Commit(ctx)
	if err != nil {
		return nil, err
	}
	return resp, err
}

func (p *Pool) RawPool() *pgxpool.Pool {
	return p.pool
}
