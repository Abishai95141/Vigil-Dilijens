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
