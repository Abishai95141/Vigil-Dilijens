package detect

import (
	"reflect"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/observe"
)

// Cascades + blast radius (doc 07 M5) golden tests on the REAL ontology: the
// authored MEMORY_LEAK -> OOM_KILL_CGROUP relation ("Eventual outcome",
// T0+terminal) recognized as one correlated story, windowed through the
// deterministic tracker; blast radius made concrete from selection
// participation + valid topology.

const (
	csPodKey  = "i|cl|shop|Pod|web-a|uid-a" // container findings ride the pod key (doc 04 compiler)
	csNodeKey = "i|cl||Node|worker-1|nodeuid-1"
)

// leakingFP: working set at-threshold AND rising — MEMORY_LEAK's signature.
func leakingFP(at time.Time) observe.Fingerprint {
	return observe.Fingerprint{
		CEIKey: csPodKey, Namespace: "shop", Name: "web-a", Kind: "Container", EvaluatedAt: at,
		Thresholds: []observe.VariableThreshold{{
			RuleID: "THR_CONTAINER_MEM_WORKING_SET_VS_LIMIT",
			Metric: "container_memory_working_set_bytes",
			State:  observe.StateAtThreshold, Slope: 1500, SlopeSamples: 6,
			BarSource: "config", Deriv: observe.DerivationRef{StreamID: "s1", SampleAt: at, How: "gauge-level"},
		}},
	}
}

// oomKilledFP: the cgroup OOM counter incremented inside the window.
func oomKilledFP(at time.Time) observe.Fingerprint {
	return observe.Fingerprint{
		CEIKey: csPodKey, Namespace: "shop", Name: "web-a", Kind: "Container", EvaluatedAt: at,
		Rates: []observe.VariableRate{{
			RuleID: "THR_CONTAINER_OOM_EVENTS", Metric: "container_oom_events_total",
			WindowDelta: 1, Bar: 1, Breached: true, BarSource: "default", Flagged: true,
			Deriv: observe.DerivationRef{StreamID: "s2", SampleAt: at, How: "rate-guard"},
		}},
	}
}

func cascadeTopo(t *testing.T) *identity.EdgeStore {
	t.Helper()
	s := identity.NewEdgeStore(func() time.Time { return evalAt },
		map[identity.EdgeType]time.Duration{identity.EdgeRunsOn: 90 * time.Second}, 24*time.Hour)
	s.Assert(identity.EdgeRunsOn, mustCEI(t, csPodKey), mustCEI(t, csNodeKey), evalAt.Add(-10*time.Second))
	return s
}

func findCascade(cs []Cascade, trigger, downstream string) *Cascade {
	for i := range cs {
		if cs[i].Trigger.Phenomenon == trigger && cs[i].Downstream.Phenomenon == downstream {
			return &cs[i]
		}
	}
	return nil
}

// The headline M5 case, windowed: MEMORY_LEAK fired two minutes ago (in the
// tracker); the OOM counter breaches NOW on the same entity — one correlated
// story, the authored relation verbatim, trigger reference pointing at the
// EARLIER occurrence.
func TestLeakToOOMCascadeAcrossTicks(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	topo := cascadeTopo(t)
	tracker := NewCascadeTracker(10 * time.Minute)

	// Tick 1 (two minutes ago): the leak fires; the tracker observes it.
	t1 := evalAt.Add(-2 * time.Minute)
	w1 := identity.TimeWindow{Start: t1.Add(-90 * time.Second), End: t1}
	leakFindings := m.Match([]observe.Fingerprint{leakingFP(t1)}, nil, topo, w1)
	if findPhen(leakFindings, "PHEN_MEMORY_LEAK") == nil {
		t.Fatal("setup: MEMORY_LEAK should fire on tick 1")
	}
	if got := m.Cascades(t1, leakFindings, tracker, topo, w1); len(got) != 0 {
		t.Fatalf("no downstream has fired yet — no story: %+v", got)
	}
	tracker.Observe(t1, leakFindings)

	// Tick 2 (now): the pod was OOM-killed; the leak (post-restart) is gone.
	oomFindings := m.Match([]observe.Fingerprint{oomKilledFP(evalAt)}, nil, topo, w)
	oom := findPhen(oomFindings, "PHEN_OOM_KILL_CGROUP")
	if oom == nil {
		t.Fatalf("OOM_KILL_CGROUP should fire on the breached counter; findings: %+v", oomFindings)
	}
	cs := m.Cascades(evalAt, oomFindings, tracker, topo, w)
	c := findCascade(cs, "PHEN_MEMORY_LEAK", "PHEN_OOM_KILL_CGROUP")
	if c == nil {
		t.Fatalf("the authored relation must be recognized as one story: %+v", cs)
	}
	if c.Trigger.EvaluatedAt != t1 || c.Trigger.EntityCEI != csPodKey {
		t.Errorf("trigger ref must point at the EARLIER leak occurrence: %+v", c.Trigger)
	}
	if c.Downstream.EvaluatedAt != evalAt {
		t.Errorf("downstream ref must point at the current OOM finding: %+v", c.Downstream)
	}
	if c.Why != "Eventual outcome" || c.Temporal != "T0+terminal" || c.Related != "same-entity" {
		t.Errorf("the AUTHORED relation must surface verbatim: %+v", c)
	}
}

