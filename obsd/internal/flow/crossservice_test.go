package flow

import (
	"strings"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
)

// roleCEId builds an identity-layer role CEI (Kind="Deployment") — the exact shape
// the identity layer mints and that obsd findings map to. The cross-service cascade
// must join on these, not on the spike's standalone Kind="Pod" keys.
func roleCEId(ns, name string) identity.CEI {
	return identity.CEI{Layer: identity.LayerRole, Cluster: "c", Namespace: ns, Kind: "Deployment", RoleKey: "Deployment/" + name}
}

func TestCrossServiceChain(t *testing.T) {
	at := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	edges := identity.NewEdgeStore(func() time.Time { return at },
		map[identity.EdgeType]time.Duration{EdgeTypeFlow: flowBudget}, time.Hour)

	catalog := roleCEId("ob", "productcatalogservice")
	for _, caller := range []string{"frontend", "checkoutservice", "recommendationservice"} {
		edges.Assert(EdgeTypeFlow, roleCEId("ob", caller), catalog, at)
	}
	// a non-catalog edge: checkout -> cart (cart must NOT appear as impacted by catalog).
	edges.Assert(EdgeTypeFlow, roleCEId("ob", "checkoutservice"), roleCEId("ob", "cartservice"), at)

	rel, err := LoadRelation("../../../ontology/graph/overlays/experimental/flow-relation-v0.yaml")
	if err != nil {
		t.Fatal(err)
	}
	w := identity.TimeWindow{Start: at, End: at}

	// productcatalog carries a MEASURED OOM finding → degraded callee.
	degraded := []DegradedWorkload{{
		CEI: catalog, Label: "ob/productcatalogservice",
		Phenomenon: "PHEN_OOM_KILL_CGROUP", Detail: "OOM_KILL_CGROUP finding (degraded)",
	}}
	chain, ok := CrossServiceChain(edges, degraded, rel, w, at)
	if !ok {
		t.Fatal("expected a cross-service chain")
	}
	if chain.MostUpstreamDegradedNode != "ob/productcatalogservice" {
		t.Fatalf("root=%q, want ob/productcatalogservice", chain.MostUpstreamDegradedNode)
	}
	if len(chain.Links) != 3 {
		t.Fatalf("links=%d, want 3", len(chain.Links))
	}
	impacted := map[string]bool{}
	for _, l := range chain.Links {
		impacted[l.Impacted] = true
		if l.Degraded != "ob/productcatalogservice" {
			t.Errorf("link degraded=%s, want productcatalog", l.Degraded)
		}
		if l.EdgeClass != "MEASURED observed flow" || l.WhyClass != "AUTHORED" {
			t.Errorf("class labels: edge=%q why=%q", l.EdgeClass, l.WhyClass)
		}
	}
	for _, w := range []string{"ob/frontend", "ob/checkoutservice", "ob/recommendationservice"} {
		if !impacted[w] {
			t.Errorf("missing impacted caller %s", w)
		}
	}
	if impacted["ob/cartservice"] {
		t.Error("cartservice wrongly impacted (it is a callee of checkout, not of catalog)")
	}

	// Charter: no causal-claim token in the rendered chain.
	b, _ := chain.JSON()
	if tok, bad := HasForbiddenToken(string(b)); bad {
		t.Errorf("forbidden token %q in chain:\n%s", tok, b)
	}

	// Negative: no degraded workloads → no chain.
	if _, ok := CrossServiceChain(edges, nil, rel, w, at); ok {
		t.Error("expected no chain for empty degraded set")
	}
	// Negative: a degraded workload with no callers (no inbound flow edge) → no chain.
	if _, ok := CrossServiceChain(edges, []DegradedWorkload{{CEI: roleCEId("ob", "adservice"), Label: "ob/adservice"}}, rel, w, at); ok {
		t.Error("expected no chain for a degraded callee with no callers")
	}
}

