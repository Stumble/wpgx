package wpgx

import (
	"errors"
	"sync"
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

	// registered holds the collectors this set took a reference on in Register.
	registered []prometheus.Collector
}

// The default registry identifies collectors by metric name and labels, so all
// metricSets in a process must share one set of collectors. registryRefs counts
// the metricSets using each collector that wpgx registered; a collector is
// unregistered only when its last user unregisters.
var (
	registryMu   sync.Mutex
	registryRefs = make(map[prometheus.Collector]int)
)

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
	registryMu.Lock()
	defer registryMu.Unlock()
	var failed []string
	m.ConnPool = register(m, m.ConnPool, "ConnPool gauges", &failed)
	m.Request = register(m, m.Request, "Request counters", &failed)
	m.Latency = register(m, m.Latency, "Latency histogram", &failed)
	m.Intent = register(m, m.Intent, "Intent counters", &failed)
	m.Error = register(m, m.Error, "Error counters", &failed)
	if len(failed) > 0 {
		log.Error().Msgf("failed to register Prometheus metrics: %v", failed)
	}
}

// register registers c with the default registry and returns the collector m
// should record to: c itself, or the equivalent collector that is already
// registered, e.g. by another Pool. registryMu must be held.
func register[T prometheus.Collector](m *metricSet, c T, name string, failed *[]string) T {
	err := prometheus.Register(c)
	if err == nil {
		registryRefs[c]++
		m.registered = append(m.registered, c)
		return c
	}
	var are prometheus.AlreadyRegisteredError
	if errors.As(err, &are) {
		if existing, ok := are.ExistingCollector.(T); ok {
			// Only share the reference count of collectors wpgx registered;
			// a collector registered by someone else is never unregistered here.
			if _, owned := registryRefs[existing]; owned {
				registryRefs[existing]++
				m.registered = append(m.registered, existing)
			}
			return existing
		}
	}
	*failed = append(*failed, name)
	return c
}

// Unregister releases the collectors taken in Register. It is idempotent.
func (m *metricSet) Unregister() {
	registryMu.Lock()
	defer registryMu.Unlock()
	if len(m.registered) > 0 && m.ConnPool != nil {
		// The gauges may outlive this set when shared; drop this app's stale values.
		m.ConnPool.DeletePartialMatch(prometheus.Labels{"app": m.AppName})
	}
	for _, c := range m.registered {
		registryRefs[c]--
		if registryRefs[c] <= 0 {
			delete(registryRefs, c)
			prometheus.Unregister(c)
		}
	}
	m.registered = nil
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

// CountError counts an error that surfaced after the observed call returned,
// e.g. from rows.Err() or Row.Scan.
func (s *metricSet) CountError(name string, replicaName *ReplicaName) {
	if s.Error != nil {
		s.Error.WithLabelValues(s.AppName, name, toLabel(replicaName)).Inc()
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