// Outside the window, the old trigger is gone — no story is invented from a
// stale memory of it.
func TestCascadeWindowExpires(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	topo := cascadeTopo(t)
	tracker := NewCascadeTracker(10 * time.Minute)

	t1 := evalAt.Add(-20 * time.Minute)
	w1 := identity.TimeWindow{Start: t1.Add(-90 * time.Second), End: t1}
	tracker.Observe(t1, m.Match([]observe.Fingerprint{leakingFP(t1)}, nil, topo, w1))

	cs := m.Cascades(evalAt, m.Match([]observe.Fingerprint{oomKilledFP(evalAt)}, nil, topo, w), tracker, topo, w)
	if findCascade(cs, "PHEN_MEMORY_LEAK", "PHEN_OOM_KILL_CGROUP") != nil {
		t.Errorf("a 20-minute-old trigger is outside the 10m window: %+v", cs)
	}
}

// Same tick, same entity: trigger and downstream lighting together still pair.
func TestCascadeSameTick(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	topo := cascadeTopo(t)
	fp := leakingFP(evalAt)
	fp.Rates = oomKilledFP(evalAt).Rates // both signatures on one entity
	findings := m.Match([]observe.Fingerprint{fp}, nil, topo, w)
	cs := m.Cascades(evalAt, findings, NewCascadeTracker(10*time.Minute), topo, w)
	if findCascade(cs, "PHEN_MEMORY_LEAK", "PHEN_OOM_KILL_CGROUP") == nil {
		t.Errorf("same-tick trigger+downstream must pair: %+v", cs)
	}
}

// Topologically UNRELATED entities never pair, however suggestive the timing —
// a leak on one pod and an OOM on another (no shared edge) is two findings,
// not one story.
func TestCascadeRequiresTopologicalRelation(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	topo := cascadeTopo(t) // only web-a's pod has edges
	tracker := NewCascadeTracker(10 * time.Minute)

	other := leakingFP(evalAt.Add(-time.Minute))
	other.CEIKey = "i|cl|shop|Pod|web-b|uid-b" // a different, edge-less pod
	other.Name = "web-b"
	w1 := identity.TimeWindow{Start: evalAt.Add(-150 * time.Second), End: evalAt.Add(-time.Minute)}
	tracker.Observe(evalAt.Add(-time.Minute), m.Match([]observe.Fingerprint{other}, nil, topo, w1))

	cs := m.Cascades(evalAt, m.Match([]observe.Fingerprint{oomKilledFP(evalAt)}, nil, topo, w), tracker, topo, w)
	if findCascade(cs, "PHEN_MEMORY_LEAK", "PHEN_OOM_KILL_CGROUP") != nil {
		t.Errorf("unrelated entities must not pair into a story: %+v", cs)
	}
}

// The reversed authored shape: a relation with role "trigger" on Q declares
// that the TARGET triggers Q — so THROTTLING_CASCADE is a trigger of
// PROBE_CASCADE_META, alongside its directly-declared downstream.
func TestDownstreamResolutionBothShapes(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	refs := m.downstream["PHEN_THROTTLING_CASCADE"]
	got := map[string]bool{}
	for _, r := range refs {
		got[r.id] = true
	}
	if !got["PHEN_PROBE_FAILURE_RESTART"] {
		t.Errorf("direct downstream role missing: %v", refs)
	}
	if !got["PHEN_PROBE_CASCADE_META"] {
		t.Errorf("reversed trigger role missing (PROBE_CASCADE_META declares THROTTLING_CASCADE as its trigger): %v", refs)
	}
}

