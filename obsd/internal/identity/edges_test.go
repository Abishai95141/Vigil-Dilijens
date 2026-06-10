package identity

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

var testBudgets = map[EdgeType]time.Duration{
	EdgeRunsOn:    90 * time.Second,
	EdgeSelects:   90 * time.Second,
	EdgeMounts:    120 * time.Second,
	EdgeOwns:      600 * time.Second,
	EdgeNodeLease: 40 * time.Second,
}

func newEdgeStore(clk *fakeClock) *EdgeStore {
	return NewEdgeStore(clk.Now, testBudgets, 30*time.Minute)
}

func podCEI(name, uid string) CEI {
	c, _ := MintInstance(InstanceCoords{Cluster: cluster, Namespace: "shop", Kind: "Pod", Name: name, UID: uid}, lcBase)
	return c
}

func nodeCEI(name, uid string) CEI {
	c, _ := MintInstance(InstanceCoords{Cluster: cluster, Kind: "Node", Name: name, UID: uid}, lcBase)
	return c
}

func win(start, end time.Time) TimeWindow { return TimeWindow{Start: start, End: end} }

func TestEdgeFreshTraversalIsValid(t *testing.T) {
	clk := newFakeClock(lcBase)
	es := newEdgeStore(clk)
	pod, node := podCEI("a", "ua"), nodeCEI("x", "ux")
	es.Assert(EdgeRunsOn, pod, node, lcBase)

	r := es.Traverse(EdgeRunsOn, pod.Key(), node.Key(), win(lcBase, lcBase.Add(30*time.Second)))
	if r != TraversalValid {
		t.Errorf("fresh edge traversal = %s, want valid", r)
	}
}

func TestEdgeStaleTraversalIsSuspect(t *testing.T) {
	clk := newFakeClock(lcBase)
	es := newEdgeStore(clk)
	pod, node := podCEI("a", "ua"), nodeCEI("x", "ux")
	es.Assert(EdgeRunsOn, pod, node, lcBase) // confirmed at lcBase, never again

	// Window ending 3m later: 3m since last confirmation > 90s budget -> suspect.
	r := es.Traverse(EdgeRunsOn, pod.Key(), node.Key(), win(lcBase.Add(2*time.Minute), lcBase.Add(3*time.Minute)))
	if r != TraversalSuspect {
		t.Errorf("stale edge traversal = %s, want suspect", r)
	}
	// Re-confirming within budget of the window end restores validity.
	es.Assert(EdgeRunsOn, pod, node, lcBase.Add(2*time.Minute+50*time.Second))
	r = es.Traverse(EdgeRunsOn, pod.Key(), node.Key(), win(lcBase.Add(2*time.Minute), lcBase.Add(3*time.Minute)))
	if r != TraversalValid {
		t.Errorf("re-confirmed edge traversal = %s, want valid", r)
	}
}

func TestEdgeAbsentWhenMissing(t *testing.T) {
	clk := newFakeClock(lcBase)
	es := newEdgeStore(clk)
	if r := es.Traverse(EdgeRunsOn, "nope", "nope", win(lcBase, lcBase.Add(time.Minute))); r != TraversalAbsent {
		t.Errorf("missing edge = %s, want absent", r)
	}
}

func TestEdgeAssertedAfterWindowIsAbsent(t *testing.T) {
	clk := newFakeClock(lcBase)
	es := newEdgeStore(clk)
	pod, node := podCEI("a", "ua"), nodeCEI("x", "ux")
	es.Assert(EdgeRunsOn, pod, node, lcBase.Add(10*time.Minute))
	// Window entirely before the edge existed.
	if r := es.Traverse(EdgeRunsOn, pod.Key(), node.Key(), win(lcBase, lcBase.Add(5*time.Minute))); r != TraversalAbsent {
		t.Errorf("edge asserted after window = %s, want absent", r)
	}
}

// THE TRUST-CRITICAL TEST (doc 07 §3.2): an edge that existed in the PAST but was
// retracted before the current window must NOT support a co-occurrence in that
// window — the rule that stops detection fabricating a 2-hop correlation through a
// stale edge. But the same edge IS valid for a window during its actual tenure.
func TestEdgeRetractedBeforeWindowCannotFabricate(t *testing.T) {
	clk := newFakeClock(lcBase)
	es := newEdgeStore(clk)
	podA, nodeX := podCEI("a", "ua"), nodeCEI("x", "ux")

	es.Assert(EdgeRunsOn, podA, nodeX, lcBase)                               // A ran on X from lcBase
	es.Retract(EdgeRunsOn, podA.Key(), nodeX.Key(), lcBase.Add(time.Minute)) // A rescheduled away at +1m

	// A LATER window (after A left X): traversing A->X must be absent. A is no longer
	// on X, so any "co-occurrence" of A and X's pods across this edge is fabricated.
	later := win(lcBase.Add(5*time.Minute), lcBase.Add(6*time.Minute))
	if r := es.Traverse(EdgeRunsOn, podA.Key(), nodeX.Key(), later); r != TraversalAbsent {
		t.Fatalf("FABRICATION RISK: stale retracted edge traversed %s for a later window, want absent", r)
	}

	// A window DURING A's actual tenure: the relationship was real -> valid.
	during := win(lcBase, lcBase.Add(30*time.Second))
	if r := es.Traverse(EdgeRunsOn, podA.Key(), nodeX.Key(), during); r != TraversalValid {
		t.Errorf("edge during its real tenure = %s, want valid", r)
	}
}

