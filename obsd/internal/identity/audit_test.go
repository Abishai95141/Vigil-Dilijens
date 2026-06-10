package identity

import (
	"testing"
	"time"
)

// fakeTruth is an in-memory control-plane truth for audit tests.
type fakeTruth struct {
	pods  map[string]string // "ns/name" -> uid
	nodes map[string]string // name -> uid
}

func newFakeTruth() *fakeTruth {
	return &fakeTruth{pods: map[string]string{}, nodes: map[string]string{}}
}

func (f *fakeTruth) PodUID(ns, name string) (string, bool) {
	u, ok := f.pods[ns+"/"+name]
	return u, ok
}
func (f *fakeTruth) NodeUID(name string) (string, bool) { u, ok := f.nodes[name]; return u, ok }

func (f *fakeTruth) PodExistsByUID(uid string) bool {
	for _, u := range f.pods {
		if u == uid {
			return true
		}
	}
	return false
}

func (f *fakeTruth) ListPods() []EntityRef {
	out := make([]EntityRef, 0, len(f.pods))
	for k, uid := range f.pods {
		ns, name, _ := splitNS(k)
		out = append(out, EntityRef{Namespace: ns, Name: name, UID: uid})
	}
	return out
}

func (f *fakeTruth) ListNodes() []EntityRef {
	out := make([]EntityRef, 0, len(f.nodes))
	for name, uid := range f.nodes {
		out = append(out, EntityRef{Name: name, UID: uid})
	}
	return out
}

func splitNS(k string) (string, string, bool) {
	for i := 0; i < len(k); i++ {
		if k[i] == '/' {
			return k[:i], k[i+1:], true
		}
	}
	return "", k, false
}

func podInstanceCEIFor(ns, name, uid string) CEI {
	c, _ := MintInstance(InstanceCoords{Cluster: cluster, Namespace: ns, Kind: "Pod", Name: name, UID: uid}, lcBase)
	return c
}

func TestVerifyJoinPod(t *testing.T) {
	truth := newFakeTruth()
	truth.pods["shop/web-x"] = "uid-1"

	if r := VerifyJoin(podInstanceCEIFor("shop", "web-x", "uid-1"), truth); r != JoinCorrect {
		t.Errorf("matching pod = %s, want correct", r)
	}
	if r := VerifyJoin(podInstanceCEIFor("shop", "web-x", "uid-WRONG"), truth); r != JoinMisjoin {
		t.Errorf("wrong-uid pod = %s, want misjoin", r)
	}
	if r := VerifyJoin(podInstanceCEIFor("shop", "ghost", "uid-9"), truth); r != JoinVanished {
		t.Errorf("absent pod = %s, want vanished", r)
	}
}

func TestVerifyJoinNodeAndContainer(t *testing.T) {
	truth := newFakeTruth()
	truth.nodes["worker-1"] = "node-1"
	truth.pods["shop/db-0"] = "pod-uid-db"

	node, _ := MintInstance(InstanceCoords{Cluster: cluster, Kind: "Node", Name: "worker-1", UID: "node-1"}, lcBase)
	if r := VerifyJoin(node, truth); r != JoinCorrect {
		t.Errorf("node = %s, want correct", r)
	}
	badNode, _ := MintInstance(InstanceCoords{Cluster: cluster, Kind: "Node", Name: "worker-1", UID: "node-WRONG"}, lcBase)
	if r := VerifyJoin(badNode, truth); r != JoinMisjoin {
		t.Errorf("wrong node = %s, want misjoin", r)
	}

	container, _ := MintInstance(InstanceCoords{Cluster: cluster, Namespace: "shop", Kind: "Container", Name: "server", UID: "pod-uid-db/server"}, lcBase)
	if r := VerifyJoin(container, truth); r != JoinCorrect {
		t.Errorf("container of live pod = %s, want correct", r)
	}
	gone, _ := MintInstance(InstanceCoords{Cluster: cluster, Namespace: "shop", Kind: "Container", Name: "server", UID: "pod-uid-GONE/server"}, lcBase)
	if r := VerifyJoin(gone, truth); r != JoinVanished {
		t.Errorf("container of dead pod = %s, want vanished", r)
	}
}

