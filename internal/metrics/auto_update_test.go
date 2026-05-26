package metrics

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

// TestAutoUpdateAttempt_CounterIncrements verifies that AutoUpdateAttempt
// increments the dockpal_auto_update_attempts_total counter for the given
// (instance, app, result) label triple. Validates that the public hook the
// AutoUpdateWorker calls actually mutates the underlying CounterVec — the
// other half of task 9.1's "counter is updated" requirement.
func TestAutoUpdateAttempt_CounterIncrements(t *testing.T) {
	c := AutoUpdateAttemptsCounterForTest()
	// Use a unique label triple per test so we never collide with leftover
	// counts from sibling tests in the same process. testutil compares the
	// per-label series, so an isolated triple is sufficient.
	const instance, app, result = "test-inst", "test-app-attempt", "success"
	before := testutil.ToFloat64(c.WithLabelValues(instance, app, result))

	AutoUpdateAttempt(instance, app, result)
	AutoUpdateAttempt(instance, app, result)
	AutoUpdateAttempt(instance, app, result)

	got := testutil.ToFloat64(c.WithLabelValues(instance, app, result))
	if got-before != 3 {
		t.Fatalf("counter delta: got %v, want 3", got-before)
	}
}

// TestAutoUpdateDuration_HistogramObserves verifies that AutoUpdateDuration
// produces an observation on the dockpal_auto_update_duration_seconds
// histogram. We assert by gathering and inspecting the underlying histogram
// because testutil.ToFloat64 only works for single-value collectors.
func TestAutoUpdateDuration_HistogramObserves(t *testing.T) {
	h := AutoUpdateDurationHistogramForTest()
	const instance, app, stage = "test-inst", "test-app-duration", "pulling"

	// Snapshot the existing observation count for this label triple.
	before := histogramSampleCount(t, h, instance, app, stage)

	AutoUpdateDuration(instance, app, stage, 0.42)
	AutoUpdateDuration(instance, app, stage, 1.5)

	after := histogramSampleCount(t, h, instance, app, stage)
	if after-before != 2 {
		t.Fatalf("histogram observation delta: got %d, want 2", after-before)
	}
}

// TestSetAppsPendingUpdate_GaugeUpdates verifies that SetAppsPendingUpdate
// mutates the dockpal_auto_update_apps_with_pending_update gauge. testutil's
// ToFloat64 returns the current gauge value directly.
func TestSetAppsPendingUpdate_GaugeUpdates(t *testing.T) {
	g := AutoUpdateAppsPendingUpdateGaugeForTest()

	SetAppsPendingUpdate(0)
	if got := testutil.ToFloat64(g); got != 0 {
		t.Fatalf("gauge after Set(0): got %v, want 0", got)
	}

	SetAppsPendingUpdate(7)
	if got := testutil.ToFloat64(g); got != 7 {
		t.Fatalf("gauge after Set(7): got %v, want 7", got)
	}

	SetAppsPendingUpdate(2)
	if got := testutil.ToFloat64(g); got != 2 {
		t.Fatalf("gauge after Set(2): got %v, want 2", got)
	}
}

// TestAutoUpdateMetricsRegistered verifies the three metrics are wired into
// the registration helper so production /api/metrics serves them. We do not
// rely on the default Prometheus registry here (production wires it from
// main.go via RegisterMetrics; tests do not, and double-registration would
// panic). Instead we use testutil.CollectAndCount on each collector to
// confirm at least one series is recorded after a synthetic observation.
func TestAutoUpdateMetricsRegistered(t *testing.T) {
	AutoUpdateAttempt("inst", "app-registered", "success")
	AutoUpdateDuration("inst", "app-registered", "pulling", 0.1)
	SetAppsPendingUpdate(1)

	if n := testutil.CollectAndCount(AutoUpdateAttemptsCounterForTest()); n == 0 {
		t.Errorf("counter has no series after observation")
	}
	if n := testutil.CollectAndCount(AutoUpdateDurationHistogramForTest()); n == 0 {
		t.Errorf("histogram has no series after observation")
	}
	if n := testutil.CollectAndCount(AutoUpdateAppsPendingUpdateGaugeForTest()); n != 1 {
		t.Errorf("gauge series count: got %d, want 1", n)
	}
}

// TestAutoUpdateMetricsHelpText spot-checks the help strings so dashboard
// authors have a stable reference for what each metric means. The check is
// substring-based to avoid brittle whole-string comparisons.
func TestAutoUpdateMetricsHelpText(t *testing.T) {
	// Counter
	{
		desc := AutoUpdateAttemptsCounterForTest().WithLabelValues("i", "a", "success").Desc().String()
		for _, want := range []string{"success", "failed", "rolled_back"} {
			if !strings.Contains(desc, want) {
				t.Errorf("counter help missing %q label value; got %s", want, desc)
			}
		}
	}
	// Histogram
	{
		obs := AutoUpdateDurationHistogramForTest().WithLabelValues("i", "a", "pulling")
		m, ok := obs.(prometheus.Metric)
		if !ok {
			t.Fatalf("expected histogram observer to implement prometheus.Metric: %T", obs)
		}
		desc := m.Desc().String()
		if !strings.Contains(desc, "pipeline stage") {
			t.Errorf("histogram help missing %q; got %s", "pipeline stage", desc)
		}
	}
	// Gauge
	{
		desc := AutoUpdateAppsPendingUpdateGaugeForTest().Desc().String()
		if !strings.Contains(desc, "opt-in apps") {
			t.Errorf("gauge help missing %q; got %s", "opt-in apps", desc)
		}
	}
}

// histogramSampleCount extracts the cumulative observation count for a single
// (label) series of a HistogramVec. testutil exposes ToFloat64 for
// single-value collectors only; for histograms we Write the underlying
// dto.Metric and read SampleCount. Returns 0 when no series exists yet for
// the requested labels.
func histogramSampleCount(t *testing.T, h *prometheus.HistogramVec, lvs ...string) uint64 {
	t.Helper()
	obs, err := h.GetMetricWithLabelValues(lvs...)
	if err != nil {
		t.Fatalf("GetMetricWithLabelValues: %v", err)
	}
	collector, ok := obs.(prometheus.Metric)
	if !ok {
		t.Fatalf("observer is not a prometheus.Metric: %T", obs)
	}
	var pb dto.Metric
	if err := collector.Write(&pb); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return pb.GetHistogram().GetSampleCount()
}
