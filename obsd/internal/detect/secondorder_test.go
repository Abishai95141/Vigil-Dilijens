package detect

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/observe"
)

// Second-order matching (doc 07 M3) golden tests. Two carriers:
//
//   - PHEN_STORAGE_SATURATION on the REAL ontology (v0.3.0): Node anchor with
//     IO PSI, PVC fill two hops away over runs-on + mounts (node → pod → PVC).
//   - PHEN_CNI_FAILURE under a TEST-ONLY overlay: the doc's noisy-neighbour
//     SHAPE — anchor pod A → node → sibling pod B over two runs-on hops.
//
// The trust-critical assertions extend M2's to paths: a suspect hop ANYWHERE
// degrades and is named; a broken first hop makes the two-hop entity
// unreachable — its evidence never contributes (degrade-never-fabricate).

const (
	soNodeKey = "i|cl||Node|worker-1|nodeuid-1"
	soPodKey  = "i|cl|shop|Pod|app-7d9|poduid-1"
	soPVCKey  = "i|cl|shop|PersistentVolumeClaim|data-claim|uid-pvc"
)

func ioPressuredNodeFP() observe.Fingerprint {
	return observe.Fingerprint{
		CEIKey: soNodeKey, Name: "worker-1", Kind: "Node", EvaluatedAt: evalAt,
		Thresholds: []observe.VariableThreshold{{
			RuleID: "THR_NODE_IO_PSI_STALL",
			Metric: "node_pressure_io_waiting_seconds_total",
			State:  observe.StateAbove, BarSource: "default", Flagged: true,
			Deriv: observe.DerivationRef{StreamID: "s-iopsi", SampleAt: evalAt, How: "counter-rate"},
		}},
	}
}

func fullPVCFP(state observe.ThresholdState) observe.Fingerprint {
	return observe.Fingerprint{
		CEIKey: soPVCKey, Namespace: "shop", Name: "data-claim", Kind: "PVC", EvaluatedAt: evalAt,
		Thresholds: []observe.VariableThreshold{{
			RuleID: "THR_PVC_USED_VS_REQUESTED",
			Metric: "kubelet_volume_stats_used_bytes",
			State:  state, BarSource: "config",
			Deriv: observe.DerivationRef{StreamID: "s-pvc", SampleAt: evalAt, How: "gauge-level"},
		}},
	}
}

// storageTopo wires node ← pod (runs-on) and pod → PVC (mounts), with each
// edge's last confirmation at the given instants.
func storageTopo(t *testing.T, runsOnConfirm, mountsConfirm time.Time) *identity.EdgeStore {
	t.Helper()
	s := identity.NewEdgeStore(func() time.Time { return evalAt },
		map[identity.EdgeType]time.Duration{
			identity.EdgeRunsOn: 90 * time.Second,
			identity.EdgeMounts: 90 * time.Second,
		}, 24*time.Hour)
	s.Assert(identity.EdgeRunsOn, mustCEI(t, soPodKey), mustCEI(t, soNodeKey), runsOnConfirm)
	s.Assert(identity.EdgeMounts, mustCEI(t, soPodKey), mustCEI(t, soPVCKey), mountsConfirm)
	return s
}

