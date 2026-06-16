package flow

import (
	"strings"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
)

// transitive-chain test harness (doc 15 cap. B). Flow edges are caller→callee; impact
// propagates callee→caller, so the chain root is the DEEPEST degraded callee and the
// walk follows NeighboursInto (the callers). All tests are network-free, deterministic,
// and assert the charter rules: ordered path, authored-only direction, silent
// intermediates never bridged, ZERO false chains on independent faults.

func transRel() Relation {
	return Relation{
		Trigger: PhenUpstreamDegradation, Downstream: PhenDownstreamImpact,
		Role: "trigger", Temporal: "T0->T0+",
		Why:    "An upstream service's degradation propagates to its downstream callers over the observed dependency edge.",
		Author: "vigil-engineering", Version: "test-v0", Status: "curated",
	}
}

func newFlowStore(at time.Time) *identity.EdgeStore {
	return identity.NewEdgeStore(func() time.Time { return at },
		map[identity.EdgeType]time.Duration{EdgeTypeFlow: flowBudget}, time.Hour)
}

func deg(ns, name, phen string) DegradedWorkload {
	return DegradedWorkload{
		CEI: roleCEId(ns, name), Label: ns + "/" + name,
		Phenomenon: phen, Detail: phen + " finding (degraded)",
	}
}

// A 4-service linear chain: prediction→db→aggregation→inference (call edges). impact
// propagates inference→aggregation→db→prediction. All four degraded ⇒ one ordered chain.
func TestTransitiveLinearChain(t *testing.T) {
	at := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	edges := newFlowStore(at)
	// caller → callee
	edges.Assert(EdgeTypeFlow, roleCEId("traffic", "aggregation"), roleCEId("traffic", "inference"), at)
	edges.Assert(EdgeTypeFlow, roleCEId("traffic", "db"), roleCEId("traffic", "aggregation"), at)
	edges.Assert(EdgeTypeFlow, roleCEId("traffic", "prediction"), roleCEId("traffic", "db"), at)
	w := identity.TimeWindow{Start: at, End: at}

	degraded := []DegradedWorkload{
		deg("traffic", "inference", "PHEN_CONTAINER_MEM_PRESSURE"),
		deg("traffic", "aggregation", "PHEN_APP_QUEUE_SATURATION"),
		deg("traffic", "db", "PHEN_APP_LATENCY"),
		deg("traffic", "prediction", "PHEN_APP_DATA_STALENESS"),
	}
	chains := TransitiveChains(edges, degraded, transRel(), w, at, 0)
	if len(chains) != 1 {
		t.Fatalf("want exactly 1 chain, got %d", len(chains))
	}
	c := chains[0]
	if c.MostUpstreamDegradedNode != "traffic/inference" {
		t.Fatalf("root=%q, want traffic/inference (deepest degraded callee)", c.MostUpstreamDegradedNode)
	}
	if len(c.Path) != 3 {
		t.Fatalf("want 3 path steps, got %d: %+v", len(c.Path), c.Path)
	}
	want := []struct {
		up, down string
		hop      int
	}{
		{"traffic/inference", "traffic/aggregation", 1},
		{"traffic/aggregation", "traffic/db", 2},
		{"traffic/db", "traffic/prediction", 3},
	}
	for i, wstep := range want {
		s := c.Path[i]
		if s.Upstream != wstep.up || s.Downstream != wstep.down || s.Hop != wstep.hop {
			t.Errorf("step %d = %s→%s hop%d, want %s→%s hop%d", i, s.Upstream, s.Downstream, s.Hop, wstep.up, wstep.down, wstep.hop)
		}
		if s.EdgeClass != "MEASURED observed flow" || s.WhyClass != "AUTHORED" || s.EdgeTraversal != "valid" {
			t.Errorf("step %d class labels: edge=%q why=%q traversal=%q", i, s.EdgeClass, s.WhyClass, s.EdgeTraversal)
		}
	}
	// Each node keeps its OWN measured phenomenon (the JOIN, never fused).
	if c.Path[0].UpstreamPhenomenon != "PHEN_CONTAINER_MEM_PRESSURE" || c.Path[2].DownstreamPhenomenon != "PHEN_APP_DATA_STALENESS" {
		t.Errorf("per-node phenomena not preserved: %+v", c.Path)
	}
	if len(c.Symptoms) != 4 {
		t.Errorf("want 4 symptoms (one per chain node), got %d", len(c.Symptoms))
	}
	// Charter: no causal token in the SYSTEM-GENERATED scaffolding (the authored `why`
	// is curated, governance-reviewed text surfaced verbatim — excluded from the guard).
	b, _ := ScaffoldingForCharter(c).JSON()
	if tok, bad := HasForbiddenToken(string(b)); bad {
		t.Errorf("forbidden token %q in chain scaffolding:\n%s", tok, b)
	}
}

