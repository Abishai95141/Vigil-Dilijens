package replay

import (
	"time"

	"testing"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/binding"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/detect"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/flow"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
)

// crossServiceTick is the gate's faithful re-computation of obsd's warm-path
// cross-service cascade. This test proves the RECONSTRUCTION: given a tick's
// re-derived findings, the captured bindings (instance→role), and the captured
// observed-flow topology, it reproduces exactly the chain the live warm path
// surfaced — the same root + impacted callers, charter-clean — joining ONLY on
// the identity-layer role CEI (no mis-join). Network-free, deterministic, -race.
func TestCrossServiceTickReconstruction(t *testing.T) {
	at := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	cluster := "c"
	ns := "ob"

	// The identity-layer role CEIs (Kind="Deployment") findings map to.
	roleCEI := func(name string) identity.CEI {
		return identity.CEI{Layer: identity.LayerRole, Cluster: cluster, Namespace: ns,
			Kind: "Deployment", RoleKey: "Deployment/" + name}
	}
	// A pod instance CEI for a workload (what a finding's EntityCEI carries).
	instCEI := func(name, uid string) identity.CEI {
		return identity.CEI{Layer: identity.LayerInstance, Cluster: cluster, Namespace: ns,
			Kind: "Pod", Name: name + "-pod-" + uid, UID: uid}
	}

	catalog := roleCEI("productcatalogservice")
	// Captured observed-flow topology: three callers -> productcatalog, plus an
	// unrelated checkout -> cart edge (cart must NOT be named impacted by catalog).
	edges := identity.NewEdgeStore(func() time.Time { return at },
		map[identity.EdgeType]time.Duration{flow.EdgeTypeFlow: 90 * time.Second}, time.Hour)
	for _, caller := range []string{"frontend", "checkoutservice", "recommendationservice"} {
		edges.Assert(flow.EdgeTypeFlow, roleCEI(caller), catalog, at)
	}
	edges.Assert(flow.EdgeTypeFlow, roleCEI("checkoutservice"), roleCEI("cartservice"), at)

	rel, err := flow.LoadRelation("../../../ontology/graph/overlays/experimental/flow-relation-v0.yaml")
	if err != nil {
		t.Fatal(err)
	}

	// The captured bindings: the instance→role join the live identity store
	// resolves (RoleKey == role CEI Key()). productcatalog's pod is degraded.
	catalogPod := instCEI("productcatalogservice", "u1")
	adPod := instCEI("adservice", "u2") // degraded but has NO caller over a flow edge
	bindings := []binding.Binding{
		{CEIKey: catalogPod.Key(), RoleKey: catalog.Key(), Entity: "Container", RuleID: "r1", Metric: "m1"},
		{CEIKey: catalogPod.Key(), RoleKey: catalog.Key(), Entity: "Container", RuleID: "r2", Metric: "m2"}, // dup role: must dedup
		{CEIKey: adPod.Key(), RoleKey: roleCEI("adservice").Key(), Entity: "Container", RuleID: "r3", Metric: "m3"},
		{CEIKey: instCEI("node", "n1").Key(), RoleKey: "", Entity: "Node", RuleID: "r4", Metric: "m4"}, // role-less: ignored
	}
	w := identity.TimeWindow{Start: at.Add(-2 * time.Minute), End: at}

	t.Run("positive fan-in", func(t *testing.T) {
		rec := TickRecord{EvalNow: at, BarsEpoch: 1}
		findings := []detect.Finding{
			{Phenomenon: "PHEN_OOM_KILL_CGROUP", EntityCEI: catalogPod.Key(), EvaluatedAt: at},
		}
		tc := crossServiceTick(rec, findings, edges, bindings, rel, w)
		if !tc.Fired {
			t.Fatal("expected the cascade to fire")
		}
		if tc.Root != "ob/productcatalogservice" {
			t.Fatalf("root=%q, want ob/productcatalogservice", tc.Root)
		}
		if got, want := tc.Impacted, []string{"ob/checkoutservice", "ob/frontend", "ob/recommendationservice"}; !equalStrs(got, want) {
			t.Fatalf("impacted=%v, want %v", got, want)
		}
		for _, im := range tc.Impacted {
			if im == "ob/cartservice" {
				t.Error("cartservice wrongly named impacted (it is a callee of checkout, not of catalog)")
			}
		}
		if !tc.CharterClean {
			t.Errorf("charter violation: token %q", tc.CharterToken)
		}
		if got, want := tc.Degraded, []string{"ob/productcatalogservice"}; !equalStrs(got, want) {
			t.Fatalf("degraded=%v, want %v", got, want)
		}
	})

	t.Run("degraded callee with no caller → quiet", func(t *testing.T) {
		rec := TickRecord{EvalNow: at, BarsEpoch: 1}
		findings := []detect.Finding{
			{Phenomenon: "PHEN_OOM_KILL_CGROUP", EntityCEI: adPod.Key(), EvaluatedAt: at},
		}
		tc := crossServiceTick(rec, findings, edges, bindings, rel, w)
		if tc.Fired {
			t.Fatalf("expected no chain for a degraded callee with no callers, got root=%q", tc.Root)
		}
		if got, want := tc.Degraded, []string{"ob/adservice"}; !equalStrs(got, want) {
			t.Fatalf("degraded=%v, want %v", got, want)
		}
	})

	t.Run("no findings → quiet, empty degraded", func(t *testing.T) {
		rec := TickRecord{EvalNow: at, BarsEpoch: 1}
		tc := crossServiceTick(rec, nil, edges, bindings, rel, w)
		if tc.Fired || len(tc.Degraded) != 0 {
			t.Fatalf("expected quiet with no degraded, got fired=%v degraded=%v", tc.Fired, tc.Degraded)
		}
	})

	t.Run("finding with no binding (mis-join guard) → not mapped", func(t *testing.T) {
		rec := TickRecord{EvalNow: at, BarsEpoch: 1}
		findings := []detect.Finding{
			{Phenomenon: "PHEN_OOM_KILL_CGROUP", EntityCEI: instCEI("unknown", "u9").Key(), EvaluatedAt: at},
		}
		tc := crossServiceTick(rec, findings, edges, bindings, rel, w)
		if tc.Fired || len(tc.Degraded) != 0 {
			t.Fatalf("a finding with no binding must not map to a degraded workload; got fired=%v degraded=%v", tc.Fired, tc.Degraded)
		}
	})

	t.Run("nil flow store → quiet (pre-topology bundle)", func(t *testing.T) {
		rec := TickRecord{EvalNow: at, BarsEpoch: 1}
		findings := []detect.Finding{
			{Phenomenon: "PHEN_OOM_KILL_CGROUP", EntityCEI: catalogPod.Key(), EvaluatedAt: at},
		}
		tc := crossServiceTick(rec, findings, nil, bindings, rel, w)
		if tc.Fired {
			t.Fatal("expected quiet when no flow topology was captured")
		}
	})
}

func equalStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
