package detect

import (
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/observe"
)

// Metamorphic property tests layered on the golden-corpus tests (doc 07 §3.7,
// charter doc 01). Where the golden tests pin EXACT findings for one scenario,
// these pin a RELATION between two scenarios that differ by a controlled
// transform — the kind of bug a single golden file cannot see:
//
//   (1) irrelevant-entity invariance — adding an entity/stream that is not the
//       anchor of, and not topologically reachable by, an existing match must
//       NOT perturb that match. (A leak through the index/sort/neighbour walk
//       would let one customer's noise rewrite another's findings.)
//   (2) co-occurrence is never upgraded to cause — a topological match inside
//       the edge-validity window is MEASURED with an AUTHORED reference
//       attached, never fused into a causal claim. The presence or absence of
//       an authored phenomenon_relation must not change the MEASURED finding by
//       one byte; it only adds a separately-labelled Cascade.
//   (3) determinism under input reordering — permuting the fingerprint slice
//       (and the topology assertion order) yields identical findings.

// ---------------------------------------------------------------------------
// Shared helpers for the metamorphic transforms.
// ---------------------------------------------------------------------------

// findingsFor returns the subset of findings anchored on one entity, so two runs
// can be compared on a SINGLE entity even when the second run adds others.
func findingsFor(fs []Finding, cei string) []Finding {
	var out []Finding
	for _, f := range fs {
		if f.EntityCEI == cei {
			out = append(out, f)
		}
	}
	return out
}

// unrelatedThrottledFP is a SECOND throttled container (its own pod, its own
// node) — same phenomenon signature as the headline cascade fixture but a
// distinct identity that shares no edge with the original. Used as the
// "irrelevant entity" added to a scene.
func unrelatedThrottledFP() observe.Fingerprint {
	return observe.Fingerprint{
		CEIKey: "i|cl|other|Container|noise|poduid-9/noise", Namespace: "other", Name: "noise", Kind: "Container",
		EvaluatedAt: evalAt,
		Thresholds: []observe.VariableThreshold{{
			RuleID: "THR_CONTAINER_CPU_THROTTLE_RATIO",
			Metric: "container_cpu_cfs_throttled_periods_total",
			State:  observe.StateAbove, BarSource: "default", Flagged: true,
			Deriv: observe.DerivationRef{StreamID: "s-ratio-9", SampleAt: evalAt, How: "counter-ratio"},
		}},
	}
}

// unrelatedNodePSIFP is a DIFFERENT pressured node, never on the original
// container's runs-on path.
func unrelatedNodePSIFP() observe.Fingerprint {
	return observe.Fingerprint{
		CEIKey: "i|cl||Node|worker-9|nodeuid-9", Name: "worker-9", Kind: "Node",
		EvaluatedAt: evalAt,
		Thresholds: []observe.VariableThreshold{{
			RuleID: "THR_NODE_CPU_PSI_STALL",
			Metric: "node_pressure_cpu_waiting_seconds_total",
			State:  observe.StateAbove, BarSource: "default", Flagged: true,
			Deriv: observe.DerivationRef{StreamID: "s-psi-9", SampleAt: evalAt, How: "counter-rate"},
		}},
	}
}

// graphWithoutRelation returns a copy of g in which the single authored
// phenomenon_relation (srcID --role--> dstID) is stripped from the source
// phenomenon, leaving everything the MEASURED path reads — phenomena, member
// checks, threshold rules, version — byte-for-byte identical. This is the
// controlled "remove the authored relation" transform: only the AUTHORED
// cascade layer (which reads g.Phenomena[id].Relations) can possibly differ.
//
// It deep-copies only the Phenomena map (so the shared loaded graph is never
// mutated); all other fields, including Version, are carried by reference so a
// finding minted against the copy is indistinguishable from one minted against
// the original. The test below proves the transform is load-bearing (it really
// dissolves the cascade) and that the MEASURED findings nonetheless do not move.
func graphWithoutRelation(t *testing.T, g *graph.Graph, srcID, dstID, role string) *graph.Graph {
	t.Helper()
	clone := *g // shallow struct copy: Version, Checks, Rules, edges all shared by reference
	clone.Phenomena = make(map[string]*graph.Phenomenon, len(g.Phenomena))
	removed := false
	for id, p := range g.Phenomena {
		pc := *p // copy the phenomenon value so we can swap its Relations slice
		if id == srcID {
			kept := make([]graph.Relation, 0, len(p.Relations))
			for _, r := range p.Relations {
				if r.TargetID == dstID && r.Role == role {
					removed = true
					continue
				}
				kept = append(kept, r)
			}
			pc.Relations = kept
		}
		clone.Phenomena[id] = &pc
	}
	if !removed {
		t.Fatalf("setup: relation %s --%s--> %s not present in graph (fixture drifted?)", srcID, role, dstID)
	}
	return &clone
}

