package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func provRows() []CandidateRow {
	return []CandidateRow{
		// stray A: classified + mapped to a group (has an associated-with edge).
		{Kind: "node", Status: "candidate", Source: "cei-fallback", Subject: "stray:redis_up/aaa"},
		{Kind: "edge", Status: "candidate", Source: "cei-fallback", Relation: "associated-with", Subject: "stray:redis_up/aaa ~> r|c||Pod|redis"},
		// stray B: classified but UNRESOLVED (no edge — below the floor).
		{Kind: "node", Status: "candidate", Source: "cei-fallback", Subject: "stray:weird_metric/bbb"},
		// agent proposals.
		{Kind: "edge", Status: "candidate", Source: "dgx-agent", Relation: "associated-with", Subject: "x ~ y"},
		{Kind: "causal_hypothesis", Status: "candidate", Source: "dgx-agent", Relation: "co-occurrence", Subject: "silence:foo"},
		// audit + trace modality proposals.
		{Kind: "causal_hypothesis", Status: "candidate", Source: "audit", Relation: "observed-adjacency", Subject: "change:z ~ incident:i"},
		{Kind: "edge", Status: "promoted", Source: "trace", Relation: "topology", Subject: "trace-call:a->b"},
	}
}

func TestBuildProvisionalCoverageReconciles(t *testing.T) {
	v := BuildProvisionalCoverage(at, provRows())
	if v.StraysClassified != 2 {
		t.Errorf("straysClassified = %d, want 2", v.StraysClassified)
	}
	if v.StraysMappedToGroup != 1 {
		t.Errorf("straysMappedToGroup = %d, want 1 (stray A has an edge)", v.StraysMappedToGroup)
	}
	if v.StraysUnresolved != 1 {
		t.Errorf("straysUnresolved = %d, want 1 (stray B has no edge)", v.StraysUnresolved)
	}
	// classified == mapped + unresolved (the reconciliation invariant).
	if v.StraysClassified != v.StraysMappedToGroup+v.StraysUnresolved {
		t.Errorf("reconciliation broken: %d != %d + %d", v.StraysClassified, v.StraysMappedToGroup, v.StraysUnresolved)
	}
	if v.AgentEdges != 1 || v.AgentHypotheses != 1 || v.AuditHypotheses != 1 || v.TraceTopology != 1 {
		t.Errorf("modality counts wrong: %+v", v)
	}
	if v.ByStatus["candidate"] != 6 || v.ByStatus["promoted"] != 1 {
		t.Errorf("byStatus wrong: %+v", v.ByStatus)
	}
	if v.BySource["cei-fallback"] != 3 || v.BySource["dgx-agent"] != 2 {
		t.Errorf("bySource wrong: %+v", v.BySource)
	}
	if v.TotalCandidates != 7 || !v.Available {
		t.Errorf("totals/availability wrong: %+v", v)
	}
}

func TestProvisionalCoverageRouteAndOffState(t *testing.T) {
	// available
	mux := http.NewServeMux()
	Register(mux, Providers{ProvisionalCoverage: func() *ProvisionalCoverageView {
		return BuildProvisionalCoverage(at, provRows())
	}})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/provisional-coverage")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got ProvisionalCoverageView
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Available || got.StraysClassified != 2 {
		t.Errorf("served view wrong: %+v", got)
	}

	// honest OFF
	mux2 := http.NewServeMux()
	Register(mux2, Providers{})
	srv2 := httptest.NewServer(mux2)
	defer srv2.Close()
	resp2, err := http.Get(srv2.URL + "/api/provisional-coverage")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	var off ProvisionalCoverageView
	if err := json.NewDecoder(resp2.Body).Decode(&off); err != nil {
		t.Fatal(err)
	}
	if off.Available {
		t.Error("nil provider must yield available=false (honest OFF state)")
	}
}