// Blast radius (doc 07 §3.4): the leak finding names ITSELF at risk of the
// authored downstream OOM — participation read from selection, relatedness
// from topology, the why verbatim. An unrelated participant stays out.
func TestBlastRadiusFromAuthoredRelation(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	topo := cascadeTopo(t)
	selected := map[string][]string{
		csPodKey: {"PHEN_MEMORY_LEAK", "PHEN_OOM_KILL_CGROUP"},
		// A far-away participant in the same downstream — no topological
		// relation to web-a's pod, so it must NOT appear in the radius.
		"i|cl|shop|Pod|far|uid-far": {"PHEN_OOM_KILL_CGROUP"},
	}
	findings := m.Match([]observe.Fingerprint{leakingFP(evalAt)}, selected, topo, w)
	leak := findPhen(findings, "PHEN_MEMORY_LEAK")
	if leak == nil {
		t.Fatal("MEMORY_LEAK should fire")
	}
	if len(leak.BlastRadius) != 1 {
		t.Fatalf("radius must hold exactly the related participant: %+v", leak.BlastRadius)
	}
	r := leak.BlastRadius[0]
	if r.CEIKey != csPodKey || r.Phenomenon != "PHEN_OOM_KILL_CGROUP" || r.Why != "Eventual outcome" || r.Related != "same-entity" {
		t.Errorf("radius entry wrong: %+v", r)
	}
}

// Without selection or topology no radius is invented (the walk needs both
// participation and valid edges — absence is absence).
func TestBlastRadiusNeedsSelectionAndTopology(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	findings := m.Match([]observe.Fingerprint{leakingFP(evalAt)}, nil, cascadeTopo(t), w)
	for _, f := range findings {
		if len(f.BlastRadius) != 0 {
			t.Errorf("no selection ⇒ no radius: %+v", f.BlastRadius)
		}
	}
}

// Determinism: same findings + same tracker state + same topology ⇒ identical
// cascades, twice.
func TestCascadesDeterministic(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	topo := cascadeTopo(t)
	fp := leakingFP(evalAt)
	fp.Rates = oomKilledFP(evalAt).Rates
	findings := m.Match([]observe.Fingerprint{fp}, nil, topo, w)
	tr1 := NewCascadeTracker(10 * time.Minute)
	tr2 := NewCascadeTracker(10 * time.Minute)
	a := m.Cascades(evalAt, findings, tr1, topo, w)
	b := m.Cascades(evalAt, findings, tr2, topo, w)
	if !reflect.DeepEqual(a, b) {
		t.Error("Cascades is not deterministic")
	}
}

// The degraded-surfacing floor (doc 07 §3.6, M4/M6): a degraded match below the
// calibrated completeness threshold does not surface; a FULL match is never
// subject to it; floor 0 surfaces every anchored degraded match.
func TestMinCompletenessPolicy(t *testing.T) {
	g := loadGraph(t)
	topo := cascadeTopo(t)

	// MEMORY_LEAK fires degraded at completeness 0.5 (1 of 2 required checked).
	at := func(floor float64) *Finding {
		m := NewMatcher(g)
		m.MinCompleteness = floor
		return findPhen(m.Match([]observe.Fingerprint{leakingFP(evalAt)}, nil, topo, w), "PHEN_MEMORY_LEAK")
	}
	if at(0) == nil {
		t.Fatal("floor 0 must surface the degraded leak")
	}
	if at(0.5) == nil {
		t.Error("completeness 0.5 meets a 0.5 floor (>=) and must surface")
	}
	if at(0.6) != nil {
		t.Error("completeness 0.5 under a 0.6 floor must NOT surface")
	}

	// A FULL match is never floor-gated: the throttling-cascade pair at 100%.
	m := NewMatcher(g)
	m.MinCompleteness = 0.99
	full := observe.Fingerprint{
		CEIKey: csPodKey, Namespace: "shop", Name: "web-a", Kind: "Container", EvaluatedAt: evalAt,
		Thresholds: []observe.VariableThreshold{{
			RuleID: "THR_CONTAINER_CPU_THROTTLE_RATIO", Metric: "container_cpu_cfs_throttled_periods_total",
			State: observe.StateAbove, BarSource: "default", Flagged: true,
			Deriv: observe.DerivationRef{StreamID: "s-r", SampleAt: evalAt, How: "counter-ratio"},
		}},
	}
	psi := observe.Fingerprint{
		CEIKey: csNodeKey, Name: "worker-1", Kind: "Node", EvaluatedAt: evalAt,
		Thresholds: []observe.VariableThreshold{{
			RuleID: "THR_NODE_CPU_PSI_STALL", Metric: "node_pressure_cpu_waiting_seconds_total",
			State: observe.StateAbove, BarSource: "default", Flagged: true,
			Deriv: observe.DerivationRef{StreamID: "s-p", SampleAt: evalAt, How: "counter-rate"},
		}},
	}
	f := findPhen(m.Match([]observe.Fingerprint{full, psi}, nil, topo, w), "PHEN_THROTTLING_CASCADE")
	if f == nil || f.Quality != QualityFull {
		t.Errorf("a FULL match must surface regardless of the floor: %+v", f)
	}
}