// ---------------------------------------------------------------------------
// (1) IRRELEVANT-ENTITY INVARIANCE
// ---------------------------------------------------------------------------

// Adding a completely unrelated throttled container (its own pod/node, no edge
// to the original) must leave the ORIGINAL container's THROTTLING_CASCADE
// finding byte-identical. The unrelated noise gets its own (separate) finding;
// it never reaches into the established one.
func TestIrrelevantEntityDoesNotPerturbExistingMatch(t *testing.T) {
	m := NewMatcher(loadGraph(t))

	base := []observe.Fingerprint{throttledContainerFP(), nodePSIFP(observe.StateAbove)}
	topo := edgeStore(t, evalAt.Add(-10*time.Second)) // valid pod→node for the ORIGINAL

	before := m.Match(base, nil, topo, w)
	origBefore := findingsFor(before, containerKey)
	if findPhen(origBefore, "PHEN_THROTTLING_CASCADE") == nil {
		t.Fatalf("setup: the original cascade must fire FULL before adding noise; got %+v", origBefore)
	}

	// Transform: add an unrelated throttled container + its own pressured node,
	// joined by their OWN runs-on edge (so the noise is itself a real scene, not
	// inert). Crucially this edge does not touch worker-1 or the original pod.
	withNoise := append(append([]observe.Fingerprint(nil), base...), unrelatedThrottledFP(), unrelatedNodePSIFP())
	topo.Assert(identity.EdgeRunsOn,
		mustCEI(t, "i|cl|other|Pod|noise-pod|poduid-9"),
		mustCEI(t, "i|cl||Node|worker-9|nodeuid-9"), evalAt.Add(-10*time.Second))

	after := m.Match(withNoise, nil, topo, w)
	origAfter := findingsFor(after, containerKey)

	if !reflect.DeepEqual(origBefore, origAfter) {
		t.Errorf("irrelevant entity changed the original entity's findings:\nbefore: %+v\nafter:  %+v", origBefore, origAfter)
	}

	// Non-vacuity guard: the noise really was processed (it produced its own
	// finding on its own anchor) — otherwise the invariance above would be
	// trivially true because nothing was added.
	if findPhen(findingsFor(after, "i|cl|other|Container|noise|poduid-9/noise"), "PHEN_THROTTLING_CASCADE") == nil {
		t.Errorf("the added entity must itself be evaluated (else the invariance is vacuous); after: %+v", after)
	}
}

