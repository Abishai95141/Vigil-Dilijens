package selection

import (
	"reflect"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/binding"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
)

const (
	kgPath     = "../../../ontology/graph/k8s_signal_kg.json"
	overlayDir = "../../../ontology/graph/overlays"
)

var selAt = time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)

func loadGraph(t *testing.T) *graph.Graph {
	t.Helper()
	g, err := graph.LoadWithOverlays(kgPath, overlayDir)
	if err != nil {
		t.Fatalf("LoadWithOverlays: %v", err)
	}
	return g
}

func cei(ns, kind, name, uid string) identity.CEI {
	return identity.CEI{Layer: identity.LayerInstance, Cluster: "cl", Namespace: ns, Kind: kind, Name: name, UID: uid}
}

func boundBinding(ceiKey, ruleID, metric string) binding.Binding {
	return binding.Binding{
		CEIKey: ceiKey, Entity: "Container", Container: "c", RuleID: ruleID, Metric: metric,
		State: binding.StateBound, Validation: binding.ValidationSuspect,
		Bar: &binding.ResolvedBar{Kind: "config-relative", Source: binding.SourceConfig, Value: 1, Unit: "bytes", Direction: "above"},
	}
}

// The four funnel verdicts, end to end against the real ontology:
//   - a working-set-bound pod earns Tier A via PHEN_MEMORY_LEAK et al.
//   - a bound variable whose rule backs no phenomenon -> no-phenomenon-member
//   - an instantiated-but-unbounded entity -> no-evaluable-variable
//   - a Service (no instrumented variables) -> structural-entity
func TestFunnelVerdicts(t *testing.T) {
	g := loadGraph(t)
	podA := cei("shop", "Pod", "web-a", "uid-a")
	podB := cei("shop", "Pod", "web-b", "uid-b")
	podC := cei("shop", "Pod", "web-c", "uid-c")
	svc := cei("shop", "Service", "web", "uid-svc")

	res := &binding.Result{Bindings: []binding.Binding{
		boundBinding(podA.Key(), "THR_CONTAINER_MEM_WORKING_SET_VS_LIMIT", "container_memory_working_set_bytes"),
		// An evaluable variable riding a rule id the graph does not know: it can
		// earn no phenomenon participation (conservative, never invented).
		boundBinding(podB.Key(), "THR_NOT_IN_GRAPH", "some_metric"),
		// Instantiated but unbounded (no crossable limit) -> gate 1 fails.
		{CEIKey: podC.Key(), Entity: "Container", Container: "c", RuleID: "THR_CONTAINER_MEM_WORKING_SET_VS_LIMIT",
			Metric: "container_memory_working_set_bytes", State: binding.StateBound, Validation: binding.ValidationSuspect, Bar: nil},
	}}
	inv := []identity.InstanceRecord{
		{CEI: podA, Kind: "Pod", Namespace: "shop", Name: "web-a"},
		{CEI: podB, Kind: "Pod", Namespace: "shop", Name: "web-b"},
		{CEI: podC, Kind: "Pod", Namespace: "shop", Name: "web-c"},
		{CEI: svc, Kind: "Service", Namespace: "shop", Name: "web"},
	}

	r := Select(inv, res, g, false, selAt, "test", nil, identity.TimeWindow{})
	if len(r.Records) != 4 {
		t.Fatalf("every inventory entity gets a record: got %d, want 4", len(r.Records))
	}
	byName := map[string]Record{}
	for _, rec := range r.Records {
		byName[rec.Name] = rec
	}

	a := byName["web-a"]
	if a.Tier != TierA || a.Reason != ReasonPhenomenonMember || !a.PassesCoverage {
		t.Errorf("web-a: %+v, want Tier A / phenomenon-member", a)
	}
	if len(a.Phenomena) == 0 {
		t.Error("web-a must name the phenomena that earned its attention")
	}
	found := false
	for _, p := range a.Phenomena {
		if p == "PHEN_MEMORY_LEAK" {
			found = true
		}
	}
	if !found {
		t.Errorf("working-set member must participate in PHEN_MEMORY_LEAK: %v", a.Phenomena)
	}

	b := byName["web-b"]
	if b.Tier != TierNone || b.Reason != ReasonNoPhenomenonMember || !b.PassesCoverage {
		t.Errorf("web-b: %+v, want none / no-phenomenon-member (coverage passed)", b)
	}
	c := byName["web-c"]
	if c.Tier != TierNone || c.Reason != ReasonNoEvaluableVariable || c.PassesCoverage {
		t.Errorf("web-c: %+v, want none / no-evaluable-variable (coverage failed)", c)
	}
	s := byName["web"]
	if s.Tier != TierNone || s.Reason != ReasonStructuralEntity || s.PassesCoverage {
		t.Errorf("service: %+v, want none / structural-entity", s)
	}

	if r.TierACount != 1 || r.NoneByReason[ReasonNoPhenomenonMember] != 1 ||
		r.NoneByReason[ReasonNoEvaluableVariable] != 1 || r.NoneByReason[ReasonStructuralEntity] != 1 {
		t.Errorf("summary wrong: tierA=%d none=%v", r.TierACount, r.NoneByReason)
	}
	if r.GraphVersion != g.Version {
		t.Error("selection must pin the graph version it read participation from")
	}
}

