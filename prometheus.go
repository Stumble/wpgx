package wpgx

import (
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
)

type metricSet struct {
	AppName  string
	ConnPool *prometheus.GaugeVec
	Request  *prometheus.CounterVec
	Latency  *prometheus.HistogramVec
	Intent   *prometheus.CounterVec
	Error    *prometheus.CounterVec
}

var (
	labels        = []string{"app", "op", "replica"}
	latencyBucket = []float64{
		4, 8, 16, 32, 64, 128, 256, 512, 1024, 2 * 1024, 4 * 1024, 8 * 1024, 16 * 1024, 32 * 1024}
	connPoolUpdateInterval = 3 * time.Second
)

func newMetricSet(appName string) *metricSet {
	return &metricSet{
		AppName: appName,
		ConnPool: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "wpgx_conn_pool",
				Help: "connection pool status",
			}, labels),
		Request: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "wpgx_request_total",
				Help: "how many CRUD operations sent to DB.",
			}, labels),
		Latency: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "wpgx_latency_milliseconds",
				Help:    "CRUD latency in milliseconds",
				Buckets: latencyBucket,
			}, labels),
		Intent: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "wpgx_intent_total",
				Help: "how many intent queries invoked, should be the sum of cached + hit_db.",
			}, labels),
		Error: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "wpgx_error_total",
				Help: "how many errors were generated for this app and op.",
			}, labels),
	}
}

func (m *metricSet) Register() {
	var failed []string
	if err := prometheus.Register(m.ConnPool); err != nil {
		failed = append(failed, "ConnPool gauges")
	}
	if err := prometheus.Register(m.Request); err != nil {
		failed = append(failed, "Request counters")
	}
	if err := prometheus.Register(m.Latency); err != nil {
		failed = append(failed, "Latency histogram")
	}
	if err := prometheus.Register(m.Intent); err != nil {
		failed = append(failed, "Intent counters")
	}
	if err := prometheus.Register(m.Error); err != nil {
		failed = append(failed, "Error counters")
	}
	if len(failed) > 0 {
		log.Error().Msgf("failed to register Prometheus metrics: %v", failed)
	}
}

func (m *metricSet) Unregister() {
	prometheus.Unregister(m.ConnPool)
	prometheus.Unregister(m.Request)
	prometheus.Unregister(m.Latency)
	prometheus.Unregister(m.Intent)
	prometheus.Unregister(m.Error)
}

func (s *metricSet) MakeObserver(name string, replicaName *ReplicaName, startedAt time.Time, errPtr *error) func() {
	return func() {
		if s.Request != nil {
			s.Request.WithLabelValues(s.AppName, name, toLabel(replicaName)).Inc()
		}
		if s.Error != nil && errPtr != nil && *errPtr != nil {
			s.Error.WithLabelValues(s.AppName, name, toLabel(replicaName)).Inc()
		}
		if s.Latency != nil {
			s.Latency.WithLabelValues(s.AppName, name, toLabel(replicaName)).Observe(
				float64(time.Since(startedAt).Milliseconds()))
		}
	}
}

func (s *metricSet) CountIntent(name string, replicaName *ReplicaName) {
	if s.Intent != nil {
		s.Intent.WithLabelValues(s.AppName, name, toLabel(replicaName)).Inc()
	}
}

type pgxPoolStat struct {
	replicaName *ReplicaName
	stats       *pgxpool.Stat
}

func (s *metricSet) UpdateConnPoolGauge(statsList []pgxPoolStat) {
	if s.ConnPool != nil {
		for _, poolStats := range statsList {
			stats := poolStats.stats
			replicaName := poolStats.replicaName
			s.ConnPool.WithLabelValues(s.AppName, "max_conns", toLabel(replicaName)).Set(float64(stats.MaxConns()))
			s.ConnPool.WithLabelValues(s.AppName, "total_conns", toLabel(replicaName)).Set(float64(stats.TotalConns()))
			s.ConnPool.WithLabelValues(s.AppName, "idle_conns", toLabel(replicaName)).Set(float64(stats.IdleConns()))
			s.ConnPool.WithLabelValues(s.AppName, "acquired_conns", toLabel(replicaName)).Set(float64(stats.AcquiredConns()))
			s.ConnPool.WithLabelValues(s.AppName, "constructing_conns", toLabel(replicaName)).Set(
				float64(stats.ConstructingConns()))
		}
	}
}
