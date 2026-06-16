package detect

import (
	"testing"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/observe"
)

const appOverlayPath = "../../../ontology/graph/overlays/experimental/app-conditions-v1.yaml"

// loadAppGraph loads the base KG + the experimental app overlay (doc 15 cap. A) — the
// graph obsd uses behind --app-metrics-enabled.
func loadAppGraph(t *testing.T) *graph.Graph {
	t.Helper()
	g, err := graph.LoadWithOverlayPaths(kgPath, []string{appOverlayPath})
	if err != nil {
		t.Fatalf("LoadWithOverlayPaths(app): %v", err)
	}
	return g
}

// appQueueFP is an aggregation pod's fingerprint carrying the app_queue_depth threshold
// variable at a given ladder state (config-sourced bar = the declared SLO).
func appQueueFP(state observe.ThresholdState) observe.Fingerprint {
	return observe.Fingerprint{
		CEIKey: "i|cl|shop|Pod|agg-a|uid-agg", Namespace: "shop", Name: "agg-a", Kind: "Pod",
		EvaluatedAt: evalAt,
		Thresholds: []observe.VariableThreshold{{
			RuleID: "THR_APP_QUEUE_DEPTH", Metric: "app_queue_depth",
			State: state, BarSource: "config", Flagged: false,
			Deriv: observe.DerivationRef{StreamID: "s-app", SampleAt: evalAt, How: "gauge-level"},
		}},
	}
}

// The keystone end-to-end: an app's queue depth crossing its DECLARED SLO produces a
// MEASURED finding for PHEN_APP_QUEUE_SATURATION. This is the first application-level
// phenomenon Vigil can detect — the L4 link of the smart-traffic chain.
func TestAppQueueSaturationFiresOnCrossedSLO(t *testing.T) {
	m := NewMatcher(loadAppGraph(t))
	out := m.MatchFingerprint(appQueueFP(observe.StateAbove))
	f := findPhen(out, "PHEN_APP_QUEUE_SATURATION")
	if f == nil {
		t.Fatalf("PHEN_APP_QUEUE_SATURATION must fire when the queue crosses its declared SLO; findings: %+v", out)
	}
	if f.Quality != QualityFull || f.RequiredMet != 1 || f.RequiredTotal != 1 {
		t.Errorf("finding = quality %s required %d/%d, want full 1/1", f.Quality, f.RequiredMet, f.RequiredTotal)
	}
	if len(f.Members) != 1 || f.Members[0].Metric != "app_queue_depth" {
		t.Errorf("member evidence = %+v, want the app_queue_depth crossing", f.Members)
	}
	// The bar is config-sourced (borrowed normativity), never a flagged default.
	if f.Members[0].BarFlagged {
		t.Errorf("the SLO bar must be config-sourced (not flagged) — borrowed normativity")
	}
}

// Below the declared SLO (the healthy approach band) it must STAY SILENT — no finding,
// no fabricated alarm.
func TestAppQueueSaturationSilentUnderSLO(t *testing.T) {
	m := NewMatcher(loadAppGraph(t))
	out := m.MatchFingerprint(appQueueFP(observe.StateAtThreshold)) // below the bar
	if f := findPhen(out, "PHEN_APP_QUEUE_SATURATION"); f != nil {
		t.Fatalf("queue UNDER the declared SLO must not fire; got %+v", f)
	}
}

// No app fingerprint variable at all (the SLO was never declared, so the bar is
// unbounded and the materializer produced nothing) ⇒ no finding, never invented.
func TestAppQueueSaturationSilentWhenUnobserved(t *testing.T) {
	m := NewMatcher(loadAppGraph(t))
	bare := observe.Fingerprint{
		CEIKey: "i|cl|shop|Pod|agg-a|uid-agg", Namespace: "shop", Name: "agg-a", Kind: "Pod", EvaluatedAt: evalAt,
	}
	if f := findPhen(m.MatchFingerprint(bare), "PHEN_APP_QUEUE_SATURATION"); f != nil {
		t.Fatalf("no app variable ⇒ no finding; got %+v", f)
	}
}