// A QA-failed binding is NOT evaluable — it must not pass the coverage gate
// (its evidence contradicts its semantics, doc 04 M3).
func TestValidationFailedExcluded(t *testing.T) {
	g := loadGraph(t)
	pod := cei("shop", "Pod", "web-a", "uid-a")
	bb := boundBinding(pod.Key(), "THR_CONTAINER_MEM_WORKING_SET_VS_LIMIT", "container_memory_working_set_bytes")
	bb.Validation = binding.ValidationFailed
	res := &binding.Result{Bindings: []binding.Binding{bb}}

	set := TierASet(res, g)
	if len(set) != 0 {
		t.Errorf("a QA-failed binding must not earn Tier A: %v", set)
	}
	r := Select([]identity.InstanceRecord{{CEI: pod, Kind: "Pod", Namespace: "shop", Name: "web-a"}}, res, g, false, selAt, "test", nil, identity.TimeWindow{})
	if r.Records[0].Reason != ReasonNoEvaluableVariable {
		t.Errorf("reason = %s, want no-evaluable-variable", r.Records[0].Reason)
	}
}

// Determinism (doc 06 records feed replay-visible detection): same inputs =>
// identical results, including phenomena order inside a record.
func TestSelectDeterministic(t *testing.T) {
	g := loadGraph(t)
	pod := cei("shop", "Pod", "web-a", "uid-a")
	res := &binding.Result{Bindings: []binding.Binding{
		boundBinding(pod.Key(), "THR_CONTAINER_MEM_WORKING_SET_VS_LIMIT", "container_memory_working_set_bytes"),
		boundBinding(pod.Key(), "THR_CONTAINER_CPU_THROTTLE_RATIO", "container_cpu_cfs_throttled_periods_total"),
	}}
	inv := []identity.InstanceRecord{{CEI: pod, Kind: "Pod", Namespace: "shop", Name: "web-a"}}
	a := Select(inv, res, g, false, selAt, "test", nil, identity.TimeWindow{})
	b := Select(inv, res, g, false, selAt, "test", nil, identity.TimeWindow{})
	if !reflect.DeepEqual(a, b) {
		t.Error("Select is not deterministic")
	}
	s1 := TierASet(res, g)
	s2 := TierASet(res, g)
	if !reflect.DeepEqual(s1, s2) {
		t.Error("TierASet is not deterministic")
	}
}

// Nil inputs (binding disabled / graph absent) yield an honest all-none result,
// never a panic and never an invented selection.
func TestSelectWithoutBindingOrGraph(t *testing.T) {
	pod := cei("shop", "Pod", "web-a", "uid-a")
	inv := []identity.InstanceRecord{{CEI: pod, Kind: "Pod", Namespace: "shop", Name: "web-a"}}
	r := Select(inv, nil, nil, false, selAt, "test", nil, identity.TimeWindow{})
	if r.TierACount != 0 || r.Records[0].Tier != TierNone {
		t.Errorf("no bindings -> nothing selected: %+v", r.Records[0])
	}
}

// Stale bound graph + fresh inventory (adversarial finding): an entity the
// bindings have never seen gets the truthful bindings-stale reason, never a
// false structural-entity claim about its kind.
func TestStaleBindingsReason(t *testing.T) {
	g := loadGraph(t)
	podA := cei("shop", "Pod", "web-a", "uid-a")
	podNew := cei("shop", "Pod", "web-new", "uid-new") // created after the last successful compile
	res := &binding.Result{Bindings: []binding.Binding{
		boundBinding(podA.Key(), "THR_CONTAINER_MEM_WORKING_SET_VS_LIMIT", "container_memory_working_set_bytes"),
	}}
	inv := []identity.InstanceRecord{
		{CEI: podA, Kind: "Pod", Namespace: "shop", Name: "web-a"},
		{CEI: podNew, Kind: "Pod", Namespace: "shop", Name: "web-new"},
	}
	r := Select(inv, res, g, true, selAt, "test", nil, identity.TimeWindow{})
	for _, rec := range r.Records {
		switch rec.Name {
		case "web-a":
			if rec.Tier != TierA {
				t.Errorf("web-a present in the stale bindings keeps its truthful verdict: %+v", rec)
			}
		case "web-new":
			if rec.Reason != ReasonBindingsStale {
				t.Errorf("binding-unseen entity under stale bindings = bindings-stale, got %s", rec.Reason)
			}
		}
	}
	// Without staleness the same absence means a structural kind.
	r2 := Select(inv, res, g, false, selAt, "test", nil, identity.TimeWindow{})
	for _, rec := range r2.Records {
		if rec.Name == "web-new" && rec.Reason != ReasonStructuralEntity {
			t.Errorf("fresh bindings + no rows = structural-entity, got %s", rec.Reason)
		}
	}
}

