package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/flow"
)

var rcNow = time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)

func TestRootCauseChainOff(t *testing.T) {
	v := BuildRootCauseChain(nil, nil, false, false, rcNow)
	if v.Enabled || v.Active || len(v.Chains) != 0 {
		t.Fatalf("OFF state wrong: enabled=%v active=%v chains=%d", v.Enabled, v.Active, len(v.Chains))
	}
	if !strings.Contains(v.Note, "OFF") {
		t.Errorf("OFF note must state the lane is off: %q", v.Note)
	}
}

func TestRootCauseChainQuiet(t *testing.T) {
	v := BuildRootCauseChain(nil, nil, true, false, rcNow)
	if !v.Enabled || v.Active {
		t.Fatalf("quiet state wrong: enabled=%v active=%v", v.Enabled, v.Active)
	}
}

// The multi-hop PROJECTED lane (cap. D) is WITHHELD while gate-pending, even with chains.
func TestRootCauseChainProjectedGatePending(t *testing.T) {
	projected := []flow.Chain{{
		MostUpstreamDegradedNode: "traffic/back",
		Path:                     []flow.PathStep{{Hop: 1, Upstream: "traffic/back", Downstream: "traffic/mid", Band: &flow.ProjectedBand{Class: "PROJECTED"}}},
	}}
	v := BuildRootCauseChain(nil, projected, true, false, rcNow) // gate NOT passed
	if v.ProjectedActive || len(v.ProjectedChains) != 0 {
		t.Fatalf("gate-pending must WITHHOLD the projected chains, got active=%v chains=%d", v.ProjectedActive, len(v.ProjectedChains))
	}
	if !strings.Contains(v.ProjectedNote, "gate") && !strings.Contains(v.ProjectedNote, "not") {
		t.Errorf("gate-pending note must say so: %q", v.ProjectedNote)
	}
	// When the gate passes, the projected lane surfaces.
	v2 := BuildRootCauseChain(nil, projected, true, true, rcNow)
	if !v2.ProjectedActive || len(v2.ProjectedChains) != 1 {
		t.Errorf("gate-passed must surface the projected chains, got active=%v chains=%d", v2.ProjectedActive, len(v2.ProjectedChains))
	}
}

func TestRootCauseChainActive(t *testing.T) {
	chains := []flow.Chain{{
		MostUpstreamDegradedNode: "traffic/inference",
		Path: []flow.PathStep{{
			Hop: 1, Upstream: "traffic/inference", UpstreamPhenomenon: "PHEN_CONTAINER_MEM_PRESSURE",
			Downstream: "traffic/aggregation", DownstreamPhenomenon: "PHEN_APP_QUEUE_SATURATION",
			EdgeClass: "MEASURED observed flow", EdgeTraversal: "valid",
			Why: "An upstream service's degradation propagates to its downstream callers.", WhyClass: "AUTHORED",
		}},
	}}
	v := BuildRootCauseChain(chains, nil, true, false, rcNow)
	if !v.Enabled || !v.Active || len(v.Chains) != 1 {
		t.Fatalf("active state wrong: enabled=%v active=%v chains=%d", v.Enabled, v.Active, len(v.Chains))
	}
	// Charter: no causal token in the SYSTEM-GENERATED surface scaffolding. The authored
	// `why` is curated verbatim text (excluded from the guard), so scrub it before scanning.
	scrub := v
	scrub.Chains = make([]flow.Chain, len(v.Chains))
	for i := range v.Chains {
		scrub.Chains[i] = flow.ScaffoldingForCharter(v.Chains[i])
	}
	b, _ := json.Marshal(scrub)
	if tok, bad := flow.HasForbiddenToken(string(b)); bad {
		t.Errorf("forbidden token %q in root-cause surface scaffolding:\n%s", tok, b)
	}
}
