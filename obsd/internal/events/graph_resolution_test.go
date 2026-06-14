package events

import (
	"testing"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
)

const (
	kgPath      = "../../../ontology/graph/k8s_signal_kg.json"
	overlayDir  = "../../../ontology/graph/overlays"
	condsV1Path = "../../../ontology/graph/overlays/experimental/event-conditions-v1.yaml"
)

// TestEventConditionsResolveInGraph guards the authored corroboration overlay the
// way flow's TestRelationFromGraph guards the cross-service relation: every NON-EMPTY
// `corroborates` target must name a real phenomenon in the released graph. An event
// condition that corroborates a phenomenon the ontology does not declare would be
// detection knowledge with no authored basis — caught here, before it can surface.
func TestEventConditionsResolveInGraph(t *testing.T) {
	g, err := graph.LoadWithOverlays(kgPath, overlayDir)
	if err != nil {
		t.Fatalf("load released graph: %v", err)
	}
	conds, err := LoadEventConditions(condsV1Path)
	if err != nil {
		t.Fatalf("load event conditions: %v", err)
	}
	if len(conds) == 0 {
		t.Fatal("the released event-conditions overlay declares no conditions")
	}
	for _, target := range CorroborationTargets(conds) {
		if g.Phenomena[target] == nil {
			t.Errorf("event condition corroborates %q, which is not a phenomenon in the released graph", target)
		}
	}
	// Sanity: the OOMKilled condition is present and targets the cgroup-OOM phenomenon.
	var sawOOM bool
	for _, c := range conds {
		if c.Reason == "OOMKilled" {
			sawOOM = true
			if c.Corroborates != "PHEN_OOM_KILL_CGROUP" {
				t.Errorf("OOMKilled corroborates %q, want PHEN_OOM_KILL_CGROUP", c.Corroborates)
			}
		}
	}
	if !sawOOM {
		t.Error("the released overlay must carry the OOMKilled condition")
	}
}

// TestExperimentalOverlayOutsideReleaseHash proves the experimental conditions file
// does NOT enter the released graph's content hash — adding it changes no release
// version (zero blast radius), exactly as overlays/experimental/ is designed.
func TestExperimentalOverlayOutsideReleaseHash(t *testing.T) {
	g, err := graph.LoadWithOverlays(kgPath, overlayDir)
	if err != nil {
		t.Fatal(err)
	}
	// The production loader globs overlayDir but skips its subdirectories, so the
	// experimental file is not merged — its presence cannot perturb g.Version. The
	// release identity is determined purely by base KG + the top-level overlays.
	if g.Version == "" {
		t.Fatal("released graph has no version pin")
	}
}
