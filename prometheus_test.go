package wpgx

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

// gatheredSeries returns the number of series of metric @p name for app @p app
// in the default registry.
func gatheredSeries(t *testing.T, name, app string) int {
	t.Helper()
	mfs, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	n := 0
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "app" && l.GetValue() == app {
					n++
				}
			}
		}
	}
	return n
}

func TestMetricSetsShareRegisteredCollectors(t *testing.T) {
	a := newMetricSet("metrics_test_a")
	a.Register()
	b := newMetricSet("metrics_test_b")
	b.Register()

	a.CountIntent("q", nil)
	b.CountIntent("q", nil)
	require.Equal(t, 1, gatheredSeries(t, "wpgx_intent_total", "metrics_test_a"))
	require.Equal(t, 1, gatheredSeries(t, "wpgx_intent_total", "metrics_test_b"),
		"the second set must record into the registered collectors")

	b.Unregister()
	b.Unregister() // idempotent: must not release a's reference
	require.Equal(t, 1, gatheredSeries(t, "wpgx_intent_total", "metrics_test_a"),
		"unregistering one set must not unregister collectors still in use")

	a.Unregister()
	require.Equal(t, 0, gatheredSeries(t, "wpgx_intent_total", "metrics_test_a"))

	// Registering again after all users left works from a clean slate.
	c := newMetricSet("metrics_test_c")
	c.Register()
	defer c.Unregister()
	c.CountIntent("q", nil)
	require.Equal(t, 1, gatheredSeries(t, "wpgx_intent_total", "metrics_test_c"))
	require.Equal(t, 0, gatheredSeries(t, "wpgx_intent_total", "metrics_test_b"))
}

func TestMetricSetUnregisterDropsOwnConnPoolGauges(t *testing.T) {
	a := newMetricSet("metrics_gauge_a")
	a.Register()
	defer a.Unregister()
	b := newMetricSet("metrics_gauge_b")
	b.Register()

	a.ConnPool.WithLabelValues(a.AppName, "max_conns", "primary").Set(1)
	b.ConnPool.WithLabelValues(b.AppName, "max_conns", "primary").Set(1)
	b.Unregister()
	require.Equal(t, 1, gatheredSeries(t, "wpgx_conn_pool", "metrics_gauge_a"))
	require.Equal(t, 0, gatheredSeries(t, "wpgx_conn_pool", "metrics_gauge_b"))
}

// histogramSum returns the sample count and sum of histogram @p name for app @p app
// and op @p op in the default registry.
func histogramSum(t *testing.T, name, app, op string) (uint64, float64) {
	t.Helper()
	mfs, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			if labels["app"] == app && labels["op"] == op {
				return m.GetHistogram().GetSampleCount(), m.GetHistogram().GetSampleSum()
			}
		}
	}
	return 0, 0
}

func TestLatencyKeepsSubMillisecondPrecision(t *testing.T) {
	m := newMetricSet("metrics_latency")
	m.Register()
	defer m.Unregister()

	m.observe("fast", nil, time.Now().Add(-1500*time.Microsecond), nil)
	count, sum := histogramSum(t, "wpgx_latency_milliseconds", "metrics_latency", "fast")
	require.Equal(t, uint64(1), count)
	require.Greater(t, sum, 1.5, "1.5ms must not be truncated to 1ms")
	require.Less(t, sum, 1000.0)
}