// TestProjectedCrossServiceChain is the Phase E core: an upstream callee FORECAST
// to cross soon propagates a PROJECTED downstream-impact hypothesis to its callers
// over the MEASURED flow edge + the AUTHORED relation. Asserts the weakest-input
// rule (every impact symptom PROJECTED, edge MEASURED, why AUTHORED), a band that
// never collapses, and charter-cleanliness. Network-free, deterministic, -race.
func TestProjectedCrossServiceChain(t *testing.T) {
	at := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	edges := identity.NewEdgeStore(func() time.Time { return at },
		map[identity.EdgeType]time.Duration{EdgeTypeFlow: flowBudget}, time.Hour)
	catalog := roleCEId("ob", "productcatalogservice")
	for _, caller := range []string{"frontend", "checkoutservice", "recommendationservice"} {
		edges.Assert(EdgeTypeFlow, roleCEId("ob", caller), catalog, at)
	}
	edges.Assert(EdgeTypeFlow, roleCEId("ob", "checkoutservice"), roleCEId("ob", "cartservice"), at)

	rel, err := LoadRelation("../../../ontology/graph/overlays/experimental/flow-relation-v0.yaml")
	if err != nil {
		t.Fatal(err)
	}
	w := identity.TimeWindow{Start: at, End: at}

	// productcatalog is PROJECTED to cross its working_set bar ~10:13, band 10:09–10:22.
	projected := []ProjectedDegradedWorkload{{
		CEI: catalog, Label: "ob/productcatalogservice", Metric: "working_set",
		PrecursorPhenomena: []string{"PHEN_OOM_KILL_CGROUP"}, Confidence: "moderate",
		CrossAt:    at.Add(13 * time.Minute),
		EarliestAt: at.Add(9 * time.Minute), LatestAt: at.Add(22 * time.Minute),
	}}
	chain, ok := ProjectedCrossServiceChain(edges, projected, rel, w, at)
	if !ok {
		t.Fatal("expected an anticipatory cross-service chain")
	}
	if chain.MostUpstreamDegradedNode != "ob/productcatalogservice" {
		t.Fatalf("root=%q", chain.MostUpstreamDegradedNode)
	}
	if !strings.Contains(chain.NodeClass, "PROJECTED") {
		t.Errorf("node class must be PROJECTED-seeded: %q", chain.NodeClass)
	}
	if len(chain.Links) != 3 {
		t.Fatalf("links=%d, want 3", len(chain.Links))
	}
	for _, l := range chain.Links {
		// The edge stays MEASURED and the why stays AUTHORED — only the impact is PROJECTED.
		if l.EdgeClass != "MEASURED observed flow" || l.WhyClass != "AUTHORED" {
			t.Errorf("link classes: edge=%q why=%q (edge MEASURED + why AUTHORED, never upgraded)", l.EdgeClass, l.WhyClass)
		}
		if l.Impacted == "ob/cartservice" {
			t.Error("cartservice wrongly impacted")
		}
	}
	// Every symptom is PROJECTED (the weakest-input rule).
	bandSeen := false
	for _, s := range chain.Symptoms {
		if s.Class != "PROJECTED" {
			t.Errorf("symptom %s class=%q, want PROJECTED", s.Workload, s.Class)
		}
		if strings.Contains(s.Detail, "10:09Z") && strings.Contains(s.Detail, "10:22Z") {
			bandSeen = true // the band is present and does not collapse to a line
		}
	}
	if !bandSeen {
		t.Error("the forecast band must be surfaced as a band (earliest..latest), never a point")
	}

	// Charter: no causal-claim token in the rendered chain.
	b, _ := chain.JSON()
	if tok, bad := HasForbiddenToken(string(b)); bad {
		t.Errorf("forbidden token %q in projected chain:\n%s", tok, b)
	}

	// Negatives: empty seed, and a forecast-degraded callee with no caller.
	if _, ok := ProjectedCrossServiceChain(edges, nil, rel, w, at); ok {
		t.Error("expected no chain for empty projected set")
	}
	lone := []ProjectedDegradedWorkload{{CEI: roleCEId("ob", "adservice"), Label: "ob/adservice", Metric: "working_set",
		Confidence: "wide", CrossAt: at.Add(time.Minute), EarliestAt: at, LatestAt: at.Add(2 * time.Minute)}}
	if _, ok := ProjectedCrossServiceChain(edges, lone, rel, w, at); ok {
		t.Error("expected no chain for a forecast-degraded callee with no callers")
	}
}
