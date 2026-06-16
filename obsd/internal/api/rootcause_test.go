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
	v := BuildRootCauseChain(nil, false, rcNow)
	if v.Enabled || v.Active || len(v.Chains) != 0 {
		t.Fatalf("OFF state wrong: enabled=%v active=%v chains=%d", v.Enabled, v.Active, len(v.Chains))
	}
	if !strings.Contains(v.Note, "OFF") {
		t.Errorf("OFF note must state the lane is off: %q", v.Note)
	}
}

func TestRootCauseChainQuiet(t *testing.T) {
	v := BuildRootCauseChain(nil, true, rcNow)
	if !v.Enabled || v.Active {
		t.Fatalf("quiet state wrong: enabled=%v active=%v", v.Enabled, v.Active)
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
	v := BuildRootCauseChain(chains, true, rcNow)
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
