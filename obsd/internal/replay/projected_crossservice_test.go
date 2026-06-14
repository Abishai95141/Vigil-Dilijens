package replay

import (
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/binding"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/detect"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/flow"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/forecast"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
)

// projectedCrossServiceTick is the phase-E gate's faithful re-computation of obsd's
// warm-path ANTICIPATORY cascade. This test proves the RECONSTRUCTION: given a
// tick's forecast candidates (the warned callees), the captured bindings
// (instance→role), and the captured observed-flow topology, it reproduces exactly
// the PROJECTED chain the live warm path surfaced — same root + impacted callers,
// charter-clean, band non-degenerate — AND the measured chain of the same tick (the
// confirm/refute + lead-time join key). It joins ONLY on the identity-layer role CEI
// (no mis-join). Network-free, deterministic, -race: the candidates are injected
// directly (the forecast IS the input), so no clock is needed.
func TestProjectedCrossServiceTick(t *testing.T) {
	at := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	cluster := "c"
	ns := "ob"

	roleCEI := func(name string) identity.CEI {
		return identity.CEI{Layer: identity.LayerRole, Cluster: cluster, Namespace: ns,
			Kind: "Deployment", RoleKey: "Deployment/" + name}
	}
	instCEI := func(name, uid string) identity.CEI {
		return identity.CEI{Layer: identity.LayerInstance, Cluster: cluster, Namespace: ns,
			Kind: "Pod", Name: name + "-pod-" + uid, UID: uid}
	}

	catalog := roleCEI("productcatalogservice")
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

	catalogPod := instCEI("productcatalogservice", "u1")
	adPod := instCEI("adservice", "u2")
	bindings := []binding.Binding{
		{CEIKey: catalogPod.Key(), RoleKey: catalog.Key(), Entity: "Container", RuleID: "r1", Metric: "working_set"},
		{CEIKey: catalogPod.Key(), RoleKey: catalog.Key(), Entity: "Container", RuleID: "r2", Metric: "working_set"}, // dup role: dedup
		{CEIKey: adPod.Key(), RoleKey: roleCEI("adservice").Key(), Entity: "Container", RuleID: "r3", Metric: "working_set"},
	}
	w := identity.TimeWindow{Start: at.Add(-2 * time.Minute), End: at}

	// A healthy (non-degenerate) projection band for the catalog pod: earliest <
	// cross < latest, far edge closed. This is what the forecast funnel emits when
	// it warns "projected to cross soon" with a real band.
	warnCatalog := func() forecast.Candidate {
		return forecast.Candidate{
			Class: "PROJECTED", IsProjection: true,
			EntityCEI: catalogPod.Key(), Metric: "working_set", Confidence: "moderate",
			PrecursorPhenomena:  []string{"PHEN_MEMORY_LEAK"},
			CrossAt:             at.Add(10 * time.Minute),
			EarliestAt:          at.Add(6 * time.Minute),
			LatestAt:            at.Add(15 * time.Minute),
			LatestBeyondHorizon: false,
		}
	}

	t.Run("anticipatory fan-in fires; measured quiet (the lead-time scenario)", func(t *testing.T) {
		rec := TickRecord{EvalNow: at, BarsEpoch: 1}
		// Forecast warns the catalog callee; NO measured finding yet → the
		// anticipatory cascade leads the measured one.
		tp := projectedCrossServiceTick(rec, []forecast.Candidate{warnCatalog()}, nil, edges, bindings, rel, w)
		if !tp.ProjFired {
			t.Fatal("expected the anticipatory cascade to fire")
		}
		if tp.MeasFired {
			t.Fatal("expected the measured cascade quiet (no findings) — this is the lead-time window")
		}
		if tp.ProjRoot != "ob/productcatalogservice" {
			t.Fatalf("projRoot=%q, want ob/productcatalogservice", tp.ProjRoot)
		}
		if got, want := tp.ProjImpacted, []string{"ob/checkoutservice", "ob/frontend", "ob/recommendationservice"}; !equalStrs(got, want) {
			t.Fatalf("projImpacted=%v, want %v", got, want)
		}
		if got, want := tp.Warned, []string{"ob/productcatalogservice"}; !equalStrs(got, want) {
			t.Fatalf("warned=%v, want %v", got, want)
		}
		if !tp.CharterClean {
			t.Errorf("charter violation: token %q", tp.CharterToken)
		}
		// The band must be carried and must NOT collapse to a line.
		if !tp.ProjEarliest.Before(tp.ProjLatest) {
			t.Errorf("band collapsed: earliest=%v latest=%v (must be earliest<latest)", tp.ProjEarliest, tp.ProjLatest)
		}
		if !tp.ProjCrossAt.Equal(at.Add(10 * time.Minute)) {
			t.Errorf("projCrossAt=%v, want %v", tp.ProjCrossAt, at.Add(10*time.Minute))
		}
	})

	t.Run("both fire on the same root (the confirm scenario)", func(t *testing.T) {
		rec := TickRecord{EvalNow: at, BarsEpoch: 1}
		findings := []detect.Finding{
			{Phenomenon: "PHEN_OOM_KILL_CGROUP", EntityCEI: catalogPod.Key(), EvaluatedAt: at},
		}
		tp := projectedCrossServiceTick(rec, []forecast.Candidate{warnCatalog()}, findings, edges, bindings, rel, w)
		if !tp.ProjFired || !tp.MeasFired {
			t.Fatalf("expected both to fire (confirm), got proj=%v meas=%v", tp.ProjFired, tp.MeasFired)
		}
		if tp.ProjRoot != tp.MeasRoot || tp.MeasRoot != "ob/productcatalogservice" {
			t.Fatalf("roots disagree: proj=%q meas=%q (want both ob/productcatalogservice)", tp.ProjRoot, tp.MeasRoot)
		}
	})

	t.Run("collapsed band → hard charter violation", func(t *testing.T) {
		rec := TickRecord{EvalNow: at, BarsEpoch: 1}
		bad := warnCatalog()
		// A degenerate band: earliest==latest==cross, far edge CLOSED → PROJECTED
		// dressed as MEASURED certainty. The charter must reject it.
		bad.EarliestAt = bad.CrossAt
		bad.LatestAt = bad.CrossAt
		bad.LatestBeyondHorizon = false
		tp := projectedCrossServiceTick(rec, []forecast.Candidate{bad}, nil, edges, bindings, rel, w)
		if !tp.ProjFired {
			t.Fatal("expected the chain to fire (so the charter check runs on it)")
		}
		if tp.CharterClean {
			t.Fatal("expected a charter violation for a collapsed band")
		}
		if tp.CharterToken != "collapsed-band" {
			t.Errorf("charterToken=%q, want collapsed-band", tp.CharterToken)
		}
	})

	t.Run("open band (beyond horizon) is honest, not collapsed", func(t *testing.T) {
		rec := TickRecord{EvalNow: at, BarsEpoch: 1}
		open := warnCatalog()
		open.LatestBeyondHorizon = true
		open.LatestAt = time.Time{} // zero when open, per the candidate contract
		tp := projectedCrossServiceTick(rec, []forecast.Candidate{open}, nil, edges, bindings, rel, w)
		if !tp.ProjFired {
			t.Fatal("expected the chain to fire")
		}
		if !tp.CharterClean {
			t.Errorf("an open band is the MOST honest projection, not a violation: token %q", tp.CharterToken)
		}
		if !tp.ProjBandOpen {
			t.Error("expected projBandOpen=true (the far edge is open)")
		}
	})

	t.Run("warned callee with no caller → quiet", func(t *testing.T) {
		rec := TickRecord{EvalNow: at, BarsEpoch: 1}
		warnAd := warnCatalog()
		warnAd.EntityCEI = adPod.Key() // adservice has no caller over a flow edge
		tp := projectedCrossServiceTick(rec, []forecast.Candidate{warnAd}, nil, edges, bindings, rel, w)
		if tp.ProjFired {
			t.Fatalf("expected no anticipatory chain for a warned callee with no callers, got root=%q", tp.ProjRoot)
		}
		if got, want := tp.Warned, []string{"ob/adservice"}; !equalStrs(got, want) {
			t.Fatalf("warned=%v, want %v", got, want)
		}
	})

	t.Run("warning with no binding (mis-join guard) → not mapped", func(t *testing.T) {
		rec := TickRecord{EvalNow: at, BarsEpoch: 1}
		ghost := warnCatalog()
		ghost.EntityCEI = instCEI("unknown", "u9").Key()
		tp := projectedCrossServiceTick(rec, []forecast.Candidate{ghost}, nil, edges, bindings, rel, w)
		if tp.ProjFired || len(tp.Warned) != 0 {
			t.Fatalf("a warning with no binding must not map to a workload; got fired=%v warned=%v", tp.ProjFired, tp.Warned)
		}
	})

	t.Run("no candidates → quiet", func(t *testing.T) {
		rec := TickRecord{EvalNow: at, BarsEpoch: 1}
		tp := projectedCrossServiceTick(rec, nil, nil, edges, bindings, rel, w)
		if tp.ProjFired || len(tp.Warned) != 0 {
			t.Fatalf("expected quiet with no candidates, got fired=%v warned=%v", tp.ProjFired, tp.Warned)
		}
	})

	t.Run("nil flow store → quiet (pre-topology bundle)", func(t *testing.T) {
		rec := TickRecord{EvalNow: at, BarsEpoch: 1}
		tp := projectedCrossServiceTick(rec, []forecast.Candidate{warnCatalog()}, nil, nil, bindings, rel, w)
		if tp.ProjFired {
			t.Fatal("expected quiet when no flow topology was captured")
		}
	})
}