// The funnel provably covers everything the matcher can fire on: an entity
// whose bound METRIC backs an authored phenomenon check participates even when
// its binding rides a rule whose signal is not a member of that phenomenon.
func TestMetricJoinCoversMatcher(t *testing.T) {
	g := loadGraph(t)
	pod := cei("shop", "Pod", "web-a", "uid-a")
	// A rule id the graph does not know (no signal join possible), but the
	// metric is consulted by PHEN_MEMORY_LEAK's authored check.
	res := &binding.Result{Bindings: []binding.Binding{
		boundBinding(pod.Key(), "THR_SOME_FOREIGN_RULE", "container_memory_working_set_bytes"),
	}}
	set := TierASet(res, g)
	phens := set[pod.Key()]
	found := false
	for _, p := range phens {
		if p == "PHEN_MEMORY_LEAK" {
			found = true
		}
	}
	if !found {
		t.Errorf("check-metric join must earn participation: %v", phens)
	}
}

// Neighbourhood closure (doc 06 M2): a Tier-A container participating in the
// first-order THROTTLING_CASCADE pulls in its pod's node along runs-on (with
// the edge verdict); with no traversable edge the absence is a RECORDED gap,
// never a silent drop.
func TestNeighbourhoodClosure(t *testing.T) {
	g := loadGraph(t)
	containerCEI := identity.CEI{Layer: identity.LayerInstance, Cluster: "cl", Namespace: "shop",
		Kind: "Container", Name: "app", UID: "uid-pod/app"}
	podCEI := cei("shop", "Pod", "app-7d9", "uid-pod")
	nodeCEI := identity.CEI{Layer: identity.LayerInstance, Cluster: "cl", Kind: "Node", Name: "w1", UID: "uid-node"}

	res := &binding.Result{Bindings: []binding.Binding{
		boundBinding(containerCEI.Key(), "THR_CONTAINER_CPU_THROTTLE_RATIO", "container_cpu_cfs_throttled_periods_total"),
	}}
	inv := []identity.InstanceRecord{{CEI: containerCEI, Kind: "Container", Namespace: "shop", Name: "app"}}
	w := identity.TimeWindow{Start: selAt.Add(-90 * time.Second), End: selAt}

	// With a fresh pod→node edge: the node enters the watch set, hop 1, valid.
	edges := identity.NewEdgeStore(func() time.Time { return selAt },
		map[identity.EdgeType]time.Duration{identity.EdgeRunsOn: 90 * time.Second}, time.Hour)
	edges.Assert(identity.EdgeRunsOn, podCEI, nodeCEI, selAt.Add(-10*time.Second))
	r := Select(inv, res, g, false, selAt, "test", edges, w)
	rec := r.Records[0]
	if rec.Tier != TierA {
		t.Fatalf("container must be Tier A: %+v", rec)
	}
	var nodeRef *NeighbourRef
	for i := range rec.Neighbourhood {
		if rec.Neighbourhood[i].CEIKey == nodeCEI.Key() {
			nodeRef = &rec.Neighbourhood[i]
		}
	}
	if nodeRef == nil || nodeRef.Via != "runs-on" || nodeRef.Hop != 1 || nodeRef.Result != "valid" {
		t.Fatalf("closure must pull the node in via runs-on hop 1 valid: %+v", rec.Neighbourhood)
	}
	found := false
	for _, p := range nodeRef.ForPhenomena {
		if p == "PHEN_THROTTLING_CASCADE" {
			found = true
		}
	}
	if !found {
		t.Errorf("the ref must name the phenomena that need it: %v", nodeRef.ForPhenomena)
	}
	for _, gap := range r.ClosureGaps {
		if gap.Phenomenon == "PHEN_THROTTLING_CASCADE" {
			t.Errorf("reachable neighbourhood must not be a gap: %+v", gap)
		}
	}

	// Without any topology for the pod: the same phenomenon is a RECORDED gap.
	empty := identity.NewEdgeStore(func() time.Time { return selAt },
		map[identity.EdgeType]time.Duration{identity.EdgeRunsOn: 90 * time.Second}, time.Hour)
	r2 := Select(inv, res, g, false, selAt, "test", empty, w)
	foundGap := false
	for _, gap := range r2.ClosureGaps {
		if gap.EntityCEI == containerCEI.Key() && gap.Phenomenon == "PHEN_THROTTLING_CASCADE" {
			foundGap = true
		}
	}
	if !foundGap {
		t.Errorf("unreachable neighbourhood must be a recorded gap: %+v", r2.ClosureGaps)
	}
}