func TestVerifyJoinRoleUnverifiable(t *testing.T) {
	role, _ := MintRole(RoleCoords{Cluster: cluster, Namespace: "shop", Kind: "Deployment", RoleKey: "Deployment/web"}, lcBase)
	if r := VerifyJoin(role, newFakeTruth()); r != JoinUnverifiable {
		t.Errorf("role = %s, want unverifiable", r)
	}
}

func TestAuditConsistencyAllCorrect(t *testing.T) {
	clk := newFakeClock(lcBase)
	st := newTestStore(clk)
	st.Observe(podCoords("shop", "web-x", "uid-1"), roleFor("shop", "Deployment", "web"), lcBase, StateActive)
	st.Observe(InstanceCoords{Cluster: cluster, Kind: "Node", Name: "worker-1", UID: "node-1"}, CEI{}, lcBase, StateActive)

	truth := newFakeTruth()
	truth.pods["shop/web-x"] = "uid-1"
	truth.nodes["worker-1"] = "node-1"

	rep := AuditConsistency(st, truth, lcBase.Add(time.Minute))
	if rep.Correct != 2 || rep.Misjoins != 0 || rep.Missing != 0 {
		t.Errorf("report = %+v, want Correct 2 Misjoins 0 Missing 0", rep)
	}
	if rep.JoinAccuracy() != 1.0 || rep.Coverage() != 1.0 {
		t.Errorf("accuracy %.3f coverage %.3f, want 1.0/1.0", rep.JoinAccuracy(), rep.Coverage())
	}
}

// THE KILLER: the Store holds a stale UID for a pod the control plane has recreated.
// The audit must catch it as a mis-join, not silently accept it.
func TestAuditConsistencyDetectsMisjoin(t *testing.T) {
	clk := newFakeClock(lcBase)
	st := newTestStore(clk)
	st.Observe(podCoords("shop", "web-x", "uid-OLD"), roleFor("shop", "Deployment", "web"), lcBase, StateActive)

	truth := newFakeTruth()
	truth.pods["shop/web-x"] = "uid-NEW" // control plane recreated the pod; the store is stale

	rep := AuditConsistency(st, truth, lcBase.Add(time.Minute))
	if rep.Misjoins != 1 {
		t.Fatalf("Misjoins = %d, want 1 (stale store UID is a mis-join)", rep.Misjoins)
	}
	if len(rep.Details) != 1 || rep.Details[0].ModelUID != "uid-OLD" || rep.Details[0].TruthUID != "uid-NEW" {
		t.Errorf("misjoin detail = %+v, want model uid-OLD truth uid-NEW", rep.Details)
	}
	if rep.JoinAccuracy() != 0.0 {
		t.Errorf("accuracy = %.3f, want 0.0 (the only resolved entity is mis-joined)", rep.JoinAccuracy())
	}
}

func TestAuditConsistencyMissingIsLagNotMisjoin(t *testing.T) {
	clk := newFakeClock(lcBase)
	st := newTestStore(clk) // store has not observed the pod yet
	truth := newFakeTruth()
	truth.pods["shop/web-x"] = "uid-1"

	rep := AuditConsistency(st, truth, lcBase.Add(time.Minute))
	if rep.Missing != 1 || rep.Misjoins != 0 || rep.Correct != 0 {
		t.Errorf("report = %+v, want Missing 1 (lag, not a mis-join)", rep)
	}
}

