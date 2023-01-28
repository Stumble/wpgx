package wpgx

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
)

type metricSet struct {
	ConnPool *prometheus.GaugeVec
	Request  *prometheus.CounterVec
	Latency  *prometheus.HistogramVec
}

var (
	labels        = []string{"name"}
	latencyBucket = []float64{
		4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192}
	connPoolUpdateInterval = 1 * time.Second
)

func newMetricSet(appName string) *metricSet {
	return &metricSet{
		ConnPool: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: fmt.Sprintf("%s_wpgx_conn_pool", appName),
				Help: "",
			}, labels),
		Request: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: fmt.Sprintf("%s_request_total", appName),
				Help: "how many CRUD operations sent to DB.",
			}, labels),
		Latency: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    fmt.Sprintf("%s_wpgx_latency_milliseconds", appName),
				Help:    "CRUD latency in milliseconds",
				Buckets: latencyBucket,
			}, labels),
	}
}
func (m *metricSet) Register() {
	err := prometheus.Register(m.ConnPool)
	if err != nil {
		log.Err(err).Msgf("failed to register Prometheus ConnPool gauges")
	}
	err = prometheus.Register(m.Request)
	if err != nil {
		log.Err(err).Msgf("failed to register prometheus Request counters")
	}
	err = prometheus.Register(m.Latency)
	if err != nil {
		log.Err(err).Msgf("failed to register Prometheus Latency histogram")
	}
}

func (m *metricSet) Unregister() {
	prometheus.Unregister(m.ConnPool)
	prometheus.Unregister(m.Request)
	prometheus.Unregister(m.Latency)
}

func (s *metricSet) Observe(name string, startedAt time.Time) func() {
	return func() {
		if s.Request != nil {
			s.Request.WithLabelValues(name).Inc()
		}
		if s.Latency != nil {
			s.Latency.WithLabelValues(name).Observe(
				float64(time.Since(startedAt).Milliseconds()))
		}
	}
}

// Pool is the wrapped pgx pool that registers Prometheus.
type Pool struct {
	pool  *pgxpool.Pool
	stats *metricSet

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
	if config.EnableTracing {
		pgConfig.ConnConfig.Tracer = otelpgx.NewTracer(otelpgx.WithIncludeQueryParameters())
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
		stats := p.pool.Stat()
		p.stats.ConnPool.WithLabelValues("max_conns").Set(float64(stats.MaxConns()))
		p.stats.ConnPool.WithLabelValues("total_conns").Set(float64(stats.TotalConns()))
		p.stats.ConnPool.WithLabelValues("idle_conns").Set(float64(stats.IdleConns()))
		p.stats.ConnPool.WithLabelValues("acquired_conns").Set(float64(stats.AcquiredConns()))
		p.stats.ConnPool.WithLabelValues("constructing_conns").Set(
			float64(stats.ConstructingConns()))
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
// The context will be used when executing the transaction control statements (BEGIN, ROLLBACK, and COMMIT)
// but does not otherwise affect the execution of fn.
func (p *Pool) Transact(ctx context.Context, txOptions pgx.TxOptions, fn TxFunc) (resp interface{}, err error) {
	pgxTx, e := p.pool.BeginTx(ctx, txOptions)
	if e != nil {
		return nil, e
	}
	tx := &WTx{
		tx:    pgxTx,
		stats: p.stats,
	}
	defer func() {
		rollbackErr := tx.Rollback(ctx)
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			err = rollbackErr
		}
	}()
	resp, err = fn(tx)
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
