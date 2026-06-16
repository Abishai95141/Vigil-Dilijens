package observe

import (
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/binding"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/qss"
)

// L6 freshness (doc 15 cap. A): the age-from-timestamp transform re-expresses a
// last-update epoch gauge as DATA AGE (evalNow − value) using the INJECTED eval clock.
// These tests pin the exact arithmetic, the injected-clock determinism (no time.Now),
// the negative-age safety (a future timestamp never fires), and the no-slope contract.

var freshNow = time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)

// freshReader serves one last-update-timestamp gauge sample for the pod uid.
type freshReader struct {
	uid, metric string
	epoch       float64 // the exposed Unix-epoch "last update" value
	sampleAt    time.Time
}

func (r freshReader) StreamsFor(uid, metric string) []string {
	if uid != r.uid || metric != r.metric {
		return nil
	}
	return []string{"s-fresh"}
}
func (r freshReader) StreamType(id string) (string, bool) { return "gauge", id == "s-fresh" }
func (r freshReader) Latest(id string) (qss.Sample, bool) {
	if id != "s-fresh" {
		return qss.Sample{}, false
	}
	return qss.Sample{At: r.sampleAt, Value: r.epoch}, true
}
func (r freshReader) LastN(id string, n int) []qss.Sample {
	s, ok := r.Latest(id)
	if !ok {
		return nil
	}
	return []qss.Sample{s}
}

func freshRule() *graph.ThresholdRule {
	return &graph.ThresholdRule{
		ID: "THR_APP_DATA_AGE", Signal: "SIG_app_data_freshness", Metric: "app_last_update_seconds",
		Kind: graph.RuleConfigRelative, ConfigPath: "slo.freshness.max_age", Factor: 1.0,
		Direction: "above", EntityScope: "Pod", Transform: graph.TransformAgeFromTimestamp, Window: "5m",
	}
}

func freshBinding() binding.Binding {
	return binding.Binding{
		CEIKey: "i|cl|traffic|Pod|agg-1|uid-agg", RoleKey: "r|cl|traffic|Deployment|Deployment/agg",
		Entity: "Pod", RuleID: "THR_APP_DATA_AGE", Metric: "app_last_update_seconds",
		State: binding.StateBound, Validation: binding.ValidationSuspect,
		Bar: &binding.ResolvedBar{
			Kind: graph.RuleConfigRelative, Source: binding.SourceConfig, Value: 30, Unit: "declared",
			Direction: "above", Factor: 1.0, Window: "5m",
		},
	}
}

func freshParams() FPParams {
	return FPParams{
		ScrapeInterval: 15 * time.Second, RateWindow: 5 * time.Minute,
		Watermark: 30 * time.Second, Band: 0.05, WellAboveFactor: 1.10,
		CooccurrenceWindow: 10 * time.Minute,
	}
}

// materialize one freshness binding for an exposed epoch; returns the single component.
func freshFP(t *testing.T, epoch float64) VariableThreshold {
	t.Helper()
	res := &binding.Result{Bindings: []binding.Binding{freshBinding()}}
	rules := map[string]*graph.ThresholdRule{"THR_APP_DATA_AGE": freshRule()}
	reader := freshReader{uid: "uid-agg", metric: "app_last_update_seconds", epoch: epoch, sampleAt: freshNow.Add(-5 * time.Second)}
	fps := Materialize(res, rules, reader, freshParams(), freshNow)
	if len(fps) != 1 || len(fps[0].Thresholds) != 1 {
		t.Fatalf("want exactly one fingerprint with one threshold, got %d fps", len(fps))
	}
	return fps[0].Thresholds[0]
}