// Even when the irrelevant addition shares the SAME phenomenon and a near-
// identical signature, it must not bleed neighbour evidence into the original
// (the index is keyed by CEI — a same-metric variable on a different entity is
// not the original's neighbour). Here the original is DEGRADED (its node edge is
// absent): adding the unrelated pressured node must not "rescue" it to FULL by
// cross-entity contamination.
func TestIrrelevantNeighbourNeverRescuesDegraded(t *testing.T) {
	m := NewMatcher(loadGraph(t))

	// Original: throttled container, but NO runs-on edge to its node ⇒ degraded,
	// neighbour PSI unobservable across the (absent) span.
	emptyTopo := identity.NewEdgeStore(func() time.Time { return evalAt },
		map[identity.EdgeType]time.Duration{identity.EdgeRunsOn: 90 * time.Second}, 24*time.Hour)
	base := []observe.Fingerprint{throttledContainerFP()}
	degradedBefore := findPhen(findingsFor(m.Match(base, nil, emptyTopo, w), containerKey), "PHEN_THROTTLING_CASCADE")
	if degradedBefore == nil || degradedBefore.Quality != QualityDegraded {
		t.Fatalf("setup: original must be degraded with no traversable node; got %+v", degradedBefore)
	}

	// Transform: add an unrelated pressured node (worker-9) carrying the SAME PSI
	// metric. It is not on any runs-on edge from the original pod.
	withNode := append(append([]observe.Fingerprint(nil), base...), unrelatedNodePSIFP())
	emptyTopo.Assert(identity.EdgeRunsOn,
		mustCEI(t, "i|cl|other|Pod|noise-pod|poduid-9"),
		mustCEI(t, "i|cl||Node|worker-9|nodeuid-9"), evalAt.Add(-10*time.Second))

	degradedAfter := findPhen(findingsFor(m.Match(withNode, nil, emptyTopo, w), containerKey), "PHEN_THROTTLING_CASCADE")
	if degradedAfter == nil {
		t.Fatal("the original degraded finding must survive the addition")
	}
	if degradedAfter.Quality != QualityDegraded || degradedAfter.RequiredMet != 1 {
		t.Errorf("FABRICATION: an unrelated node's PSI upgraded the original match: %+v", degradedAfter)
	}
	for _, ev := range degradedAfter.Members {
		if ev.Neighbour == "i|cl||Node|worker-9|nodeuid-9" {
			t.Errorf("FABRICATION: evidence cited an unrelated node as the original's neighbour: %+v", ev)
		}
	}
	if !reflect.DeepEqual(*degradedBefore, *degradedAfter) {
		t.Errorf("the original degraded finding must be byte-identical with/without the unrelated node:\nbefore: %+v\nafter:  %+v", *degradedBefore, *degradedAfter)
	}
}

// Entity-local matches (M1) carry the same invariance: a healthy unrelated
// entity, or a second leaking entity, never alters another entity's MEMORY_LEAK
// finding.
func TestEntityLocalIrrelevantInvariance(t *testing.T) {
	m := NewMatcher(loadGraph(t))

	leak := leakingFP(evalAt)
	before := m.Match([]observe.Fingerprint{leak}, nil, nil, w)
	origBefore := findingsFor(before, csPodKey)
	if findPhen(origBefore, "PHEN_MEMORY_LEAK") == nil {
		t.Fatalf("setup: MEMORY_LEAK must fire on the original; got %+v", origBefore)
	}

	// A second, independent leaking pod + a healthy bystander.
	other := leakingFP(evalAt)
	other.CEIKey = "i|cl|shop|Pod|web-b|uid-b"
	other.Name = "web-b"
	healthy := observe.Fingerprint{CEIKey: "i|cl|shop|Pod|idle|uid-idle", Name: "idle", Kind: "Container", EvaluatedAt: evalAt}

	after := m.Match([]observe.Fingerprint{leak, other, healthy}, nil, nil, w)
	origAfter := findingsFor(after, csPodKey)

	if !reflect.DeepEqual(origBefore, origAfter) {
		t.Errorf("an unrelated entity-local entity perturbed the original leak finding:\nbefore: %+v\nafter:  %+v", origBefore, origAfter)
	}
	// Non-vacuity: the second leaker produced its own finding.
	if findPhen(findingsFor(after, "i|cl|shop|Pod|web-b|uid-b"), "PHEN_MEMORY_LEAK") == nil {
		t.Error("the second leaking entity must produce its own finding (else invariance is vacuous)")
	}
}

// ---------------------------------------------------------------------------
// (2) CO-OCCURRENCE IS NEVER UPGRADED TO CAUSE
// ---------------------------------------------------------------------------

// causalWords / assertNoCausalClaim are a cheap FUTURE-REGRESSION NET, not the
// proof of the co-occurrence-never-cause invariant. That invariant is carried
// structurally by the assertions in this file: that a topological match surfaces
// as a span path of topology verdicts (TestTopologicalMatchSurfacesAsCoOccurrence
// NotCause) and that removing the authored relation leaves the MEASURED findings
// byte-identical (TestAuthoredRelationDoesNotStrengthenMeasuredFindings). This
// substring scan only guards against a later change that starts splicing
// causal vocabulary ("because", "caused by", "root cause") into a string detect
// itself emits — it would catch such a regression, but its passing today proves
// nothing on its own (today's authored notes simply contain no such words).
// It is run on detect's OWN output strings, never on the authored graph text,
// which is surfaced verbatim by contract.
var causalWords = []string{"caused by", "because", "therefore", "proves", "root cause", "leads to causing"}

