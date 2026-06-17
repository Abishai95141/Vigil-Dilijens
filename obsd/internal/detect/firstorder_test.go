package detect

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/observe"
)

// First-order matching (doc 07 M2) golden tests, built on the REAL ontology
// (v0.2.0 conditions): THROTTLING_CASCADE (Container anchor, node PSI one
// runs-on hop away) and EVICTION_MEMORY (Node anchor). The trust-critical
// assertions are the validity-contract ones: a stale edge DEGRADES, an absent
// edge NEVER fabricates a cross-entity co-occurrence (doc 07 §3.2).

const (
	containerKey = "i|cl|shop|Container|app|poduid-1/app"
	podKey       = "i|cl|shop|Pod|app-7d9|poduid-1"
	nodeKey      = "i|cl||Node|worker-1|nodeuid-1"
)

var w = identity.TimeWindow{Start: evalAt.Add(-90 * time.Second), End: evalAt}

// throttledContainerFP: the anchor's required T0 — throttled-period ratio crossed.
func throttledContainerFP() observe.Fingerprint {
	return observe.Fingerprint{
		CEIKey: containerKey, Namespace: "shop", Name: "app", Kind: "Container",
		EvaluatedAt: evalAt,
		Thresholds: []observe.VariableThreshold{{
			RuleID: "THR_CONTAINER_CPU_THROTTLE_RATIO",
			Metric: "container_cpu_cfs_throttled_periods_total",
			State:  observe.StateAbove, BarSource: "default", Flagged: true,
			Deriv: observe.DerivationRef{StreamID: "s-ratio", SampleAt: evalAt, How: "counter-ratio"},
		}},
	}
}

// nodePSIFP: the neighbour's required T0+ — CPU some-stall fraction, at the
// given ladder state.
func nodePSIFP(state observe.ThresholdState) observe.Fingerprint {
	return observe.Fingerprint{
		CEIKey: nodeKey, Name: "worker-1", Kind: "Node",
		EvaluatedAt: evalAt,
		Thresholds: []observe.VariableThreshold{{
			RuleID: "THR_NODE_CPU_PSI_STALL",
			Metric: "node_pressure_cpu_waiting_seconds_total",
			State:  state, BarSource: "default", Flagged: true,
			Deriv: observe.DerivationRef{StreamID: "s-psi", SampleAt: evalAt, How: "counter-rate"},
		}},
	}
}

func mustCEI(t *testing.T, key string) identity.CEI {
	t.Helper()
	cei, err := identity.ParseKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return cei
}

// edgeStore builds a store whose pod→node runs-on edge was last confirmed at
// the given instant (the validity-contract input under test).
func edgeStore(t *testing.T, confirmedAt time.Time) *identity.EdgeStore {
	t.Helper()
	s := identity.NewEdgeStore(func() time.Time { return evalAt },
		map[identity.EdgeType]time.Duration{identity.EdgeRunsOn: 90 * time.Second}, 24*time.Hour)
	s.Assert(identity.EdgeRunsOn, mustCEI(t, podKey), mustCEI(t, nodeKey), confirmedAt.Add(-time.Hour))
	s.Assert(identity.EdgeRunsOn, mustCEI(t, podKey), mustCEI(t, nodeKey), confirmedAt)
	return s
}

func findPhen(fs []Finding, id string) *Finding {
	for i := range fs {
		if fs[i].Phenomenon == id {
			return &fs[i]
		}
	}
	return nil
}