// The headline M3 case on the real graph: IO-pressured node + full PVC two
// valid hops away ⇒ a DEGRADED second-order match (the per-device disk-IO
// required member is honestly unobservable) whose derivation carries the FULL
// two-step path and the hop-2 supporting evidence.
func TestStorageSaturationTwoHop(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	fps := []observe.Fingerprint{ioPressuredNodeFP(), fullPVCFP(observe.StateAbove)}
	fresh := evalAt.Add(-10 * time.Second)
	out := m.Match(fps, nil, storageTopo(t, fresh, fresh), w)
	f := findPhen(out, "PHEN_STORAGE_SATURATION")
	if f == nil {
		t.Fatalf("STORAGE_SATURATION should fire at the node; findings: %+v", out)
	}
	if f.EntityCEI != soNodeKey || f.Span != "second-order" {
		t.Errorf("anchored at %s span %s, want the node / second-order", f.EntityCEI, f.Span)
	}
	// Honestly degraded: the required disk-IO member has no check on this
	// signal set (per-device sub-variables pending) — named, never hidden.
	if f.Quality != QualityDegraded || f.RequiredMet != 1 || f.RequiredUnobserved == 0 {
		t.Errorf("want degraded with PSI met + disk-IO unobservable, got %s met=%d unobs=%d",
			f.Quality, f.RequiredMet, f.RequiredUnobserved)
	}
	// The two-hop supporting evidence: PVC fill, across node→pod→PVC.
	var pvc *MemberEvidence
	for i := range f.Members {
		if f.Members[i].Metric == "kubelet_volume_stats_used_bytes" {
			pvc = &f.Members[i]
		}
	}
	if pvc == nil || pvc.Neighbour != soPVCKey || pvc.Hop != 2 || pvc.Via != "runs-on→mounts" || pvc.EdgeResult != "valid" {
		t.Fatalf("PVC evidence must cite the claim two hops away: %+v", pvc)
	}
	if f.SupportingMet != 1 {
		t.Errorf("the PVC member is supporting (corroborating role): met=%d", f.SupportingMet)
	}
	// The span instantiation: BOTH steps, as walked from the anchor.
	if len(f.SpanPath) != 2 ||
		f.SpanPath[0].Type != "runs-on" || f.SpanPath[0].From != soNodeKey || f.SpanPath[0].To != soPodKey ||
		f.SpanPath[1].Type != "mounts" || f.SpanPath[1].From != soPodKey || f.SpanPath[1].To != soPVCKey {
		t.Errorf("span path must walk node→pod→PVC: %+v", f.SpanPath)
	}
}

// A suspect hop ANYWHERE on the path degrades and is NAMED — here the second
// hop (mounts) went stale while the first stayed fresh.
func TestTwoHopSuspectSecondHopNamed(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	fps := []observe.Fingerprint{ioPressuredNodeFP(), fullPVCFP(observe.StateAbove)}
	out := m.Match(fps, nil, storageTopo(t, evalAt.Add(-10*time.Second), evalAt.Add(-10*time.Minute)), w)
	f := findPhen(out, "PHEN_STORAGE_SATURATION")
	if f == nil {
		t.Fatal("the match must surface — degraded, not dropped")
	}
	var pvc *MemberEvidence
	for i := range f.Members {
		if f.Members[i].Metric == "kubelet_volume_stats_used_bytes" {
			pvc = &f.Members[i]
		}
	}
	if pvc == nil || pvc.EdgeResult != "suspect" {
		t.Fatalf("the path's worst verdict must reach the evidence: %+v", pvc)
	}
	named := false
	for _, s := range f.SuspectEdges {
		if strings.Contains(s, "mounts") && strings.Contains(s, soPVCKey) {
			named = true
		}
	}
	if !named {
		t.Errorf("the stale mounts hop must be NAMED: %v", f.SuspectEdges)
	}
}

// THE M3 trust case: the FIRST hop is gone (runs-on retracted before the
// window) — the PVC's real, crossed fill evidence is unreachable and must
// contribute NOTHING. No span path, no neighbour evidence, no fabrication.
func TestBrokenFirstHopNeverFabricates(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	fps := []observe.Fingerprint{ioPressuredNodeFP(), fullPVCFP(observe.StateAbove)}
	s := identity.NewEdgeStore(func() time.Time { return evalAt },
		map[identity.EdgeType]time.Duration{
			identity.EdgeRunsOn: 90 * time.Second,
			identity.EdgeMounts: 90 * time.Second,
		}, 24*time.Hour)
	s.Assert(identity.EdgeRunsOn, mustCEI(t, soPodKey), mustCEI(t, soNodeKey), evalAt.Add(-2*time.Hour))
	s.Retract(identity.EdgeRunsOn, soPodKey, soNodeKey, evalAt.Add(-time.Hour)) // long before the window
	s.Assert(identity.EdgeMounts, mustCEI(t, soPodKey), mustCEI(t, soPVCKey), evalAt.Add(-10*time.Second))

	f := findPhen(m.Match(fps, nil, s, w), "PHEN_STORAGE_SATURATION")
	if f == nil {
		t.Fatal("anchor evidence alone should still produce the degraded finding")
	}
	for _, ev := range f.Members {
		if ev.Neighbour != "" {
			t.Errorf("FABRICATION: evidence cited across a broken path: %+v", ev)
		}
	}
	if len(f.SpanPath) != 0 || f.SupportingMet != 0 {
		t.Errorf("nothing was traversable: span=%+v supportingMet=%d", f.SpanPath, f.SupportingMet)
	}
}