func assertNoCausalClaim(t *testing.T, where, s string) {
	t.Helper()
	low := strings.ToLower(s)
	for _, w := range causalWords {
		if strings.Contains(low, w) {
			t.Errorf("%s contains causal vocabulary %q (co-occurrence upgraded to cause): %q", where, w, s)
		}
	}
}

// A FULL first-order match across a VALID edge is surfaced as a co-occurrence:
// the neighbour evidence cites the edge it crossed (Via + EdgeResult) and the
// finding's Span/SpanPath describe the topological path — MEASURED. The only
// "why" on the evidence is the AUTHORED member note (carried verbatim). Nothing
// the matcher emits asserts that one member CAUSED another.
func TestTopologicalMatchSurfacesAsCoOccurrenceNotCause(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	fps := []observe.Fingerprint{throttledContainerFP(), nodePSIFP(observe.StateAbove)}
	f := findPhen(m.Match(fps, nil, edgeStore(t, evalAt.Add(-10*time.Second)), w), "PHEN_THROTTLING_CASCADE")
	if f == nil || f.Quality != QualityFull {
		t.Fatalf("setup: a full first-order match must fire; got %+v", f)
	}

	// The match is a co-occurrence ACROSS an authored edge, made concrete: the
	// span path is present and every step is a real topology verdict, not a claim.
	if f.Span != "first-order" || len(f.SpanPath) == 0 {
		t.Fatalf("a topological match must carry its span path (the co-occurrence path): %+v", f)
	}
	for _, s := range f.SpanPath {
		if s.Result != "valid" && s.Result != "suspect" {
			t.Errorf("span step result must be a topology verdict, not a derived claim: %+v", s)
		}
	}
	// The neighbour evidence is keyed to the edge crossed — co-occurrence, with
	// the authored note as the ONLY why.
	var psi *MemberEvidence
	for i := range f.Members {
		if f.Members[i].Neighbour != "" {
			psi = &f.Members[i]
		}
	}
	if psi == nil {
		t.Fatal("the cross-entity member must cite the neighbour it co-occurred with")
	}
	if psi.Via == "" || psi.EdgeResult == "" {
		t.Errorf("neighbour evidence must name the edge crossed (co-occurrence), got %+v", psi)
	}
	if psi.Note == "" {
		t.Error("the only why is the AUTHORED member note — it must be present")
	}
	assertNoCausalClaim(t, "member note", psi.Note)
	for _, u := range f.Unobservable {
		assertNoCausalClaim(t, "unobservable label", u)
	}
}