// The headline M2 case: throttled container + pressured node, joined across a
// FRESH runs-on edge ⇒ a FULL first-order match with complete derivation (span
// path, neighbour evidence, edge verdicts).
func TestThrottlingCascadeFullAcrossValidEdge(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	fps := []observe.Fingerprint{throttledContainerFP(), nodePSIFP(observe.StateAbove)}
	out := m.Match(fps, nil, edgeStore(t, evalAt.Add(-10*time.Second)), w)
	f := findPhen(out, "PHEN_THROTTLING_CASCADE")
	if f == nil {
		t.Fatalf("THROTTLING_CASCADE should fire; findings: %+v", out)
	}
	if f.EntityCEI != containerKey {
		t.Errorf("anchored at %s, want the container", f.EntityCEI)
	}
	if f.Quality != QualityFull || f.RequiredTotal != 2 || f.RequiredMet != 2 {
		t.Errorf("want FULL 2/2, got %s %d/%d (unobs %d, suspect %v)",
			f.Quality, f.RequiredMet, f.RequiredTotal, f.RequiredUnobserved, f.SuspectEdges)
	}
	if f.Span != "first-order" {
		t.Errorf("span = %q, want first-order", f.Span)
	}
	// The span instantiation: pod → node over runs-on, valid.
	if len(f.SpanPath) != 1 || f.SpanPath[0].Type != "runs-on" || f.SpanPath[0].From != podKey ||
		f.SpanPath[0].To != nodeKey || f.SpanPath[0].Result != "valid" {
		t.Errorf("span path = %+v, want one valid runs-on pod→node step", f.SpanPath)
	}
	// Neighbour evidence carries the entity + edge it crossed.
	var psi *MemberEvidence
	for i := range f.Members {
		if f.Members[i].Metric == "node_pressure_cpu_waiting_seconds_total" {
			psi = &f.Members[i]
		}
	}
	if psi == nil || psi.Neighbour != nodeKey || psi.Via != "runs-on" || psi.EdgeResult != "valid" {
		t.Errorf("PSI evidence must cite the node across runs-on: %+v", psi)
	}
	if !psi.BarFlagged {
		t.Error("the PSI default bar's flagged provenance must reach the evidence")
	}
}

// THE trust-critical test (doc 07 §3.2, the Phase-1 exit property): staleness
// degrades, absence never fabricates.
func TestStaleEdgeDegradesNeverFabricates(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	fps := []observe.Fingerprint{throttledContainerFP(), nodePSIFP(observe.StateAbove)}

	// (a) SUSPECT: last confirmation far beyond the 90s budget as of window end.
	out := m.Match(fps, nil, edgeStore(t, evalAt.Add(-10*time.Minute)), w)
	f := findPhen(out, "PHEN_THROTTLING_CASCADE")
	if f == nil {
		t.Fatal("a suspect edge must still surface the match — degraded, not dropped")
	}
	if f.Quality != QualityDegraded || len(f.SuspectEdges) == 0 {
		t.Errorf("suspect edge must degrade and be NAMED: quality=%s suspect=%v", f.Quality, f.SuspectEdges)
	}
	if f.SpanPath[0].Result != "suspect" {
		t.Errorf("span path must carry the suspect verdict: %+v", f.SpanPath)
	}

	// (b) ABSENT: the edge was retracted before the window — the node's (real,
	// crossed) PSI evidence is NOT a co-occurrence with this container. The match
	// must not cite the node, and must not claim completeness.
	s := identity.NewEdgeStore(func() time.Time { return evalAt },
		map[identity.EdgeType]time.Duration{identity.EdgeRunsOn: 90 * time.Second}, 24*time.Hour)
	s.Assert(identity.EdgeRunsOn, mustCEI(t, podKey), mustCEI(t, nodeKey), evalAt.Add(-2*time.Hour))
	s.Retract(identity.EdgeRunsOn, podKey, nodeKey, evalAt.Add(-time.Hour)) // long before the window
	out = m.Match(fps, nil, s, w)
	f = findPhen(out, "PHEN_THROTTLING_CASCADE")
	if f == nil {
		t.Fatal("anchor evidence alone should still produce a DEGRADED finding")
	}
	if f.Quality != QualityDegraded || f.RequiredMet != 1 || f.RequiredUnobserved != 1 {
		t.Errorf("absent edge: want degraded 1 met / 1 unobservable, got %s %d/%d", f.Quality, f.RequiredMet, f.RequiredUnobserved)
	}
	for _, ev := range f.Members {
		if ev.Neighbour != "" {
			t.Errorf("FABRICATION: evidence cited across an absent edge: %+v", ev)
		}
	}
	if len(f.SpanPath) != 0 {
		t.Errorf("no edge was traversable; span path must be empty: %+v", f.SpanPath)
	}
}

