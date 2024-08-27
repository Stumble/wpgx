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
	"golang.org/x/sync/errgroup"
)

const (
	transactionTraceSpanName = "$TX$"
)

// Pool is the wrapped pgx pool that registers Prometheus.
type Pool struct {
	pool         *pgxpool.Pool
	replicaPools map[ReplicaName]*pgxpool.Pool
	stats        *metricSet
	tracer       *tracer

	// graceful shutdown utilities
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type pgxConfig struct {
	Username        string
	Password        string
	Host            string
	Port            int
	DBName          string
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
	BeforeAcquire   func(context.Context, *pgx.Conn) bool
	IsProxy         bool
}

func newRawPgxPool(ctx context.Context, config *pgxConfig) (*pgxpool.Pool, error) {
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
	// Use Exec mode for db behind proxy, see:
	// https://github.com/jackc/pgx/blob/b197994b1f8e803940b05821957fea0ee5f82c04/doc.go#L189
	if config.IsProxy {
		pgConfig.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec
	}
	return pgxpool.NewWithConfig(ctx, pgConfig)
}

// NewPool creates a new Pool with the given context and config.
// When the context is canceled, the pool will be closed.
func NewPool(ctx context.Context, config *Config) (*Pool, error) {
	if err := config.Valid(); err != nil {
		return nil, err
	}
	primaryPool, err := newRawPgxPool(ctx, &pgxConfig{
		Username:        config.Username,
		Password:        config.Password,
		Host:            config.Host,
		Port:            config.Port,
		DBName:          config.DBName,
		MaxConns:        config.MaxConns,
		MinConns:        config.MinConns,
		MaxConnLifetime: config.MaxConnLifetime,
		MaxConnIdleTime: config.MaxConnIdleTime,
		BeforeAcquire:   config.BeforeAcquire,
		IsProxy:         config.IsProxy,
	})
	if err != nil {
		return nil, err
	}
	pool := &Pool{
		pool:         primaryPool,
		replicaPools: make(map[ReplicaName]*pgxpool.Pool),
	}
	for _, replicaConfig := range config.ReadReplicas {
		replicaPool, err := newRawPgxPool(ctx, &pgxConfig{
			Username:        replicaConfig.Username,
			Password:        replicaConfig.Password,
			Host:            replicaConfig.Host,
			Port:            replicaConfig.Port,
			DBName:          replicaConfig.DBName,
			MaxConns:        replicaConfig.MaxConns,
			MinConns:        replicaConfig.MinConns,
			MaxConnLifetime: replicaConfig.MaxConnLifetime,
			MaxConnIdleTime: replicaConfig.MaxConnIdleTime,
			BeforeAcquire:   replicaConfig.BeforeAcquire,
			IsProxy:         replicaConfig.IsProxy,
		})
		if err != nil {
			return nil, err
		}
		pool.replicaPools[replicaConfig.Name] = replicaPool
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

func (p *Pool) updateMetrics(_ context.Context) {
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
			var allStats []pgxPoolStat
			allStats = append(allStats, pgxPoolStat{replicaName: nil, stats: p.pool.Stat()})
			for replicaName, replicaPool := range p.replicaPools {
				name := replicaName
				allStats = append(allStats, pgxPoolStat{replicaName: &name, stats: replicaPool.Stat()})
			}
			p.stats.UpdateConnPoolGauge(allStats)
		}
	}
}

// Close closes all pools, spawned goroutines, and cancels the context.
func (p *Pool) Close() {
	for _, pp := range p.replicaPools {
		p.wg.Add(1)
		go func(replicaPool *pgxpool.Pool) {
			defer p.wg.Done()
			replicaPool.Close()
		}(pp)
	}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.pool.Close()
	}()
	p.cancel()
	p.wg.Wait()

	// unregister after all	go routines are closed.
	if p.stats != nil {
		p.stats.Unregister()
	}
}

// Ping pings all the instances in the pool, returns the first error encountered.
func (p *Pool) Ping(ctx context.Context) error {
	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		return p.pool.Ping(ctx)
	})
	for _, replicaPool := range p.replicaPools {
		replicaPool := replicaPool
		eg.Go(func() error {
			return replicaPool.Ping(ctx)
		})
	}
	return eg.Wait()
}

// PingPrimary pings the primary instance in the pool.
func (p *Pool) PingPrimary(ctx context.Context) error {
	return p.pool.Ping(ctx)
}

//// Connections

// WConn returns a wrapped connection for the primary instance.
func (p *Pool) WConn() *WConn {
	return &WConn{p: p.pool, stats: p.stats, tracer: p.tracer}
}

// WQuerier returns a wrapped querier based on the given replica name.
// When the name is nil, it returns the primary connection.
func (p *Pool) WQuerier(name *ReplicaName) (WQuerier, error) {
	if name == nil {
		return p.WConn(), nil
	}
	pp, ok := p.replicaPools[*name]
	if !ok {
		return nil, fmt.Errorf("%w, name: %s", ErrReplicaNotFound, *name)
	}
	return &WConn{p: pp, stats: p.stats, tracer: p.tracer, replicaName: name}, nil
}

// Transact is a wrapper of pgx.Transaction
// It acquires a connection from the Pool and starts a transaction with pgx.TxOptions determining the transaction mode.
// The context will be used when executing the transaction control statements (BEGIN, ROLLBACK, and COMMIT),
// and when if tracing is enabled, the context with transaction span will be passed down to @p fn.
func (p *Pool) Transact(ctx context.Context, txOptions pgx.TxOptions, fn TxFunc) (resp interface{}, err error) {
	if p.tracer != nil {
		ctx = p.tracer.TraceStart(ctx, transactionTraceSpanName, nil)
		defer p.tracer.TraceEnd(ctx, &err)
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

////// Getters of raw pgx pools.

// RawPool returns the raw primary pgx pool.
// Deprecated: For backward compatibility only, use RawPrimaryPool instead.
func (p *Pool) RawPool() *pgxpool.Pool {
	return p.pool
}

// RawPrimaryPool returns the raw primary pgx pool.
func (p *Pool) RawPrimaryPool() *pgxpool.Pool {
	return p.pool
}

// RawReplicaPools returns the raw replica pgx pools.
// NOTE: due to go's lack of constant qualifier, the returned map should be treated as read-only.
func (p *Pool) ReplicaPools() map[ReplicaName]*pgxpool.Pool {
	return p.replicaPools
}

// ReplicaPool returns the replica pool by name.
func (p *Pool) ReplicaPool(name ReplicaName) (pp *pgxpool.Pool, ok bool) {
	pp, ok = p.replicaPools[name]
	return
}

// MustReplicaPool returns the replica pool by name, panics if not found.
func (p *Pool) MustReplicaPool(name ReplicaName) *pgxpool.Pool {
	pp, ok := p.replicaPools[name]
	if !ok {
		panic(fmt.Sprintf("replica pool %s not found", name))
	}
	return pp
}
