package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/flow"
)

var csAt = time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)

// a charter-clean joined chain, as the warm path produces it.
func sampleChain() *flow.Chain {
	return &flow.Chain{
		MostUpstreamDegradedNode: "chaos/leaky-callee",
		NodeClass:                "MEASURED (structural fan-in over observed-flow edges)",
		NodeBasis:                "degraded callee reaching the most impacted callers",
		Links: []flow.Link{{
			Impacted: "chaos/flow-caller", Degraded: "chaos/leaky-callee",
			EdgeClass: "MEASURED observed flow", EdgeTraversal: "valid",
			Why:      "the caller's degradation is downstream of, not independent of, the callee's",
			WhyClass: "AUTHORED", Temporal: "T0->T0+", Author: "vigil-engineering", Version: "v0",
		}},
		Symptoms: []flow.SymptomOut{{
			Workload: "chaos/leaky-callee", Phenomenon: "PHEN_MEMORY_LEAK", Class: "MEASURED",
			Detail: "PHEN_MEMORY_LEAK finding (degraded callee)",
		}},
		GeneratedAt: csAt,
	}
}

func TestBuildCrossServiceStates(t *testing.T) {
	// OFF: flow discovery not running — the honest dark state, no chain.
	off := BuildCrossService(nil, false, csAt)
	if off.Enabled || off.Active || off.Chain != nil {
		t.Errorf("OFF state must be disabled+inactive with no chain: %+v", off)
	}
	if off.Note == "" || off.GateNote == "" {
		t.Error("OFF state must still state why the lane is dark + the gate posture")
	}

	// QUIET: flow on, but no cascade firing this tick — distinct from OFF.
	quiet := BuildCrossService(nil, true, csAt)
	if !quiet.Enabled || quiet.Active || quiet.Chain != nil {
		t.Errorf("QUIET state must be enabled, inactive, no chain: %+v", quiet)
	}
	if quiet.Note == off.Note {
		t.Error("QUIET and OFF must be DISTINCT stated states (never conflated)")
	}

	// ACTIVE: a chain is firing — surfaced verbatim.
	act := BuildCrossService(sampleChain(), true, csAt)
	if !act.Enabled || !act.Active || act.Chain == nil {
		t.Fatalf("ACTIVE state must carry the chain: %+v", act)
	}
	if act.Chain.MostUpstreamDegradedNode != "chaos/leaky-callee" {
		t.Errorf("active chain root not surfaced: %+v", act.Chain)
	}
	// The join is labelled per part (never fused).
	if act.Class == "" || act.Chain.Links[0].EdgeClass != "MEASURED observed flow" ||
		act.Chain.Links[0].WhyClass != "AUTHORED" {
		t.Error("each provenance class must be labelled on the surface (join, never fuse)")
	}
}

// The surface must carry NO causal-claim token — the exact charter check the
// backtest gate enforced over the rendered chain (flow.HasForbiddenToken), now
// applied to the full /api/cross-service payload including the framing notes.
func TestCrossServiceSurfaceCharterClean(t *testing.T) {
	for _, v := range []*CrossServiceView{
		BuildCrossService(nil, false, csAt),
		BuildCrossService(nil, true, csAt),
		BuildCrossService(sampleChain(), true, csAt),
	} {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if tok, bad := flow.HasForbiddenToken(string(raw)); bad {
			t.Errorf("charter violation: token %q in cross-service payload:\n%s", tok, raw)
		}
	}
}

// The route serves the honest OFF state when no provider is wired (flow off),
// and the active chain when the provider returns one.
func TestCrossServiceRoute(t *testing.T) {
	// No provider → OFF state, HTTP 200 (never a 404 implying "no such concept").
	mux := http.NewServeMux()
	Register(mux, Providers{})
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/cross-service", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var off CrossServiceView
	if err := json.Unmarshal(rr.Body.Bytes(), &off); err != nil {
		t.Fatal(err)
	}
	if off.Enabled || off.Active {
		t.Errorf("no provider must serve the OFF state: %+v", off)
	}

	// Provider returning an active chain → surfaced.
	mux2 := http.NewServeMux()
	Register(mux2, Providers{CrossService: func() *CrossServiceView {
		return BuildCrossService(sampleChain(), true, csAt)
	}})
	rr2 := httptest.NewRecorder()
	mux2.ServeHTTP(rr2, httptest.NewRequest(http.MethodGet, "/api/cross-service", nil))
	var act CrossServiceView
	if err := json.Unmarshal(rr2.Body.Bytes(), &act); err != nil {
		t.Fatal(err)
	}
	if !act.Active || act.Chain == nil || act.Chain.MostUpstreamDegradedNode != "chaos/leaky-callee" {
		t.Errorf("active chain not served through the route: %+v", act)
	}

	// POST is rejected (read-only surface).
	rr3 := httptest.NewRecorder()
	mux.ServeHTTP(rr3, httptest.NewRequest(http.MethodPost, "/api/cross-service", nil))
	if rr3.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST status = %d, want 405", rr3.Code)
	}
}