// A retracted edge that overlaps the window (retracted mid- or post-window) provably
// existed during the overlap -> valid (we have ground truth of the interval).
func TestEdgeRetractedDuringWindowIsValid(t *testing.T) {
	clk := newFakeClock(lcBase)
	es := newEdgeStore(clk)
	pod, node := podCEI("a", "ua"), nodeCEI("x", "ux")
	es.Assert(EdgeRunsOn, pod, node, lcBase)
	es.Retract(EdgeRunsOn, pod.Key(), node.Key(), lcBase.Add(5*time.Minute))
	// Window [+2m, +4m] is inside the edge's [lcBase, +5m) existence.
	if r := es.Traverse(EdgeRunsOn, pod.Key(), node.Key(), win(lcBase.Add(2*time.Minute), lcBase.Add(4*time.Minute))); r != TraversalValid {
		t.Errorf("edge retracted after window = %s, want valid", r)
	}
}

// The retracted horizon is a memory-reclamation boundary owned by GC, NOT a
// traversal input: Traverse answers purely from (state, window). A still-resident
// retracted edge answers from its recorded interval; once GC reclaims it past the
// horizon it is absent.
func TestEdgeRetractedHorizonIsGCBoundaryNotTraversalInput(t *testing.T) {
	clk := newFakeClock(lcBase)
	es := newEdgeStore(clk)
	pod, node := podCEI("a", "ua"), nodeCEI("x", "ux")
	es.Assert(EdgeRunsOn, pod, node, lcBase)
	es.Retract(EdgeRunsOn, pod.Key(), node.Key(), lcBase.Add(time.Minute))
	clk.Advance(31 * time.Minute) // wall clock past the 30m horizon

	during := win(lcBase, lcBase.Add(30*time.Second)) // a window in the edge's real tenure
	// Before GC: still resident, so it answers from its interval — deterministically,
	// independent of wall-clock (the determinism fix).
	if r := es.Traverse(EdgeRunsOn, pod.Key(), node.Key(), during); r != TraversalValid {
		t.Errorf("pre-GC in-tenure window = %s, want valid", r)
	}
	es.GC() // GC reclaims past-horizon retracted edges
	if r := es.Traverse(EdgeRunsOn, pod.Key(), node.Key(), during); r != TraversalAbsent {
		t.Errorf("post-GC window = %s, want absent", r)
	}
}

// Re-assert must be monotonic: a stale event older than the retraction must not
// resurrect a retracted edge or move confirmation backwards (doc 14 §1.1 — an
// out-of-order resync re-delivering an old lease RenewTime).
func TestReassertIgnoresStaleEvent(t *testing.T) {
	clk := newFakeClock(lcBase)
	es := newEdgeStore(clk)
	node := nodeCEI("x", "ux")
	es.Assert(EdgeNodeLease, node, node, lcBase.Add(10*time.Second))              // renewed at +10s
	es.Retract(EdgeNodeLease, node.Key(), node.Key(), lcBase.Add(60*time.Second)) // node died, retracted at +60s

	// A stale resync re-delivers the OLD renew time (+5s) — must NOT resurrect.
	es.Assert(EdgeNodeLease, node, node, lcBase.Add(5*time.Second))
	if s := es.NodeLiveness(node.Key(), lcBase.Add(20*time.Second)); s != StatusRetracted {
		t.Errorf("stale re-add resurrected a dead node: status=%s, want retracted", s)
	}
	if es.Metrics().Reasserted != 0 {
		t.Errorf("stale event should not count as a reassert, got %d", es.Metrics().Reasserted)
	}

	// A genuine fresh renew (after the retraction) DOES reassert.
	es.Assert(EdgeNodeLease, node, node, lcBase.Add(90*time.Second))
	if s := es.NodeLiveness(node.Key(), lcBase.Add(100*time.Second)); s != StatusValid {
		t.Errorf("fresh renew did not reassert: status=%s, want valid", s)
	}
}

