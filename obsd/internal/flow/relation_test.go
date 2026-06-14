package flow

import (
	"strings"
	"testing"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
)

// RelationFromGraph reads the cross-service relation from the CURATED ontology
// (doc 15 Phase C) — the phenomenon_relation edge added by the cross-service
// overlay, not the experimental file. This guards that obsd's production path
// (flow.RelationFromGraph in main.go) finds a well-formed, charter-clean relation.
func TestRelationFromGraph(t *testing.T) {
	g, err := graph.LoadWithOverlays("../../../ontology/graph/k8s_signal_kg.json", "../../../ontology/graph/overlays")
	if err != nil {
		t.Fatal(err)
	}
	rel, ok := RelationFromGraph(g)
	if !ok {
		t.Fatal("curated cross-service relation not found in the graph")
	}
	if rel.Trigger != PhenUpstreamDegradation || rel.Downstream != PhenDownstreamImpact {
		t.Errorf("relation roles = %s -> %s", rel.Trigger, rel.Downstream)
	}
	if rel.Role != "downstream" || rel.Temporal != "T0->T0+" {
		t.Errorf("relation role/temporal = %q / %q", rel.Role, rel.Temporal)
	}
	if rel.Status != "curated" || rel.Why == "" {
		t.Errorf("relation status/why = %q / %q", rel.Status, rel.Why)
	}
	// Version provenance is the release name (or content-hash pin) — present.
	if rel.Version == "" {
		t.Error("relation carries no version provenance")
	}
	// Charter: the curated 'why' must carry no causal-claim token (it is surfaced
	// verbatim in the cascade, which the gate proved charter-clean).
	if tok, bad := HasForbiddenToken(rel.Why); bad {
		t.Errorf("curated relation why carries forbidden token %q: %s", tok, rel.Why)
	}
	if !strings.Contains(rel.Why, "downstream of") {
		t.Errorf("curated why not the expected authored text: %q", rel.Why)
	}

	// Negative: a graph without the overlay (base only) yields no curated relation.
	base, err := graph.Load("../../../ontology/graph/k8s_signal_kg.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := RelationFromGraph(base); ok {
		t.Error("base graph (no cross-service overlay) must yield no curated relation")
	}
}