// Silent intermediate: inference(deg) ← aggregation(SILENT) ← prediction(deg). The chain
// from inference must NOT bridge to prediction through the silent aggregation; the
// silence is a stated Gap; prediction is its OWN (separate) root with no path.
func TestTransitiveSilentIntermediateNotBridged(t *testing.T) {
	at := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	edges := newFlowStore(at)
	edges.Assert(EdgeTypeFlow, roleCEId("traffic", "aggregation"), roleCEId("traffic", "inference"), at)
	edges.Assert(EdgeTypeFlow, roleCEId("traffic", "prediction"), roleCEId("traffic", "aggregation"), at)
	w := identity.TimeWindow{Start: at, End: at}

	// aggregation is NOT degraded (silent intermediate).
	degraded := []DegradedWorkload{
		deg("traffic", "inference", "PHEN_CONTAINER_MEM_PRESSURE"),
		deg("traffic", "prediction", "PHEN_APP_DATA_STALENESS"),
	}
	chains := TransitiveChains(edges, degraded, transRel(), w, at, 0)
	// inference has a silent caller (aggregation) → no degraded impact edge → no chain from it,
	// but a stated gap. prediction's callee (aggregation) is silent too → prediction is a lone
	// root with no path. Net: ZERO chains (no asserted impact edge anywhere), but the gap is real.
	for _, c := range chains {
		for _, s := range c.Path {
			if (s.Upstream == "traffic/inference" && s.Downstream == "traffic/prediction") ||
				(s.Upstream == "traffic/prediction" && s.Downstream == "traffic/inference") {
				t.Fatalf("chain WRONGLY bridged the silent aggregation: %s→%s", s.Upstream, s.Downstream)
			}
		}
	}
	if len(chains) != 0 {
		t.Errorf("want 0 chains (the only path runs through a silent node — never bridged), got %d: %+v", len(chains), chains)
	}
}

// THE CARDINAL RULE: two independently-coincident faults that are NOT flow-connected
// must produce ZERO chains — never one merged chain (a false root-cause story).
func TestTransitiveNoFalseChainOnIndependentFaults(t *testing.T) {
	at := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	edges := newFlowStore(at)
	// Two disjoint pairs; the faults are in DIFFERENT pairs and never adjacent.
	edges.Assert(EdgeTypeFlow, roleCEId("a", "caller1"), roleCEId("a", "lonely1"), at)
	edges.Assert(EdgeTypeFlow, roleCEId("b", "caller2"), roleCEId("b", "lonely2"), at)
	w := identity.TimeWindow{Start: at, End: at}

	// lonely1 and lonely2 both degraded, but they have no flow edge between them and their
	// callers are healthy. Independent coincidence.
	degraded := []DegradedWorkload{
		deg("a", "lonely1", "PHEN_OOM_KILL_CGROUP"),
		deg("b", "lonely2", "PHEN_APP_DATA_STALENESS"),
	}
	chains := TransitiveChains(edges, degraded, transRel(), w, at, 0)
	if len(chains) != 0 {
		t.Fatalf("CARDINAL RULE VIOLATED: independent coincident faults produced %d chain(s): %+v", len(chains), chains)
	}
}

// Fan-in: one degraded callee with TWO degraded callers ⇒ two hop-1 steps, no merge.
func TestTransitiveFanIn(t *testing.T) {
	at := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	edges := newFlowStore(at)
	edges.Assert(EdgeTypeFlow, roleCEId("t", "callerA"), roleCEId("t", "shared"), at)
	edges.Assert(EdgeTypeFlow, roleCEId("t", "callerB"), roleCEId("t", "shared"), at)
	w := identity.TimeWindow{Start: at, End: at}
	degraded := []DegradedWorkload{
		deg("t", "shared", "PHEN_OOM_KILL_CGROUP"),
		deg("t", "callerA", "PHEN_APP_QUEUE_SATURATION"),
		deg("t", "callerB", "PHEN_APP_QUEUE_SATURATION"),
	}
	chains := TransitiveChains(edges, degraded, transRel(), w, at, 0)
	if len(chains) != 1 {
		t.Fatalf("want 1 chain, got %d", len(chains))
	}
	if len(chains[0].Path) != 2 {
		t.Fatalf("want 2 hop-1 steps (fan-in), got %d", len(chains[0].Path))
	}
	for _, s := range chains[0].Path {
		if s.Hop != 1 || s.Upstream != "t/shared" {
			t.Errorf("fan-in step = %s→%s hop%d, want t/shared→caller hop1", s.Upstream, s.Downstream, s.Hop)
		}
	}
}