// THE charter property, metamorphic form: a recognized Cascade is an AUTHORED
// relation made manifest — its Why is the graph note VERBATIM and its Related is
// a topology descriptor. The metamorphic transform is to remove that authored
// MEMORY_LEAK→OOM_KILL phenomenon_relation from the graph and re-run BOTH Match
// and Cascades: the MEASURED Finding slice must be byte-identical (the AUTHORED
// edge never reaches into the MEASURED class — join, never fuse), while the
// separately-surfaced Cascade DISAPPEARS (proving the transform is load-bearing,
// not vacuous). The two classes are joined at the surface, never fused.
func TestAuthoredRelationDoesNotStrengthenMeasuredFindings(t *testing.T) {
	base := loadGraph(t)
	m := NewMatcher(base)
	topo := cascadeTopo(t)

	// One entity carrying BOTH the leak signature and the OOM breach — the
	// trigger and downstream of the authored MEMORY_LEAK→OOM_KILL relation.
	fp := leakingFP(evalAt)
	fp.Rates = oomKilledFP(evalAt).Rates
	findings := m.Match([]observe.Fingerprint{fp}, nil, topo, w)

	// The two MEASURED findings stand on their own evidence.
	leak := findPhen(findings, "PHEN_MEMORY_LEAK")
	oom := findPhen(findings, "PHEN_OOM_KILL_CGROUP")
	if leak == nil || oom == nil {
		t.Fatalf("setup: both findings must fire on the entity; got %+v", findings)
	}

	// The authored relation surfaces — SEPARATELY — as a Cascade. Its Why is the
	// verbatim authored note; its Related is a topology relation; the temporal tag
	// is the authored one. None of these is a derived causal statement.
	cs := m.Cascades(evalAt, findings, NewCascadeTracker(10*time.Minute), topo, w)
	c := findCascade(cs, "PHEN_MEMORY_LEAK", "PHEN_OOM_KILL_CGROUP")
	if c == nil {
		t.Fatalf("the authored relation must surface as a cascade: %+v", cs)
	}
	if c.Why != "Eventual outcome" {
		t.Errorf("cascade Why must be the AUTHORED note verbatim, got %q", c.Why)
	}
	if c.Related != "same-entity" {
		t.Errorf("cascade Related must be a topology descriptor, got %q", c.Related)
	}
	assertNoCausalClaim(t, "cascade Why", c.Why)

	// ----- THE metamorphic transform: remove the authored relation entirely. -----
	// A second graph identical to the first EXCEPT the MEMORY_LEAK→OOM_KILL
	// phenomenon_relation is stripped. A matcher over it sees the same phenomena,
	// the same checks, the same threshold rules, the same version.
	noRel := graphWithoutRelation(t, base, "PHEN_MEMORY_LEAK", "PHEN_OOM_KILL_CGROUP", "downstream")
	mNoRel := NewMatcher(noRel)

	// The MEASURED layer: Match against the relation-absent graph must yield a
	// byte-identical Finding slice. The AUTHORED edge contributes ZERO to MEASURED.
	findingsNoRel := mNoRel.Match([]observe.Fingerprint{fp}, nil, topo, w)
	if !reflect.DeepEqual(findings, findingsNoRel) {
		t.Errorf("removing the authored relation changed the MEASURED findings (it must not):\nwith:    %+v\nwithout: %+v", findings, findingsNoRel)
	}

	// The AUTHORED layer: with the relation gone, the cascade must NOT surface —
	// this is what makes the byte-identity above non-vacuous (the relation really
	// was the only thing carrying the story, and it lived purely in the AUTHORED
	// channel).
	csNoRel := mNoRel.Cascades(evalAt, findingsNoRel, NewCascadeTracker(10*time.Minute), topo, w)
	if findCascade(csNoRel, "PHEN_MEMORY_LEAK", "PHEN_OOM_KILL_CGROUP") != nil {
		t.Errorf("removing the authored relation must dissolve the cascade (it only lived in the AUTHORED channel): %+v", csNoRel)
	}

	// And the transform left the original graph untouched (no aliasing leak): the
	// relation still surfaces on the unmodified matcher.
	if findCascade(m.Cascades(evalAt, findings, NewCascadeTracker(10*time.Minute), topo, w), "PHEN_MEMORY_LEAK", "PHEN_OOM_KILL_CGROUP") == nil {
		t.Error("the transform must copy, not mutate: the original cascade must still surface")
	}

	// A weaker but still-useful invariant: running cascade recognition must not
	// mutate the MEASURED findings in place (recomputing Match yields the same).
	again := m.Match([]observe.Fingerprint{fp}, nil, topo, w)
	if !reflect.DeepEqual(findings, again) {
		t.Error("running cascade recognition must not mutate the MEASURED findings")
	}
	// The finding structs expose no causal field — their only cross-phenomenon
	// surface is BlastRadius (an authored, separately-labelled at-risk set).
	for _, f := range []*Finding{leak, oom} {
		for _, ev := range f.Members {
			assertNoCausalClaim(t, "finding member note", ev.Note)
		}
	}
}

