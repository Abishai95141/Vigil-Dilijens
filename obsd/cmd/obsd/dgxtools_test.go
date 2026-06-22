package main

import (
	"context"
	"strings"
	"testing"
	"time"

	vapi "github.com/Abishai95141/Vigil-Dilijens/obsd/internal/api"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/candidate"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/dgx"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/unexplained"
)

func TestEquivGroupObservations(t *testing.T) {
	g := &graph.Graph{EquivalenceGroups: map[string]*graph.EquivalenceGroup{
		"EQG_B": {ID: "EQG_B", Label: "B grp", CanonicalOTel: "b.canon", Patterns: []string{"^b_metric$"}},
		"EQG_A": {ID: "EQG_A", Label: "A grp", CanonicalOTel: "a.canon"},
	}}
	obs := equivGroupObservations(g)
	if len(obs) != 2 {
		t.Fatalf("got %d obs, want 2", len(obs))
	}
	if obs[0].Ref != "group:EQG_A" || obs[1].Ref != "group:EQG_B" {
		t.Errorf("not sorted by id: %s, %s", obs[0].Ref, obs[1].Ref)
	}
	if !strings.Contains(obs[1].Detail, "b.canon") || !strings.Contains(obs[1].Detail, "^b_metric$") {
		t.Errorf("detail missing canonical/pattern: %q", obs[1].Detail)
	}
	if equivGroupObservations(nil) != nil {
		t.Error("nil graph → nil")
	}
}

func TestTopologyObservations(t *testing.T) {
	v := &vapi.TopologyView{
		Nodes: []vapi.TopoNode{{CEIKey: "k1", Kind: "Deployment", Namespace: "boutique", Name: "cart", Degraded: true}},
		Edges: []vapi.TopoEdge{{Type: "flow", From: "k1", To: "k2", Status: "valid"}},
	}
	obs := topologyObservations(v)
	if len(obs) != 2 {
		t.Fatalf("got %d, want 2 (1 node + 1 edge)", len(obs))
	}
	// Observations are sorted by Ref ("edge:" < "entity:"), so order is edge then node.
	byRef := map[string]dgx.Observation{}
	for _, o := range obs {
		byRef[o.Ref] = o
	}
	if node, ok := byRef["entity:k1"]; !ok || !strings.Contains(node.Detail, "degraded") {
		t.Errorf("node obs wrong: %+v", node)
	}
	if _, ok := byRef["edge:k1~k2"]; !ok {
		t.Errorf("edge ref missing: %+v", obs)
	}
	if obs[0].Ref >= obs[1].Ref {
		t.Errorf("observations not sorted by ref: %s, %s", obs[0].Ref, obs[1].Ref)
	}
	if topologyObservations(nil) != nil {
		t.Error("nil view → nil")
	}
}

func TestSilenceAndCoverageObservations(t *testing.T) {
	sv := &vapi.SilenceLedgerView{Silent: []vapi.SilenceLedgerRow{
		{Metric: "redis_connected_clients", Entity: "Pod", Reason: "unbounded (no declared bar)"},
	}}
	sobs := silenceObservations(sv)
	if len(sobs) != 1 || sobs[0].Ref != "silence:redis_connected_clients" {
		t.Fatalf("silence obs: %+v", sobs)
	}

	cv := &vapi.CoverageView{
		Summary:   vapi.CoverageSummary{PhenomenaFull: 13, PhenomenaPartial: 11, PhenomenaNone: 17, Resolvability: 0.28},
		Phenomena: []vapi.PhenomenonRow{{ID: "PHEN_DNS_FAILURE", Label: "DNS failure", Observability: "none", RequiredTotal: 2, RequiredOk: 0, MissingReasons: []string{"no DNS series"}}},
	}
	cobs := coverageObservations(cv)
	if len(cobs) != 2 || cobs[0].Ref != "coverage:summary" {
		t.Fatalf("coverage obs: %+v", cobs)
	}
	if cobs[1].Ref != "phenomenon:PHEN_DNS_FAILURE" || !strings.Contains(cobs[1].Detail, "no DNS series") {
		t.Errorf("phenomenon obs wrong: %+v", cobs[1])
	}
}