// A cycle terminates (visited-set) and asserts both edges, never loops forever.
func TestTransitiveCycleTerminates(t *testing.T) {
	at := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	edges := newFlowStore(at)
	edges.Assert(EdgeTypeFlow, roleCEId("t", "x"), roleCEId("t", "y"), at)
	edges.Assert(EdgeTypeFlow, roleCEId("t", "y"), roleCEId("t", "x"), at)
	w := identity.TimeWindow{Start: at, End: at}
	degraded := []DegradedWorkload{deg("t", "x", "P1"), deg("t", "y", "P2")}
	chains := TransitiveChains(edges, degraded, transRel(), w, at, 0)
	if len(chains) != 1 {
		t.Fatalf("want 1 chain for the 2-cycle, got %d", len(chains))
	}
	if len(chains[0].Path) != 2 {
		t.Errorf("want both cycle edges (2 steps), got %d", len(chains[0].Path))
	}
}

// maxHops bounds the walk and states the ceiling as a gap.
func TestTransitiveMaxHopsCeiling(t *testing.T) {
	at := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	edges := newFlowStore(at)
	edges.Assert(EdgeTypeFlow, roleCEId("t", "n2"), roleCEId("t", "n1"), at)
	edges.Assert(EdgeTypeFlow, roleCEId("t", "n3"), roleCEId("t", "n2"), at)
	edges.Assert(EdgeTypeFlow, roleCEId("t", "n4"), roleCEId("t", "n3"), at)
	w := identity.TimeWindow{Start: at, End: at}
	degraded := []DegradedWorkload{deg("t", "n1", "P"), deg("t", "n2", "P"), deg("t", "n3", "P"), deg("t", "n4", "P")}
	chains := TransitiveChains(edges, degraded, transRel(), w, at, 2)
	if len(chains) != 1 {
		t.Fatalf("want 1 chain, got %d", len(chains))
	}
	if len(chains[0].Path) != 2 {
		t.Errorf("maxHops=2 must bound the path to 2 steps, got %d", len(chains[0].Path))
	}
	hasCeiling := false
	for _, g := range chains[0].Gaps {
		if strings.Contains(g.Reason, "hop ceiling") {
			hasCeiling = true
		}
	}
	if !hasCeiling {
		t.Errorf("a bounded walk with further degraded callers must state a hop-ceiling gap; gaps=%+v", chains[0].Gaps)
	}
}

// No authored relation ⇒ NO chain (direction is never invented).
func TestTransitiveNoRelationNoChain(t *testing.T) {
	at := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	edges := newFlowStore(at)
	edges.Assert(EdgeTypeFlow, roleCEId("t", "caller"), roleCEId("t", "callee"), at)
	w := identity.TimeWindow{Start: at, End: at}
	degraded := []DegradedWorkload{deg("t", "callee", "P"), deg("t", "caller", "P")}
	if chains := TransitiveChains(edges, degraded, Relation{}, w, at, 0); chains != nil {
		t.Fatalf("no authored relation ⇒ no chain, got %d", len(chains))
	}
}

// Determinism: the same inputs reproduce the same chains byte-for-byte.
func TestTransitiveDeterministic(t *testing.T) {
	at := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	build := func() string {
		edges := newFlowStore(at)
		edges.Assert(EdgeTypeFlow, roleCEId("t", "b"), roleCEId("t", "a"), at)
		edges.Assert(EdgeTypeFlow, roleCEId("t", "c"), roleCEId("t", "a"), at)
		edges.Assert(EdgeTypeFlow, roleCEId("t", "d"), roleCEId("t", "b"), at)
		w := identity.TimeWindow{Start: at, End: at}
		degraded := []DegradedWorkload{deg("t", "a", "P"), deg("t", "b", "P"), deg("t", "c", "P"), deg("t", "d", "P")}
		chains := TransitiveChains(edges, degraded, transRel(), w, at, 0)
		var sb strings.Builder
		for _, c := range chains {
			b, _ := c.JSON()
			sb.Write(b)
		}
		return sb.String()
	}
	if build() != build() {
		t.Error("TransitiveChains is non-deterministic across identical inputs")
	}
}