// Topological relatedness is necessary for a cascade EVEN when an authored
// relation and suggestive timing both exist: a leak on one pod and an OOM on an
// edge-less other pod is two findings, never a story. This is the metamorphic
// contrapositive of the headline cascade test — the SAME authored relation, the
// SAME timing, only the topology removed, yields NO cascade. (Co-occurrence
// requires actual co-location; timing alone never manufactures cause.)
func TestCascadeNeedsTopologyNotJustAuthoredRelation(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	topo := cascadeTopo(t) // only web-a's pod is wired
	tracker := NewCascadeTracker(10 * time.Minute)

	// Trigger on an UNWIRED pod, one minute before the downstream.
	trig := leakingFP(evalAt.Add(-time.Minute))
	trig.CEIKey = "i|cl|shop|Pod|lonely|uid-lonely"
	trig.Name = "lonely"
	w1 := identity.TimeWindow{Start: evalAt.Add(-150 * time.Second), End: evalAt.Add(-time.Minute)}
	tracker.Observe(evalAt.Add(-time.Minute), m.Match([]observe.Fingerprint{trig}, nil, topo, w1))

	// Downstream on the wired pod NOW. Authored relation + good timing both hold.
	cs := m.Cascades(evalAt, m.Match([]observe.Fingerprint{oomKilledFP(evalAt)}, nil, topo, w), tracker, topo, w)
	if findCascade(cs, "PHEN_MEMORY_LEAK", "PHEN_OOM_KILL_CGROUP") != nil {
		t.Errorf("no shared topology ⇒ no story, even with an authored relation and tight timing: %+v", cs)
	}
}

// ---------------------------------------------------------------------------
// (3) DETERMINISM UNDER INPUT REORDERING
// ---------------------------------------------------------------------------

// Permuting the fingerprint slice must not change the findings: the matcher
// canonically orders its output, so input order is not observable. This is a
// stronger statement than the existing "same input twice" determinism test —
// it asserts ORDER-INVARIANCE, the property a hash-map iteration leak would
// break.
func TestMatchOrderInvariantUnderFingerprintPermutation(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	topo := edgeStore(t, evalAt.Add(-10*time.Second))

	// A scene rich enough that ordering could matter: two anchors (a throttled
	// container and a node with PSI), plus an entity-local leaker, plus inert
	// healthy bystanders interleaved.
	leak := leakingFP(evalAt)
	leak.CEIKey = "i|cl|shop|Pod|leaker|uid-leak"
	leak.Name = "leaker"
	h1 := observe.Fingerprint{CEIKey: "i|cl|shop|Pod|h1|uid-h1", Name: "h1", Kind: "Container", EvaluatedAt: evalAt}
	h2 := observe.Fingerprint{CEIKey: "i|cl|shop|Pod|h2|uid-h2", Name: "h2", Kind: "Pod", EvaluatedAt: evalAt}

	base := []observe.Fingerprint{throttledContainerFP(), nodePSIFP(observe.StateAbove), leak, h1, h2}
	want := m.Match(base, nil, topo, w)
	if len(want) == 0 {
		t.Fatal("setup: the scene must produce findings")
	}

	// Every rotation of the slice must yield identical findings.
	for shift := 1; shift < len(base); shift++ {
		perm := make([]observe.Fingerprint, 0, len(base))
		perm = append(perm, base[shift:]...)
		perm = append(perm, base[:shift]...)
		got := m.Match(perm, nil, topo, w)
		if !reflect.DeepEqual(want, got) {
			t.Errorf("findings changed under fingerprint rotation by %d:\nwant: %+v\ngot:  %+v", shift, want, got)
		}
	}

	// And the full reversal (the adversarial permutation).
	rev := make([]observe.Fingerprint, len(base))
	for i := range base {
		rev[len(base)-1-i] = base[i]
	}
	if got := m.Match(rev, nil, topo, w); !reflect.DeepEqual(want, got) {
		t.Errorf("findings changed under fingerprint reversal:\nwant: %+v\ngot:  %+v", want, got)
	}
}

