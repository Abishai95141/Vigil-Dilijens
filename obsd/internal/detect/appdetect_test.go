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

// appFP is an aggregation pod's fingerprint carrying ONE app threshold variable at a
// given ladder state, config-sourced (the declared SLO). `how` records the derivation
// (gauge-level / gauge-age-from-timestamp / counter-rate) for evidence-trail realism.
func appFP(ruleID, metric, how string, state observe.ThresholdState) observe.Fingerprint {
	return observe.Fingerprint{
		CEIKey: "i|cl|shop|Pod|agg-a|uid-agg", Namespace: "shop", Name: "agg-a", Kind: "Pod",
		EvaluatedAt: evalAt,
		Thresholds: []observe.VariableThreshold{{
			RuleID: ruleID, Metric: metric,
			State: state, BarSource: "config", Flagged: false,
			Deriv: observe.DerivationRef{StreamID: "s-app", SampleAt: evalAt, How: how},
		}},
	}
}

// appQueueFP is the L4 keystone fingerprint (kept for the existing queue tests).
func appQueueFP(state observe.ThresholdState) observe.Fingerprint {
	return appFP("THR_APP_QUEUE_DEPTH", "app_queue_depth", "gauge-level", state)
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

// L6 — data staleness: the data AGE (evalNow − last_update) crossing the declared
// freshness SLO produces a MEASURED finding for PHEN_APP_DATA_STALENESS. The terminal
// link of the smart-traffic chain (and the differentiator: SLO-relative staleness).
func TestAppDataStalenessFiresOnCrossedSLO(t *testing.T) {
	m := NewMatcher(loadAppGraph(t))
	fp := appFP("THR_APP_DATA_AGE", "app_last_update_seconds", "gauge-age-from-timestamp", observe.StateAbove)
	f := findPhen(m.MatchFingerprint(fp), "PHEN_APP_DATA_STALENESS")
	if f == nil {
		t.Fatalf("PHEN_APP_DATA_STALENESS must fire when data age crosses its declared freshness SLO")
	}
	if f.Quality != QualityFull || f.RequiredMet != 1 || f.RequiredTotal != 1 {
		t.Errorf("finding = quality %s required %d/%d, want full 1/1", f.Quality, f.RequiredMet, f.RequiredTotal)
	}
	if len(f.Members) != 1 || f.Members[0].Metric != "app_last_update_seconds" || f.Members[0].BarFlagged {
		t.Errorf("member evidence = %+v, want the app_last_update_seconds crossing on a config bar", f.Members)
	}
	// Cross-talk guard: a staleness fingerprint must NOT light up queue/load.
	if findPhen(m.MatchFingerprint(fp), "PHEN_APP_QUEUE_SATURATION") != nil ||
		findPhen(m.MatchFingerprint(fp), "PHEN_APP_LOAD_SURGE") != nil {
		t.Errorf("a staleness variable must not fire queue or load phenomena (independence)")
	}
}

// Fresh data (age below the freshness SLO) must STAY SILENT.
func TestAppDataStalenessSilentWhenFresh(t *testing.T) {
	m := NewMatcher(loadAppGraph(t))
	fp := appFP("THR_APP_DATA_AGE", "app_last_update_seconds", "gauge-age-from-timestamp", observe.StateBelow)
	if f := findPhen(m.MatchFingerprint(fp), "PHEN_APP_DATA_STALENESS"); f != nil {
		t.Fatalf("fresh data (age under the SLO) must not fire; got %+v", f)
	}
}

// L1 — load surge: the request rate crossing the declared capacity SLO produces a
// MEASURED finding for PHEN_APP_LOAD_SURGE. The chain trigger (the 3× load).
func TestAppLoadSurgeFiresOnCrossedSLO(t *testing.T) {
	m := NewMatcher(loadAppGraph(t))
	fp := appFP("THR_APP_REQUEST_RATE", "app_requests_total", "counter-rate", observe.StateWellAbove)
	f := findPhen(m.MatchFingerprint(fp), "PHEN_APP_LOAD_SURGE")
	if f == nil {
		t.Fatalf("PHEN_APP_LOAD_SURGE must fire when request rate crosses its declared capacity SLO")
	}
	if f.Quality != QualityFull || f.RequiredMet != 1 || f.RequiredTotal != 1 {
		t.Errorf("finding = quality %s required %d/%d, want full 1/1", f.Quality, f.RequiredMet, f.RequiredTotal)
	}
	if len(f.Members) != 1 || f.Members[0].Metric != "app_requests_total" || f.Members[0].BarFlagged {
		t.Errorf("member evidence = %+v, want the app_requests_total crossing on a config bar", f.Members)
	}
}

// Request rate under the declared capacity must STAY SILENT.
func TestAppLoadSurgeSilentUnderCapacity(t *testing.T) {
	m := NewMatcher(loadAppGraph(t))
	fp := appFP("THR_APP_REQUEST_RATE", "app_requests_total", "counter-rate", observe.StateBelow)
	if f := findPhen(m.MatchFingerprint(fp), "PHEN_APP_LOAD_SURGE"); f != nil {
		t.Fatalf("request rate under capacity must not fire; got %+v", f)
	}
}