func TestEvaluateGate(t *testing.T) {
	t.Run("pass: perfect", func(t *testing.T) {
		rep := ConsistencyReport{Ready: true, CheckedPods: 10, Correct: 10}
		g := EvaluateGate(rep, 0.99)
		if !g.Passed {
			t.Errorf("gate should pass: %+v", g)
		}
	})
	t.Run("fail: any mis-join", func(t *testing.T) {
		rep := ConsistencyReport{Ready: true, CheckedPods: 10, Correct: 9, Misjoins: 1}
		g := EvaluateGate(rep, 0.50)
		if g.Passed {
			t.Error("gate must fail with a mis-join, regardless of coverage")
		}
		if len(g.Reasons) == 0 {
			t.Error("expected a reason citing the mis-join")
		}
	})
	t.Run("fail: low coverage", func(t *testing.T) {
		rep := ConsistencyReport{Ready: true, CheckedPods: 10, Correct: 8, Missing: 2}
		g := EvaluateGate(rep, 0.99)
		if g.Passed {
			t.Error("gate must fail when coverage is below target")
		}
	})
	t.Run("pass: empty but synced cluster", func(t *testing.T) {
		if !EvaluateGate(ConsistencyReport{Ready: true}, 0.99).Passed {
			t.Error("a synced empty cluster should pass (no entities, no mis-joins)")
		}
	})
	t.Run("fail: not synced (must not false-pass on an empty read)", func(t *testing.T) {
		g := EvaluateGate(ConsistencyReport{Ready: false}, 0.99)
		if g.Passed {
			t.Error("a not-synced report must NOT pass — empty listers are not a clean cluster")
		}
		if len(g.Reasons) == 0 {
			t.Error("expected a reason citing not-synced")
		}
	})
}

func TestAuditorAggregates(t *testing.T) {
	a := NewAuditor()
	a.Record(AuditRecord{Outcome: OutcomeResolved})
	a.Record(AuditRecord{Outcome: OutcomeResolved})
	a.Record(AuditRecord{Outcome: OutcomeResolved})
	a.Record(AuditRecord{Outcome: OutcomeQuarantined, Reason: ReasonUnknownPod})
	a.Record(AuditRecord{Outcome: OutcomeQuarantined, Reason: ReasonUnknownPod})
	a.Record(AuditRecord{Outcome: OutcomeQuarantined, Reason: ReasonMissingLabels})
	a.Record(AuditRecord{Outcome: OutcomeDropped, Reason: ReasonPauseSandbox})

	s := a.Snapshot()
	if s.Resolved != 3 || s.Quarantined != 3 || s.Dropped != 1 {
		t.Errorf("counts = %+v", s)
	}
	if s.OrphanRate != 0.5 { // 3 quarantined / (3 resolved + 3 quarantined)
		t.Errorf("orphan rate = %.3f, want 0.5", s.OrphanRate)
	}
	if s.QuarantineByReason[string(ReasonUnknownPod)] != 2 || s.QuarantineByReason[string(ReasonMissingLabels)] != 1 {
		t.Errorf("quarantine-by-reason = %+v", s.QuarantineByReason)
	}
}

func TestAuditorVerifyStreamAccuracy(t *testing.T) {
	truth := newFakeTruth()
	truth.pods["shop/a"] = "ua"
	truth.pods["shop/b"] = "ub"

	a := NewAuditor()
	a.VerifyStream(podInstanceCEIFor("shop", "a", "ua"), truth)     // correct
	a.VerifyStream(podInstanceCEIFor("shop", "b", "ub"), truth)     // correct
	a.VerifyStream(podInstanceCEIFor("shop", "a", "uWRONG"), truth) // misjoin

	s := a.Snapshot()
	if s.Correct != 2 || s.Misjoins != 1 {
		t.Errorf("verify counts: correct=%d misjoins=%d", s.Correct, s.Misjoins)
	}
	if want := 2.0 / 3.0; s.JoinAccuracy < want-1e-9 || s.JoinAccuracy > want+1e-9 {
		t.Errorf("join accuracy = %.4f, want %.4f", s.JoinAccuracy, want)
	}
}