// Reordering the TOPOLOGY assertions (the edges the snapshot was built in) must
// also not change findings: the validity verdict for an (edge, window) pair is
// a function of the assertions, not the order they arrived. A degraded/suspect
// path in particular must resolve the same regardless of assertion order.
func TestMatchInvariantUnderEdgeAssertionOrder(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	fps := []observe.Fingerprint{throttledContainerFP(), nodePSIFP(observe.StateAbove)}

	mk := func() *identity.EdgeStore {
		return identity.NewEdgeStore(func() time.Time { return evalAt },
			map[identity.EdgeType]time.Duration{identity.EdgeRunsOn: 90 * time.Second}, 24*time.Hour)
	}

	// Two assertions for the same edge: an older confirmation and a newer one.
	older := evalAt.Add(-time.Hour)
	newer := evalAt.Add(-10 * time.Second)

	sA := mk()
	sA.Assert(identity.EdgeRunsOn, mustCEI(t, podKey), mustCEI(t, nodeKey), older)
	sA.Assert(identity.EdgeRunsOn, mustCEI(t, podKey), mustCEI(t, nodeKey), newer)

	sB := mk()
	sB.Assert(identity.EdgeRunsOn, mustCEI(t, podKey), mustCEI(t, nodeKey), newer)
	sB.Assert(identity.EdgeRunsOn, mustCEI(t, podKey), mustCEI(t, nodeKey), older)

	a := m.Match(fps, nil, sA, w)
	b := m.Match(fps, nil, sB, w)
	if !reflect.DeepEqual(a, b) {
		t.Errorf("edge assertion order changed findings:\nA: %+v\nB: %+v", a, b)
	}
	// Non-vacuity: this scene actually produces a full cascade (so the edge was
	// load-bearing — a vacuous empty result would pass trivially).
	if findPhen(a, "PHEN_THROTTLING_CASCADE") == nil {
		t.Fatal("setup: the cascade must fire (else the order-invariance is vacuous)")
	}
}

// Cascade recognition is order-invariant within a tick: permuting the findings
// slice handed to Cascades yields the same canonically-sorted stories.
func TestCascadesOrderInvariantUnderFindingPermutation(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	topo := cascadeTopo(t)

	fp := leakingFP(evalAt)
	fp.Rates = oomKilledFP(evalAt).Rates
	findings := m.Match([]observe.Fingerprint{fp}, nil, topo, w)
	if len(findings) < 2 {
		t.Fatalf("setup: need both leak and OOM findings; got %+v", findings)
	}

	want := m.Cascades(evalAt, findings, NewCascadeTracker(10*time.Minute), topo, w)
	if findCascade(want, "PHEN_MEMORY_LEAK", "PHEN_OOM_KILL_CGROUP") == nil {
		t.Fatalf("setup: the cascade must be recognized; got %+v", want)
	}

	rev := make([]Finding, len(findings))
	for i := range findings {
		rev[len(findings)-1-i] = findings[i]
	}
	got := m.Cascades(evalAt, rev, NewCascadeTracker(10*time.Minute), topo, w)
	if !reflect.DeepEqual(want, got) {
		t.Errorf("cascade recognition changed under finding permutation:\nwant: %+v\ngot:  %+v", want, got)
	}
}

// Determinism backstop on the entity-local path: a scene shuffled into many
// orders always collapses to the same canonical finding sequence. Uses a stable
// fingerprint generator so the test itself is hermetic and fixed-time.
func TestEntityLocalCanonicalOrderStable(t *testing.T) {
	m := NewMatcher(loadGraph(t))

	// Five independent leakers; their findings must always come out sorted by
	// EntityCEI regardless of input order.
	var fps []observe.Fingerprint
	keys := []string{"web-e", "web-a", "web-c", "web-b", "web-d"}
	for _, name := range keys {
		fp := leakingFP(evalAt)
		fp.CEIKey = "i|cl|shop|Pod|" + name + "|uid-" + name
		fp.Name = name
		fps = append(fps, fp)
	}

	out := m.Match(fps, nil, nil, w)
	// Extract the order findings came out in.
	var gotKeys []string
	seen := map[string]bool{}
	for _, f := range out {
		if f.Phenomenon == "PHEN_MEMORY_LEAK" && !seen[f.EntityCEI] {
			seen[f.EntityCEI] = true
			gotKeys = append(gotKeys, f.EntityCEI)
		}
	}
	if len(gotKeys) != len(keys) {
		t.Fatalf("expected one leak per entity, got keys %v", gotKeys)
	}
	if !sort.SliceIsSorted(gotKeys, func(i, j int) bool { return gotKeys[i] < gotKeys[j] }) {
		t.Errorf("findings are not in canonical EntityCEI order: %v", gotKeys)
	}
}