// Window intersection semantics: an edge retracted INSIDE the window provably
// existed during the overlap (supports); retracted before it, or asserted after
// it, is absent.
func TestEdgeValidityWindowIntersection(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	fps := []observe.Fingerprint{throttledContainerFP(), nodePSIFP(observe.StateAbove)}

	mk := func() *identity.EdgeStore {
		return identity.NewEdgeStore(func() time.Time { return evalAt },
			map[identity.EdgeType]time.Duration{identity.EdgeRunsOn: 90 * time.Second}, 24*time.Hour)
	}

	// Retracted mid-window: still a co-occurrence (ground truth of the interval).
	s := mk()
	s.Assert(identity.EdgeRunsOn, mustCEI(t, podKey), mustCEI(t, nodeKey), evalAt.Add(-time.Hour))
	s.Retract(identity.EdgeRunsOn, podKey, nodeKey, evalAt.Add(-30*time.Second))
	f := findPhen(m.Match(fps, nil, s, w), "PHEN_THROTTLING_CASCADE")
	if f == nil || f.Quality != QualityFull {
		t.Errorf("an edge retracted inside the window provably overlapped it — want FULL, got %+v", f)
	}

	// Asserted after the window end: absent.
	s = mk()
	s.Assert(identity.EdgeRunsOn, mustCEI(t, podKey), mustCEI(t, nodeKey), evalAt.Add(time.Minute))
	f = findPhen(m.Match(fps, nil, s, w), "PHEN_THROTTLING_CASCADE")
	if f == nil || f.RequiredMet != 1 || len(f.SpanPath) != 0 {
		t.Errorf("an edge asserted after the window must not support: %+v", f)
	}
}

// A reachable neighbour whose member is observable and NOT met fails the
// conjunction — throttling without node pressure is not the cascade phenomenon.
func TestNeighbourNotMetFailsConjunction(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	fps := []observe.Fingerprint{throttledContainerFP(), nodePSIFP(observe.StateBelow)}
	if f := findPhen(m.Match(fps, nil, edgeStore(t, evalAt.Add(-10*time.Second)), w), "PHEN_THROTTLING_CASCADE"); f != nil {
		t.Errorf("healthy node PSI must fail the conjunction, got %+v", f)
	}
}

// First-order phenomena are evaluated at their AUTHORED anchor only — the node
// (which carries the PSI variable) must not mint a THROTTLING_CASCADE finding.
func TestAnchorKindGate(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	fps := []observe.Fingerprint{nodePSIFP(observe.StateAbove)} // no container anchor present
	for _, f := range m.Match(fps, nil, edgeStore(t, evalAt.Add(-10*time.Second)), w) {
		if f.Phenomenon == "PHEN_THROTTLING_CASCADE" {
			t.Errorf("cascade evaluated off-anchor at %s", f.EntityCEI)
		}
	}
}

// A container whose pod is unknown to the topology cannot reach neighbours: the
// neighbour member is unobservable WITH THE REASON STATED, never guessed.
func TestContainmentMissingPodStated(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	s := identity.NewEdgeStore(func() time.Time { return evalAt },
		map[identity.EdgeType]time.Duration{identity.EdgeRunsOn: 90 * time.Second}, 24*time.Hour)
	fps := []observe.Fingerprint{throttledContainerFP(), nodePSIFP(observe.StateAbove)}
	f := findPhen(m.Match(fps, nil, s, w), "PHEN_THROTTLING_CASCADE")
	if f == nil || f.Quality != QualityDegraded {
		t.Fatalf("want a degraded match with the containment gap stated, got %+v", f)
	}
	found := false
	for _, u := range f.Unobservable {
		if strings.Contains(u, "containing pod absent from topology") {
			found = true
		}
	}
	if !found {
		t.Errorf("the containment gap must be stated on the finding: %v", f.Unobservable)
	}
}

