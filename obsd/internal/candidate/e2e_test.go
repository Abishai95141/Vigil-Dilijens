package candidate_test

import (
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/candidate"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
)

// End-to-end (doc 20 P1): a REAL identity inventory + the ER orchestration produce a
// provisional node + an associated-with edge to the real seeded entity, read back
// through the store — proving the identity -> ER -> store chain, not just the units.
// This is an external test (candidate_test), so importing identity here does not put
// internal/candidate on identity's import path (the firewall is unaffected).
func TestStrayResolutionAgainstRealInventory(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	cluster := "cl1"
	st := identity.NewStore(func() time.Time { return now }, 15*time.Minute, 24*time.Hour, 1000)
	if _, err := st.Observe(
		identity.InstanceCoords{Cluster: cluster, Namespace: "shop", Kind: "Pod", Name: "web-a", UID: "pod-x"},
		identity.CEI{}, now, identity.StateActive,
	); err != nil {
		t.Fatal(err)
	}

	// map the real inventory exactly as cmd/obsd buildEntityRefs does.
	var inv []candidate.EntityRef
	for _, r := range st.ActiveInstances() {
		inv = append(inv, candidate.EntityRef{Key: r.CEI.Key(), Kind: r.Kind, Namespace: r.Namespace, Name: r.Name, UID: r.UID})
	}
	if len(inv) != 1 {
		t.Fatalf("inventory = %d, want 1 seeded pod", len(inv))
	}
	wantKey := inv[0].Key

	s, err := candidate.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	stray := candidate.StrayObservation{
		Family: "node-exporter", Metric: "vendor_widget_total",
		Labels:    map[string]string{"namespace": "shop", "pod": "web-a", "endpoint": "/x"},
		Node:      "worker-1",
		Reason:    "unknown-exporter-family",
		StreamRef: "vendor_widget_total@worker-1",
	}
	staged, err := candidate.ResolveAndStage(s, now, []candidate.StrayObservation{stray}, inv)
	if err != nil {
		t.Fatal(err)
	}
	if staged != 2 {
		t.Fatalf("staged %d, want 2 (provisional node + associated-with edge)", staged)
	}

	edges, _ := s.List(candidate.Filter{Kind: candidate.KindEdge})
	if len(edges) != 1 {
		t.Fatalf("edges = %d, want 1", len(edges))
	}
	e := edges[0]
	if e.Relation != "associated-with" {
		t.Errorf("relation = %q, want associated-with (never causal)", e.Relation)
	}
	if e.Payload["entityKey"] != wantKey {
		t.Errorf("edge entityKey = %v, want the real CEI %q", e.Payload["entityKey"], wantKey)
	}
	if e.Status != candidate.StatusCandidate || e.Lineage.Source != "cei-fallback" {
		t.Errorf("edge lifecycle/lineage wrong: %+v", e)
	}

	nodes, _ := s.List(candidate.Filter{Kind: candidate.KindNode})
	if len(nodes) != 1 || nodes[0].Status != candidate.StatusCandidate {
		t.Errorf("provisional node wrong: %+v", nodes)
	}
}
