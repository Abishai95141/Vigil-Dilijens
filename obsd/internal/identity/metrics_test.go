package identity

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// gather collects a registry into a map keyed by "name" or "name{l=v,...}" (labels
// sorted by Gather), with the gauge/counter value.
func gather(t *testing.T, reg *prometheus.Registry) map[string]float64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	out := map[string]float64{}
	for _, mf := range mfs {
		for _, m := range mf.GetMetric() {
			key := mf.GetName()
			if lbls := m.GetLabel(); len(lbls) > 0 {
				key += "{"
				for i, l := range lbls {
					if i > 0 {
						key += ","
					}
					key += l.GetName() + "=" + l.GetValue()
				}
				key += "}"
			}
			switch {
			case m.Gauge != nil:
				out[key] = m.GetGauge().GetValue()
			case m.Counter != nil:
				out[key] = m.GetCounter().GetValue()
			}
		}
	}
	return out
}

func TestCollectorEmitsHealthAndGateMetrics(t *testing.T) {
	clk := newFakeClock(lcBase)
	store := newTestStore(clk)
	store.Observe(podCoords("shop", "web-x", "uid-1"), roleFor("shop", "Deployment", "web"), lcBase, StateActive)

	edges := newEdgeStore(clk)
	edges.Assert(EdgeRunsOn, podCEI("web-x", "uid-1"), nodeCEI("worker-1", "node-1"), lcBase)

	auditor := NewAuditor()
	auditor.Record(AuditRecord{Outcome: OutcomeResolved})
	auditor.Record(AuditRecord{Outcome: OutcomeQuarantined, Reason: ReasonUnknownPod})

	// A consistency report with one mis-join — the gate must fail.
	consistency := func() ConsistencyReport {
		return ConsistencyReport{Ready: true, CheckedPods: 2, Correct: 1, Misjoins: 1}
	}

	reg := prometheus.NewRegistry()
	reg.MustRegister(NewCollector(store, edges, auditor, consistency, 0.99))
	vals := gather(t, reg)

	check := func(name string, want float64) {
		t.Helper()
		got, ok := vals[name]
		if !ok {
			t.Errorf("missing metric %s", name)
			return
		}
		if got != want {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}

	check("vigil_identity_active_entities", 1)
	check("vigil_identity_misjoins", 1)
	check("vigil_identity_join_accuracy", 0.5)     // 1 correct / (1 correct + 1 misjoin)
	check("vigil_identity_join_coverage", 0.5)     // 1 correct / 2 checked
	check("vigil_identity_phase0a_gate_passed", 0) // a mis-join fails the gate
	check("vigil_identity_orphan_rate", 0.5)       // 1 quarantined / (1 resolved + 1 quarantined)
	check("vigil_identity_streams_resolved_total", 1)
	check("vigil_identity_streams_quarantined_total", 1)
	check("vigil_identity_quarantine_total{reason=unknown-pod}", 1)
	check("vigil_identity_edges{status=valid,type=runs-on}", 1)
}

func TestCollectorGatePassesWithNoMisjoins(t *testing.T) {
	clk := newFakeClock(lcBase)
	store := newTestStore(clk)
	edges := newEdgeStore(clk)
	consistency := func() ConsistencyReport {
		return ConsistencyReport{Ready: true, CheckedPods: 100, Correct: 100} // perfect
	}
	reg := prometheus.NewRegistry()
	reg.MustRegister(NewCollector(store, edges, NewAuditor(), consistency, 0.99))
	vals := gather(t, reg)
	if vals["vigil_identity_phase0a_gate_passed"] != 1 {
		t.Errorf("gate should pass with no mis-joins and full coverage")
	}
	if vals["vigil_identity_misjoins"] != 0 {
		t.Errorf("expected 0 mis-joins")
	}
}

// The gate must NOT report a pass while the identity layer is unsynced — an empty
// read during the sync window must not publish a false ALL-CLEAR.
func TestCollectorGateNotPassedWhenNotSynced(t *testing.T) {
	store := newTestStore(newFakeClock(lcBase))
	edges := newEdgeStore(newFakeClock(lcBase))
	notSynced := func() ConsistencyReport { return ConsistencyReport{Ready: false} }
	reg := prometheus.NewRegistry()
	reg.MustRegister(NewCollector(store, edges, NewAuditor(), notSynced, 0.99))
	vals := gather(t, reg)
	if vals["vigil_identity_phase0a_gate_passed"] != 0 {
		t.Errorf("gate_passed = %v while not synced, want 0", vals["vigil_identity_phase0a_gate_passed"])
	}
}

// Ensure the registered collector is internally consistent (no duplicate/invalid
// descriptors) — MustRegister + Gather would panic/error otherwise.
func TestCollectorRegistersCleanly(t *testing.T) {
	store := newTestStore(newFakeClock(lcBase))
	edges := newEdgeStore(newFakeClock(lcBase))
	reg := prometheus.NewRegistry()
	reg.MustRegister(NewCollector(store, edges, NewAuditor(), nil, 0.99)) // nil consistency is allowed
	if _, err := reg.Gather(); err != nil {
		t.Fatalf("gather with nil consistency: %v", err)
	}
	var _ dto.MetricFamily // keep the dto import meaningful
}