func TestUnexplainedObservations(t *testing.T) {
	v := &vapi.UnexplainedView{
		OpenCards: []unexplained.Finding{
			{Scope: "cei-cart", Namespace: "boutique", Name: "cart", Kind: "Deployment", MatchCheck: "no phenomenon covered the loud state"},
		},
		Candidates: []unexplained.CandidateReport{
			{Metrics: []string{"foo_total", "bar_total"}, EntityKind: "Pod", Entities: []string{"e1", "e2"}, Rationale: "recurs over 3 windows"},
		},
	}
	obs := unexplainedObservations(v)
	if len(obs) != 2 {
		t.Fatalf("got %d, want 2 (1 card + 1 candidate)", len(obs))
	}
	if obs[0].Ref != "unexplained:cei-cart" || !strings.Contains(obs[0].Detail, "cart") {
		t.Errorf("card obs wrong: %+v", obs[0])
	}
	if obs[1].Ref != "uxcand:foo_total,bar_total" || !strings.Contains(obs[1].Detail, "recurs over 3 windows") {
		t.Errorf("candidate obs wrong: %+v", obs[1])
	}
	if unexplainedObservations(nil) != nil {
		t.Error("nil view → nil")
	}
}

func TestBuildLedger(t *testing.T) {
	s, err := candidate.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	// pending (left as candidate)
	if _, err := s.Put(now, candidate.Candidate{Kind: candidate.KindNode, Subject: "stray:pending"}); err != nil {
		t.Fatal(err)
	}
	// rejected by a human
	rid, _ := s.Put(now, candidate.Candidate{Kind: candidate.KindEdge, Subject: "a~>b", Relation: "associated-with"})
	if err := s.Decide(now, rid, candidate.StatusRejected, "alice", "wrong workload"); err != nil {
		t.Fatal(err)
	}
	// promoted by a human
	pid, _ := s.Put(now, candidate.Candidate{Kind: candidate.KindEquivGroup, Subject: "stray:redis"})
	if err := s.Decide(now, pid, candidate.StatusPromoted, "alice", "redis clients gauge"); err != nil {
		t.Fatal(err)
	}

	led := buildLedger(s)
	if len(led.Pending) != 1 || led.Pending[0].Subject != "stray:pending" {
		t.Errorf("pending: %+v", led.Pending)
	}
	if len(led.Rejected) != 1 || led.Rejected[0].Note != "wrong workload" {
		t.Errorf("rejected: %+v", led.Rejected)
	}
	if len(led.Promoted) != 1 || led.Promoted[0].Note != "redis clients gauge" {
		t.Errorf("promoted: %+v", led.Promoted)
	}
	if buildLedger(nil).Promoted != nil {
		t.Error("nil store → empty ledger")
	}
}

// The registry wires the six named read-only tools and dispatches them.
func TestNewDGXToolRegistry(t *testing.T) {
	s, _ := candidate.Open("")
	defer s.Close()
	g := &graph.Graph{EquivalenceGroups: map[string]*graph.EquivalenceGroup{"EQG_X": {ID: "EQG_X", Label: "x", CanonicalOTel: "x"}}}
	reg := newDGXToolRegistry(s, g, nil, nil, nil, nil, nil, nil, nil, nil)
	names := reg.Names()
	want := []string{"get_causal_hypotheses", "get_coverage", "get_cross_service", "get_dependency", "get_rightsizing", "get_silence_ledger", "get_strays", "get_topology", "get_unexplained", "search_equivalence_groups"}
	if len(names) != len(want) {
		t.Fatalf("tools = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("tool[%d] = %q, want %q", i, names[i], want[i])
		}
	}
	// search_equivalence_groups dispatches against the graph.
	obs, err := reg.Call(context.Background(), "search_equivalence_groups", nil)
	if err != nil || len(obs) != 1 || obs[0].Ref != "group:EQG_X" {
		t.Errorf("search_equivalence_groups = %+v, %v", obs, err)
	}
	// a nil-view tool returns an honest empty (no panic).
	if obs, err := reg.Call(context.Background(), "get_topology", nil); err != nil || obs != nil {
		t.Errorf("nil-view get_topology should be empty: %+v, %v", obs, err)
	}
}
