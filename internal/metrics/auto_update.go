package metrics

import "github.com/prometheus/client_golang/prometheus"

// Auto-update worker metrics (requirements R10.1-R10.3 of the auto-image-update
// spec). The variables are package-private to match the existing style in
// prometheus.go; they are wired into the default registry from
// registerAutoUpdateMetrics(), called by RegisterMetrics. Test helpers below
// expose the underlying collectors so callers can use prometheus/testutil
// without needing to share the default registry.
var (
	// autoUpdateAttempts counts every auto-update attempt by terminal result.
	// Labels match the spec: instance is the AutoUpdateWorker.instanceID
	// (empty / "local" for the edge process), app is the compose project,
	// result is one of: success, failed, rolled_back, skipped_cooldown,
	// skipped_window, update_already_running.
	autoUpdateAttempts = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "dockpal_auto_update_attempts_total",
			Help: "Total auto-update attempts by instance, app, and result. " +
				"result is one of: success, failed, rolled_back, " +
				"skipped_cooldown, skipped_window, update_already_running.",
		},
		[]string{"instance", "app", "result"},
	)

	// autoUpdateDuration measures the wall-clock duration spent in each stage
	// of the pipeline. The stage label uses the same values as the persisted
	// AppUpdateRecord stage (pulling, recreating, verifying), so an observer
	// can answer "how long does pulling typically take for app X" directly.
	autoUpdateDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "dockpal_auto_update_duration_seconds",
			Help:    "Duration of each auto-update pipeline stage in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"instance", "app", "stage"},
	)

	// autoUpdateAppsPendingUpdate is the count of opt-in apps that currently
	// have at least one image with a pending update. Updated by the worker's
	// cycle listener after every ImageUpdateMonitor.checkAll() pass, so the
	// gauge converges to the live state without polling.
	autoUpdateAppsPendingUpdate = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "dockpal_auto_update_apps_with_pending_update",
			Help: "Number of opt-in apps with at least one image waiting to be auto-updated.",
		},
	)
)

// AutoUpdateAttempt increments the auto-update attempts counter for the given
// (instance, app, result). The result label MUST be one of the strings listed
// in the autoUpdateAttempts help text; passing an unrecognized value still
// works (Prometheus accepts arbitrary label values) but breaks downstream
// dashboards.
func AutoUpdateAttempt(instance, app, result string) {
	autoUpdateAttempts.WithLabelValues(instance, app, result).Inc()
}

// AutoUpdateDuration observes a single pipeline stage's duration. The seconds
// argument is the elapsed wall-clock time spent in that stage; callers
// typically derive it from time.Since(stageStartedAt).Seconds().
func AutoUpdateDuration(instance, app, stage string, seconds float64) {
	autoUpdateDuration.WithLabelValues(instance, app, stage).Observe(seconds)
}

// SetAppsPendingUpdate publishes the latest "apps waiting to update" count as
// the gauge value. Called by the AutoUpdateWorker's cycle listener with the
// number of opt-in apps that have at least one image marked HasUpdate=true.
func SetAppsPendingUpdate(n int) {
	autoUpdateAppsPendingUpdate.Set(float64(n))
}

// registerAutoUpdateMetrics wires the auto-update metrics into the default
// prometheus.Registerer. Invoked from RegisterMetrics so the existing single
// registration path keeps managing every metric in one place.
func registerAutoUpdateMetrics() {
	prometheus.MustRegister(
		autoUpdateAttempts,
		autoUpdateDuration,
		autoUpdateAppsPendingUpdate,
	)
}

// AutoUpdateAttemptsCounterForTest returns the underlying CounterVec so unit
// tests can assert specific label values via prometheus/testutil. Production
// callers should use AutoUpdateAttempt instead.
func AutoUpdateAttemptsCounterForTest() *prometheus.CounterVec {
	return autoUpdateAttempts
}

// AutoUpdateDurationHistogramForTest returns the underlying HistogramVec so
// unit tests can assert that observations were recorded. Production callers
// should use AutoUpdateDuration instead.
func AutoUpdateDurationHistogramForTest() *prometheus.HistogramVec {
	return autoUpdateDuration
}

// AutoUpdateAppsPendingUpdateGaugeForTest returns the underlying Gauge so
// unit tests can read the published value via prometheus/testutil. Production
// callers should use SetAppsPendingUpdate instead.
func AutoUpdateAppsPendingUpdateGaugeForTest() prometheus.Gauge {
	return autoUpdateAppsPendingUpdate
}