// Stale data: last update 120s ago, freshness SLO 30s → age 120 crosses (well-above).
func TestAgeFromTimestampStaleCrosses(t *testing.T) {
	vt := freshFP(t, float64(freshNow.Unix())-120)
	if vt.Value != 120 {
		t.Errorf("age = %v, want 120 (evalNow − last_update)", vt.Value)
	}
	if !vt.State.Crossed() {
		t.Errorf("state = %s, want crossed (age 120 > SLO 30)", vt.State)
	}
	if vt.Deriv.How != "gauge-age-from-timestamp" {
		t.Errorf("derivation How = %q, want gauge-age-from-timestamp (the evidence trail)", vt.Deriv.How)
	}
	// No slope on an age variable (it would be just clock drift, meaningless).
	if vt.Slope != 0 || vt.SlopeSamples != 0 {
		t.Errorf("age must carry no slope, got slope=%v samples=%d", vt.Slope, vt.SlopeSamples)
	}
}

// Fresh data: last update 10s ago, SLO 30s → age 10 stays below the bar (silent).
func TestAgeFromTimestampFreshStaysBelow(t *testing.T) {
	vt := freshFP(t, float64(freshNow.Unix())-10)
	if vt.Value != 10 {
		t.Errorf("age = %v, want 10", vt.Value)
	}
	if vt.State.Crossed() {
		t.Errorf("state = %s, want NOT crossed (age 10 < SLO 30)", vt.State)
	}
}

// Future timestamp (clock skew): age is negative → never fires, never fabricated.
func TestAgeFromTimestampFutureIsNegativeAndSafe(t *testing.T) {
	vt := freshFP(t, float64(freshNow.Unix())+60)
	if vt.Value != -60 {
		t.Errorf("age = %v, want -60 (a future last_update)", vt.Value)
	}
	if vt.State.Crossed() {
		t.Errorf("a negative age must land on the healthy side, got %s", vt.State)
	}
}

// Soundness edge: even if the app MIS-EXPOSES its last-update gauge as a Prometheus
// counter, the AUTHORED transform intent wins — the value is still aged, never silently
// rate-converted (the counter branch is excluded for transform rules).
func TestAgeFromTimestampWinsOverMisTypedCounter(t *testing.T) {
	res := &binding.Result{Bindings: []binding.Binding{freshBinding()}}
	rules := map[string]*graph.ThresholdRule{"THR_APP_DATA_AGE": freshRule()}
	// A reader that reports the stream as a counter (the mis-type) while the rule carries
	// the age-from-timestamp transform — the transform must still win.
	cr := counterTypedReader{freshReader{uid: "uid-agg", metric: "app_last_update_seconds",
		epoch: float64(freshNow.Unix()) - 120, sampleAt: freshNow.Add(-5 * time.Second)}}
	fps := Materialize(res, rules, cr, freshParams(), freshNow)
	if len(fps) != 1 || len(fps[0].Thresholds) != 1 {
		t.Fatalf("want one fingerprint/threshold, got %d fps", len(fps))
	}
	vt := fps[0].Thresholds[0]
	if vt.Value != 120 || vt.Deriv.How != "gauge-age-from-timestamp" {
		t.Errorf("mis-typed counter must still AGE (value=120, how=gauge-age-from-timestamp), got value=%v how=%q", vt.Value, vt.Deriv.How)
	}
}

// counterTypedReader reports the stream as a counter (the mis-type) but serves the same sample.
type counterTypedReader struct{ freshReader }

func (r counterTypedReader) StreamType(id string) (string, bool) { return "counter", id == "s-fresh" }

// Determinism: the age depends ONLY on the injected evalNow and the gauge — re-running
// at the SAME injected clock reproduces the SAME age, regardless of wall time.
func TestAgeFromTimestampDeterministic(t *testing.T) {
	a := freshFP(t, float64(freshNow.Unix())-77)
	b := freshFP(t, float64(freshNow.Unix())-77)
	if a.Value != b.Value || a.State != b.State {
		t.Errorf("non-deterministic age: %v/%s vs %v/%s", a.Value, a.State, b.Value, b.State)
	}
	if a.Value != 77 {
		t.Errorf("age = %v, want 77", a.Value)
	}
}
