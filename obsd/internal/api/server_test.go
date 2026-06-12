package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/binding"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/selection"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/store"
)

var at = time.Date(2026, 6, 12, 9, 0, 0, 0, time.UTC)

func TestCoverageUnavailableIsHonest(t *testing.T) {
	v := BuildCoverage("cl", "sha256:abc", at, nil, nil, nil)
	if v.Available {
		t.Error("nil binding result must yield an unavailable (not all-zero) view")
	}
	if len(v.Caveats) == 0 {
		t.Error("the unavailable state must state why")
	}
}

func TestCoverageComposes(t *testing.T) {
	res := &binding.Result{
		Coverage: binding.CoverageReport{
			Resolvability: 0.91, ConfigBound: 10, ConfigEligible: 11, DefaultBars: 2,
			UnboundedWorkloads: []string{"paymentservice: cpu throttle (no limit)"},
			Validation:         binding.QASummary{Verified: 60, Suspect: 4},
			PerRule: []binding.RuleCoverage{
				{RuleID: "THR_B", Instantiated: 3, ConfigBound: 3},
				{RuleID: "THR_A", Instantiated: 5, ConfigBound: 4, Unbounded: 1},
			},
			Notes: []string{"node rules out-of-scope: node-exporter absent"},
		},
	}
	obs := &binding.ObservabilityReport{
		Full: 1, Partial: 1, None: 1,
		PerPhenomenon: []binding.PhenomenonCoverage{
			{PhenomenonID: "PHEN_FULL", Observability: "full", RequiredTotal: 1, RequiredObtainable: 1},
			{PhenomenonID: "PHEN_NONE", Observability: "none", RequiredTotal: 2, MissingReasons: []string{"node-exporter absent"}},
			{PhenomenonID: "PHEN_PART", Observability: "partial", RequiredTotal: 2, RequiredObtainable: 1},
		},
	}
	sel := &selection.Result{TierACount: 16, NoneByReason: map[selection.Reason]int{selection.ReasonNoEvaluableVariable: 12}}
	sel.Records = make([]selection.Record, 28)

	v := BuildCoverage("cl", "sha256:abc", at, res, obs, sel)
	if !v.Available || v.Summary.Entities != 28 || v.Summary.TierA != 16 {
		t.Errorf("summary wrong: %+v", v.Summary)
	}
	if v.Summary.PhenomenaFull != 1 || v.Summary.PhenomenaNone != 1 {
		t.Errorf("phenomena rollup wrong: %+v", v.Summary)
	}
	// Rules sorted by id; gaps-first phenomenon ordering (none before full).
	if v.Rules[0].RuleID != "THR_A" {
		t.Errorf("rules must sort by id: %v", v.Rules)
	}
	if v.Phenomena[0].Observability != "none" {
		t.Errorf("phenomena must surface gaps first: %v", v.Phenomena[0])
	}
	if v.Selection.NoneByReason["no-evaluable-variable"] != 12 {
		t.Errorf("selection summary wrong: %+v", v.Selection)
	}
}

func TestEndpointsServeJSON(t *testing.T) {
	st, _ := store.Open("")
	defer st.Close()
	mux := http.NewServeMux()
	Register(mux, Providers{
		Coverage: func() *CoverageView { return BuildCoverage("cl", "v", at, nil, nil, nil) },
		Findings: func(limit int) ([]store.FindingRow, error) { return st.ActiveFindings(limit) },
	})

	for _, path := range []string{"/api/coverage", "/api/findings"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status %d", path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
			t.Errorf("%s: content-type %q", path, ct)
		}
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Errorf("%s: invalid JSON: %v", path, err)
		}
	}
	// A POST is rejected.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/coverage", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST should be rejected, got %d", rec.Code)
	}
}
