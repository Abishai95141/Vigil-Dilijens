package binding

import (
	"reflect"
	"strings"
	"testing"
)

// Per-phenomenon observability on the kind cluster: every one of the 40 phenomena
// gets a verdict; nothing is full unless every required member is cleanly
// obtainable; reasons travel with every degraded verdict. Conservative by
// construction: indeterminate members can only degrade, never promote. v0.4.0
// (doc 15 Phase C) added the two cross-service relation roles — they have no
// resolvable members on this cluster, so they verdict "none" (honest).
func TestPhenomenonObservabilityKind(t *testing.T) {
	g := loadGraph(t)
	avail := GateSignals(g, kindFacts())
	rep := PhenomenonObservability(g, avail)

	if got := rep.Full + rep.Partial + rep.None; got != 41 || len(rep.PerPhenomenon) != 41 {
		t.Fatalf("verdicts = %d (full %d partial %d none %d), want 41 (+ PHEN_DISK_FILLING, v0.9.0)", got, rep.Full, rep.Partial, rep.None)
	}
	for _, p := range rep.PerPhenomenon {
		if p.Observability != "full" && len(p.MissingReasons) == 0 {
			t.Errorf("%s: %s with no stated reasons", p.PhenomenonID, p.Observability)
		}
		if p.Observability == "full" && (p.RequiredOutOfScope > 0 || p.RequiredIndeterminate > 0) {
			t.Errorf("%s: full despite degraded members: %+v", p.PhenomenonID, p)
		}
	}

	byID := map[string]PhenomenonCoverage{}
	for _, p := range rep.PerPhenomenon {
		byID[p.PhenomenonID] = p
	}
	// Conntrack exhaustion needs node-exporter (absent on kind): must not be full,
	// and the reason must name it.
	ct := byID["PHEN_CONNTRACK_EXHAUSTION"]
	if ct.Observability == "full" {
		t.Errorf("conntrack should not be fully observable without node-exporter: %+v", ct)
	}
	if !strings.Contains(strings.Join(ct.MissingReasons, " "), "node-exporter") {
		t.Errorf("conntrack reasons should name node-exporter: %v", ct.MissingReasons)
	}
	// The OOM phenomenon has cAdvisor-backed required members obtainable on kind:
	// it must be at least partially observable.
	oom := byID["PHEN_OOM_KILL_CGROUP"]
	if oom.Observability == "none" {
		t.Errorf("OOM should be at least partial on kind (cAdvisor members obtainable): %+v", oom)
	}

	// Determinism.
	rep2 := PhenomenonObservability(g, GateSignals(g, kindFacts()))
	if !reflect.DeepEqual(rep, rep2) {
		t.Error("observability rollup is not deterministic")
	}
}