// The doc's noisy-neighbour SHAPE (A → node → B over two runs-on hops), under
// a TEST-ONLY overlay binding real KG members to fixture variables: the match
// anchors at A with full two-step derivation citing B; B itself stays silent
// (its anchor carries no anchor-side evidence — the M2 rule, unchanged at
// depth 2).
func TestNoisyNeighbourShapeAcrossTwoHops(t *testing.T) {
	g, err := graph.LoadWithOverlays(kgPath, "testdata/overlays-noisy")
	if err != nil {
		t.Fatalf("LoadWithOverlays(test overlay): %v", err)
	}
	m := NewMatcher(g)
	if m.SecondOrderCount() != 1 {
		t.Fatalf("test overlay should yield exactly one evaluable second-order phenomenon, got %d", m.SecondOrderCount())
	}

	const (
		podA = "i|cl|shop|Pod|victim|uid-a"
		podB = "i|cl|shop|Pod|neighbour|uid-b"
		node = "i|cl||Node|worker-1|nodeuid-1"
	)
	rate := func(metric string) []observe.VariableRate {
		return []observe.VariableRate{{
			RuleID: "TEST_RULE", Metric: metric, WindowDelta: 3, Bar: 1, Breached: true,
			Deriv: observe.DerivationRef{StreamID: "s-" + metric, SampleAt: evalAt, How: "rate-guard"},
		}}
	}
	fps := []observe.Fingerprint{
		{CEIKey: podA, Namespace: "shop", Name: "victim", Kind: "Pod", EvaluatedAt: evalAt, Rates: rate("test_sandbox_failures")},
		{CEIKey: podB, Namespace: "shop", Name: "neighbour", Kind: "Pod", EvaluatedAt: evalAt, Rates: rate("test_cni_drops")},
	}
	s := identity.NewEdgeStore(func() time.Time { return evalAt },
		map[identity.EdgeType]time.Duration{identity.EdgeRunsOn: 90 * time.Second}, 24*time.Hour)
	s.Assert(identity.EdgeRunsOn, mustCEI(t, podA), mustCEI(t, node), evalAt.Add(-10*time.Second))
	s.Assert(identity.EdgeRunsOn, mustCEI(t, podB), mustCEI(t, node), evalAt.Add(-10*time.Second))

	out := m.Match(fps, nil, s, w)
	var atA, atB *Finding
	for i := range out {
		if out[i].Phenomenon != "PHEN_CNI_FAILURE" {
			continue
		}
		switch out[i].EntityCEI {
		case podA:
			atA = &out[i]
		case podB:
			atB = &out[i]
		}
	}
	if atA == nil {
		t.Fatalf("CNI_FAILURE must fire at the anchor pod A; findings: %+v", out)
	}
	if atB != nil {
		t.Errorf("pod B has no anchor-side evidence and must stay silent: %+v", atB)
	}
	// Full derivation: A → node → B, both runs-on, both valid; the two-hop
	// evidence cites B.
	if len(atA.SpanPath) != 2 ||
		atA.SpanPath[0].From != podA || atA.SpanPath[0].To != node ||
		atA.SpanPath[1].From != node || atA.SpanPath[1].To != podB {
		t.Errorf("span path must walk A→node→B: %+v", atA.SpanPath)
	}
	var hit *MemberEvidence
	for i := range atA.Members {
		if atA.Members[i].Metric == "test_cni_drops" {
			hit = &atA.Members[i]
		}
	}
	if hit == nil || hit.Neighbour != podB || hit.Hop != 2 || hit.Via != "runs-on→runs-on" || hit.EdgeResult != "valid" {
		t.Errorf("two-hop evidence must cite pod B with the full path: %+v", hit)
	}
	if atA.RequiredMet != 2 {
		t.Errorf("both checked required members met (anchor + two-hop), got %d", atA.RequiredMet)
	}
}