// Node-anchored EVICTION_MEMORY: MemAvailable crossing its allocatable-relative
// bar fires a DEGRADED match at the node (its other required members have no
// scrapable channel here), with every gap enumerated.
func TestEvictionMemoryDegradedAtNode(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	nodeFP := observe.Fingerprint{
		CEIKey: nodeKey, Name: "worker-1", Kind: "Node", EvaluatedAt: evalAt,
		Thresholds: []observe.VariableThreshold{{
			RuleID: "THR_NODE_MEMAVAILABLE_VS_ALLOCATABLE",
			Metric: "node_memory_MemAvailable_bytes",
			State:  observe.StateAbove, BarSource: "config",
			Deriv: observe.DerivationRef{StreamID: "s-mem", SampleAt: evalAt, How: "gauge-level"},
		}},
	}
	out := m.Match([]observe.Fingerprint{nodeFP}, nil, edgeStore(t, evalAt.Add(-10*time.Second)), w)
	f := findPhen(out, "PHEN_EVICTION_MEMORY")
	if f == nil {
		t.Fatalf("EVICTION_MEMORY should fire degraded at the node; got %+v", out)
	}
	if f.Quality != QualityDegraded || f.RequiredMet != 1 {
		t.Errorf("want degraded with 1 required met, got %s %d/%d", f.Quality, f.RequiredMet, f.RequiredTotal)
	}
	if f.RequiredUnobserved == 0 || len(f.Unobservable) != f.RequiredUnobserved {
		t.Errorf("every unobservable required member must be enumerated: unobs=%d list=%v", f.RequiredUnobserved, f.Unobservable)
	}
}

// Determinism + the replay property (doc 07 §3.7): matching over the live store
// and over a store REBUILT FROM ITS SNAPSHOT yields identical findings.
func TestSnapshotRoundTripIdenticalFindings(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	fps := []observe.Fingerprint{throttledContainerFP(), nodePSIFP(observe.StateAbove)}
	live := edgeStore(t, evalAt.Add(-10*time.Second))

	a := m.Match(fps, nil, live, w)
	b := m.Match(fps, nil, live, w)
	if !reflect.DeepEqual(a, b) {
		t.Error("Match is not deterministic over the same store")
	}

	rebuilt, err := identity.NewEdgeStoreFromSnapshot(live.Snapshot(),
		map[identity.EdgeType]time.Duration{identity.EdgeRunsOn: 90 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	c := m.Match(fps, nil, rebuilt, w)
	if !reflect.DeepEqual(a, c) {
		t.Errorf("snapshot round-trip changed findings:\nlive:    %+v\nrebuilt: %+v", a, c)
	}
}

// The matcher's spanned census: v0.2.0 authored checks for four first-order
// phenomena; v0.3.0 adds OOM_KILL_CGROUP (first-order) and STORAGE_SATURATION
// (second-order, evaluated by the two-hop walk — never skipped).
func TestSpannedCensus(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	// v0.2.0: four first-order; v0.3.0: +OOM_KILL_CGROUP (5); v0.7.0 (DISK):
	// +DISK_PID_INODE_PRESSURE (6); v0.8.0 (PVC): +VOLUME_MOUNT_FAILURE PVC-anchor check (7).
	if m.FirstOrderCount() != 7 {
		t.Errorf("first-order phenomena with checks = %d, want 7", m.FirstOrderCount())
	}
	if m.SecondOrderCount() != 1 {
		t.Errorf("second-order phenomena with checks = %d, want 1 (STORAGE_SATURATION)", m.SecondOrderCount())
	}
}

// Regression (07-M2 live finding): a container with NO anchor-side evidence —
// the cpu-hog shape: no CPU limit, so the throttle-ratio member is not even
// instantiable — must NOT fire the cascade on the node's PSI alone. Neighbour
// evidence corroborates; it never alone constitutes a phenomenon about the
// anchor. (Observed live: every container on the pressured node lit up
// "degraded cascade" before this rule.)
func TestNeighbourEvidenceAloneNeverFires(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	hog := observe.Fingerprint{
		// A real fingerprint (it has SOME variable — its memory bar) but zero
		// throttle-ratio evidence.
		CEIKey: podKey, Namespace: "shop", Name: "app", Kind: "Container",
		EvaluatedAt: evalAt,
		Thresholds: []observe.VariableThreshold{{
			RuleID: "THR_CONTAINER_MEM_WORKING_SET_VS_LIMIT",
			Metric: "container_memory_working_set_bytes",
			State:  observe.StateBelow, BarSource: "config",
			Deriv: observe.DerivationRef{StreamID: "s-mem", SampleAt: evalAt, How: "gauge-level"},
		}},
	}
	fps := []observe.Fingerprint{hog, nodePSIFP(observe.StateAbove)}
	for _, f := range m.Match(fps, nil, edgeStore(t, evalAt.Add(-10*time.Second)), w) {
		if f.Phenomenon == "PHEN_THROTTLING_CASCADE" {
			t.Errorf("cascade fired with zero anchor evidence (neighbour PSI alone): %+v", f)
		}
	}
}