// M4: a checked-but-unobservable SUPPORTING member is a NAMED gap on the
// finding — never silently uncounted (quality stays keyed to required
// coverage, the stated policy).
func TestSupportingGapNamed(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	topo := cascadeTopo(t)
	// STORAGE_SATURATION's PVC member (supporting role) is checked but no PVC
	// fingerprint exists: the gap must be named on the finding.
	nodeFP := observe.Fingerprint{
		CEIKey: csNodeKey, Name: "worker-1", Kind: "Node", EvaluatedAt: evalAt,
		Thresholds: []observe.VariableThreshold{{
			RuleID: "THR_NODE_IO_PSI_STALL", Metric: "node_pressure_io_waiting_seconds_total",
			State: observe.StateAbove, BarSource: "default", Flagged: true,
			Deriv: observe.DerivationRef{StreamID: "s-io", SampleAt: evalAt, How: "counter-rate"},
		}},
	}
	f := findPhen(m.Match([]observe.Fingerprint{nodeFP}, nil, topo, w), "PHEN_STORAGE_SATURATION")
	if f == nil {
		t.Fatal("STORAGE_SATURATION should fire degraded at the node")
	}
	if f.SupportingUnobserved == 0 || len(f.SupportingGaps) != f.SupportingUnobserved {
		t.Errorf("the checked-but-unreachable PVC supporting member must be a NAMED gap: unobs=%d gaps=%v",
			f.SupportingUnobserved, f.SupportingGaps)
	}
	if f.Quality != QualityDegraded {
		t.Errorf("quality stays keyed to required coverage: %s", f.Quality)
	}
}

// --- relation honesty (pre-Phase-2 audit) ---------------------------------------

// A SUSPECT hop still relates two entities (the edge-validity contract makes
// suspect usable-but-NAMED, doc 07 §3.2) — so the story must carry the
// suspicion marker rather than passing as a clean relation.
func TestCascadeSuspectHopNamed(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	tracker := NewCascadeTracker(10 * time.Minute)

	// runs-on confirmed 10 minutes ago against a 90s budget: SUSPECT, not absent.
	topo := identity.NewEdgeStore(func() time.Time { return evalAt },
		map[identity.EdgeType]time.Duration{identity.EdgeRunsOn: 90 * time.Second}, 24*time.Hour)
	topo.Assert(identity.EdgeRunsOn, mustCEI(t, csPodKey), mustCEI(t, csNodeKey), evalAt.Add(-10*time.Minute))

	t1 := evalAt.Add(-2 * time.Minute)
	trigger := Finding{Phenomenon: "PHEN_MEMORY_LEAK", EntityCEI: csPodKey, EvaluatedAt: t1, Quality: QualityDegraded}
	tracker.Observe(t1, []Finding{trigger})
	down := Finding{Phenomenon: "PHEN_OOM_KILL_CGROUP", EntityCEI: csNodeKey, EvaluatedAt: evalAt, Quality: QualityDegraded}

	cs := m.Cascades(evalAt, []Finding{down}, tracker, topo, w)
	c := findCascade(cs, "PHEN_MEMORY_LEAK", "PHEN_OOM_KILL_CGROUP")
	if c == nil {
		t.Fatalf("a suspect hop still relates (usable-but-named): %+v", cs)
	}
	if c.Related != "runs-on (suspect)" {
		t.Errorf("the suspicion must be NAMED on the relation, got %q", c.Related)
	}
}

// Two containers of the same pod are related by IDENTITY (containment is not a
// hop): the relation holds even when the pod has no runs-on edge in the
// snapshot — an absent edge must not erase what the keys themselves prove.
func TestSamePodContainmentWithoutEdge(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	tracker := NewCascadeTracker(10 * time.Minute)
	empty := identity.NewEdgeStore(func() time.Time { return evalAt },
		map[identity.EdgeType]time.Duration{}, 24*time.Hour)

	ca := "i|cl|shop|Container|web-a/app|uid-a/app"
	cb := "i|cl|shop|Container|web-a/sidecar|uid-a/sidecar"
	t1 := evalAt.Add(-1 * time.Minute)
	tracker.Observe(t1, []Finding{{Phenomenon: "PHEN_MEMORY_LEAK", EntityCEI: ca, EvaluatedAt: t1, Quality: QualityFull}})
	down := Finding{Phenomenon: "PHEN_OOM_KILL_CGROUP", EntityCEI: cb, EvaluatedAt: evalAt, Quality: QualityDegraded}

	cs := m.Cascades(evalAt, []Finding{down}, tracker, empty, w)
	c := findCascade(cs, "PHEN_MEMORY_LEAK", "PHEN_OOM_KILL_CGROUP")
	if c == nil {
		t.Fatalf("same-pod containers must relate as same-entity without topology: %+v", cs)
	}
	if c.Related != "same-entity" {
		t.Errorf("containment is identity, got %q", c.Related)
	}
}