// An entity reachable in ONE hop is never re-listed as a two-hop target — the
// walk reaches OUT, it does not bounce back through the intermediate.
func TestTwoHopExcludesHopOneEntities(t *testing.T) {
	g, err := graph.LoadWithOverlays(kgPath, "testdata/overlays-noisy")
	if err != nil {
		t.Fatal(err)
	}
	m := NewMatcher(g)
	const (
		podA = "i|cl|shop|Pod|victim|uid-a"
		node = "i|cl||Node|worker-1|nodeuid-1"
	)
	// The node itself carries the two-hop metric — but it is a HOP-1 entity
	// from A, so the two-hop check must not be satisfied by it.
	fps := []observe.Fingerprint{
		{CEIKey: podA, Namespace: "shop", Name: "victim", Kind: "Pod", EvaluatedAt: evalAt,
			Rates: []observe.VariableRate{{RuleID: "TEST_RULE", Metric: "test_sandbox_failures",
				WindowDelta: 3, Bar: 1, Breached: true,
				Deriv: observe.DerivationRef{StreamID: "s1", SampleAt: evalAt, How: "rate-guard"}}}},
		{CEIKey: node, Name: "worker-1", Kind: "Node", EvaluatedAt: evalAt,
			Rates: []observe.VariableRate{{RuleID: "TEST_RULE", Metric: "test_cni_drops",
				WindowDelta: 3, Bar: 1, Breached: true,
				Deriv: observe.DerivationRef{StreamID: "s2", SampleAt: evalAt, How: "rate-guard"}}}},
	}
	s := identity.NewEdgeStore(func() time.Time { return evalAt },
		map[identity.EdgeType]time.Duration{identity.EdgeRunsOn: 90 * time.Second}, 24*time.Hour)
	s.Assert(identity.EdgeRunsOn, mustCEI(t, podA), mustCEI(t, node), evalAt.Add(-10*time.Second))

	f := findPhen(m.Match(fps, nil, s, w), "PHEN_CNI_FAILURE")
	if f == nil {
		t.Fatal("anchor evidence should still produce a degraded finding")
	}
	for _, ev := range f.Members {
		if ev.Metric == "test_cni_drops" {
			t.Errorf("the hop-1 node must not satisfy a two-hop member: %+v", ev)
		}
	}
}

// Determinism + the replay property at depth 2: matching over the live store
// and a store rebuilt from its snapshot yields identical findings.
func TestTwoHopSnapshotRoundTrip(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	fps := []observe.Fingerprint{ioPressuredNodeFP(), fullPVCFP(observe.StateAbove)}
	fresh := evalAt.Add(-10 * time.Second)
	live := storageTopo(t, fresh, fresh)

	a := m.Match(fps, nil, live, w)
	b := m.Match(fps, nil, live, w)
	if !reflect.DeepEqual(a, b) {
		t.Error("Match is not deterministic over the same store at depth 2")
	}
	rebuilt, err := identity.NewEdgeStoreFromSnapshot(live.Snapshot(),
		map[identity.EdgeType]time.Duration{
			identity.EdgeRunsOn: 90 * time.Second,
			identity.EdgeMounts: 90 * time.Second,
		})
	if err != nil {
		t.Fatal(err)
	}
	c := m.Match(fps, nil, rebuilt, w)
	if !reflect.DeepEqual(a, c) {
		t.Errorf("snapshot round-trip changed two-hop findings:\nlive:    %+v\nrebuilt: %+v", a, c)
	}
}
