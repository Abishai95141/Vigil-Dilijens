package flow

import (
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