func TestEdgeReassertAfterRetract(t *testing.T) {
	clk := newFakeClock(lcBase)
	es := newEdgeStore(clk)
	pod, node := podCEI("a", "ua"), nodeCEI("x", "ux")
	es.Assert(EdgeRunsOn, pod, node, lcBase)
	es.Retract(EdgeRunsOn, pod.Key(), node.Key(), lcBase.Add(time.Minute))
	es.Assert(EdgeRunsOn, pod, node, lcBase.Add(2*time.Minute)) // came back

	r := es.Traverse(EdgeRunsOn, pod.Key(), node.Key(), win(lcBase.Add(2*time.Minute), lcBase.Add(2*time.Minute+30*time.Second)))
	if r != TraversalValid {
		t.Errorf("re-asserted edge = %s, want valid", r)
	}
	if es.Metrics().Reasserted != 1 {
		t.Errorf("Reasserted = %d, want 1", es.Metrics().Reasserted)
	}
}

// ReconcileOut models a reschedule: a pod runs on exactly one node, so asserting the
// new node retracts the old runs-on edge.
func TestReconcileOutReschedule(t *testing.T) {
	clk := newFakeClock(lcBase)
	es := newEdgeStore(clk)
	pod := podCEI("a", "ua")
	nodeX, nodeY := nodeCEI("x", "ux"), nodeCEI("y", "uy")

	es.Assert(EdgeRunsOn, pod, nodeX, lcBase)
	es.ReconcileOut(EdgeRunsOn, pod, []CEI{nodeY}, lcBase.Add(time.Minute)) // rescheduled to Y

	after := win(lcBase.Add(2*time.Minute), lcBase.Add(3*time.Minute))
	if r := es.Traverse(EdgeRunsOn, pod.Key(), nodeX.Key(), after); r != TraversalAbsent {
		t.Errorf("old node edge after reschedule = %s, want absent", r)
	}
	if r := es.Traverse(EdgeRunsOn, pod.Key(), nodeY.Key(), win(lcBase.Add(time.Minute), lcBase.Add(90*time.Second))); r != TraversalValid {
		t.Errorf("new node edge = %s, want valid", r)
	}
}

func TestReconcileOutEmptyRetractsAll(t *testing.T) {
	clk := newFakeClock(lcBase)
	es := newEdgeStore(clk)
	pod, node := podCEI("a", "ua"), nodeCEI("x", "ux")
	es.Assert(EdgeRunsOn, pod, node, lcBase)
	es.ReconcileOut(EdgeRunsOn, pod, nil, lcBase.Add(time.Minute)) // pod unscheduled
	if r := es.Traverse(EdgeRunsOn, pod.Key(), node.Key(), win(lcBase.Add(2*time.Minute), lcBase.Add(3*time.Minute))); r != TraversalAbsent {
		t.Errorf("edge after empty reconcile = %s, want absent", r)
	}
}

func TestRetractAllFromAndTo(t *testing.T) {
	clk := newFakeClock(lcBase)
	es := newEdgeStore(clk)
	pod := podCEI("a", "ua")
	node := nodeCEI("x", "ux")
	pvc, _ := MintInstance(InstanceCoords{Cluster: cluster, Namespace: "shop", Kind: "PersistentVolumeClaim", Name: "data", UID: "upvc"}, lcBase)
	es.Assert(EdgeRunsOn, pod, node, lcBase)
	es.Assert(EdgeMounts, pod, pvc, lcBase)

	es.RetractAllFrom(pod.Key(), lcBase.Add(time.Minute)) // pod deleted
	after := win(lcBase.Add(2*time.Minute), lcBase.Add(3*time.Minute))
	if es.Traverse(EdgeRunsOn, pod.Key(), node.Key(), after) != TraversalAbsent {
		t.Error("runs-on should be retracted after RetractAllFrom")
	}
	if es.Traverse(EdgeMounts, pod.Key(), pvc.Key(), after) != TraversalAbsent {
		t.Error("mounts should be retracted after RetractAllFrom")
	}

	// RetractAllTo: a fresh edge into a node, then node deleted.
	podB := podCEI("b", "ub")
	es.Assert(EdgeRunsOn, podB, node, lcBase.Add(2*time.Minute))
	es.RetractAllTo(node.Key(), lcBase.Add(3*time.Minute))
	if es.Traverse(EdgeRunsOn, podB.Key(), node.Key(), win(lcBase.Add(4*time.Minute), lcBase.Add(5*time.Minute))) != TraversalAbsent {
		t.Error("edge into node should be retracted after RetractAllTo")
	}
}

func TestNeighboursHonourValidity(t *testing.T) {
	clk := newFakeClock(lcBase)
	es := newEdgeStore(clk)
	svc, _ := MintInstance(InstanceCoords{Cluster: cluster, Namespace: "shop", Kind: "Service", Name: "web", UID: "usvc"}, lcBase)
	p1, p2, p3 := podCEI("p1", "u1"), podCEI("p2", "u2"), podCEI("p3", "u3")
	es.Assert(EdgeSelects, svc, p1, lcBase)                                  // fresh
	es.Assert(EdgeSelects, svc, p2, lcBase)                                  // will be stale
	es.Assert(EdgeSelects, svc, p3, lcBase)                                  //
	es.Retract(EdgeSelects, svc.Key(), p3.Key(), lcBase.Add(30*time.Second)) // p3 left

	// Window where p1/p2 are present; re-confirm p1 to keep it fresh.
	es.Assert(EdgeSelects, svc, p1, lcBase.Add(2*time.Minute+30*time.Second))
	w := win(lcBase.Add(2*time.Minute), lcBase.Add(3*time.Minute))
	ns := es.Neighbours(EdgeSelects, svc.Key(), w)

	got := map[string]Traversal{}
	for _, n := range ns {
		got[n.To.Name] = n.Result
	}
	if got["p1"] != TraversalValid {
		t.Errorf("p1 = %s, want valid", got["p1"])
	}
	if got["p2"] != TraversalSuspect {
		t.Errorf("p2 = %s, want suspect (stale)", got["p2"])
	}
	if _, present := got["p3"]; present {
		t.Errorf("p3 retracted before window should be absent, got %s", got["p3"])
	}
}

func TestNodeLiveness(t *testing.T) {
	clk := newFakeClock(lcBase)
	es := newEdgeStore(clk)
	node := nodeCEI("x", "ux")
	es.Assert(EdgeNodeLease, node, node, lcBase) // lease renewed

	if s := es.NodeLiveness(node.Key(), lcBase.Add(30*time.Second)); s != StatusValid {
		t.Errorf("node liveness at 30s = %s, want valid", s) // 30s < 40s budget
	}
	if s := es.NodeLiveness(node.Key(), lcBase.Add(50*time.Second)); s != StatusSuspect {
		t.Errorf("node liveness at 50s = %s, want suspect", s) // 50s > 40s budget
	}
}

func TestEdgeGCDropsPastHorizon(t *testing.T) {
	clk := newFakeClock(lcBase)
	es := newEdgeStore(clk)
	pod, node := podCEI("a", "ua"), nodeCEI("x", "ux")
	es.Assert(EdgeRunsOn, pod, node, lcBase)
	es.Retract(EdgeRunsOn, pod.Key(), node.Key(), lcBase)
	clk.Advance(31 * time.Minute)
	es.GC()
	if m := es.Metrics(); m.Live+m.Suspect+m.Retracted != 0 {
		t.Errorf("GC did not reclaim past-horizon edge: %+v", m)
	}
}

func TestEdgeMetricsPerType(t *testing.T) {
	clk := newFakeClock(lcBase)
	es := newEdgeStore(clk)
	pod, node := podCEI("a", "ua"), nodeCEI("x", "ux")
	svc, _ := MintInstance(InstanceCoords{Cluster: cluster, Namespace: "shop", Kind: "Service", Name: "web", UID: "usvc"}, lcBase)
	es.Assert(EdgeRunsOn, pod, node, lcBase)
	es.Assert(EdgeSelects, svc, pod, lcBase)
	clk.Advance(2 * time.Minute) // runs-on (90s) and selects (90s) now both stale

	m := es.Metrics()
	if m.PerType[EdgeRunsOn].Suspect != 1 || m.PerType[EdgeSelects].Suspect != 1 {
		t.Errorf("per-type suspect counts wrong: %+v", m.PerType)
	}
	if m.Suspect != 2 {
		t.Errorf("total suspect = %d, want 2", m.Suspect)
	}
}

func TestEdgeStoreConcurrent(t *testing.T) {
	clk := newFakeClock(lcBase)
	es := newEdgeStore(clk)
	node := nodeCEI("x", "ux")
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pod := podCEI(fmt.Sprintf("p%d", i), fmt.Sprintf("u%d", i))
			es.Assert(EdgeRunsOn, pod, node, lcBase)
			es.Traverse(EdgeRunsOn, pod.Key(), node.Key(), win(lcBase, lcBase.Add(30*time.Second)))
			es.Neighbours(EdgeRunsOn, pod.Key(), win(lcBase, lcBase.Add(30*time.Second)))
			es.Metrics()
		}(i)
	}
	wg.Wait()
	if m := es.Metrics(); m.Live != 50 {
		t.Errorf("Live = %d, want 50", m.Live)
	}
}
