package detect

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/observe"
)

const (
	kgPath     = "../../../ontology/graph/k8s_signal_kg.json"
	overlayDir = "../../../ontology/graph/overlays"
)

var evalAt = time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)

func loadGraph(t *testing.T) *graph.Graph {
	t.Helper()
	g, err := graph.LoadWithOverlays(kgPath, overlayDir)
	if err != nil {
		t.Fatalf("LoadWithOverlays: %v", err)
	}
	return g
}

// memLeakFingerprint builds a container fingerprint with the working-set variable
// at a given ladder state, slope, and staleness.
func memLeakFingerprint(state observe.ThresholdState, slope float64, slopeSamples int, stale bool) observe.Fingerprint {
	return observe.Fingerprint{
		CEIKey: "i|cl|shop|Pod|web-a|uid-a", Namespace: "shop", Name: "web-a", Kind: "Container",
		EvaluatedAt: evalAt,
		Thresholds: []observe.VariableThreshold{{
			RuleID: "THR_CONTAINER_MEM_WORKING_SET_VS_LIMIT",
			Metric: "container_memory_working_set_bytes",
			State:  state, Slope: slope, SlopeSamples: slopeSamples, Stale: stale,
			BarSource: "config", Deriv: observe.DerivationRef{StreamID: "s1", SampleAt: evalAt, How: "gauge-level"},
		}},
	}
}

// The headline M1 case: MEMORY_LEAK fires DEGRADED when working-set is rising — its
// other required member (the kubelet resource endpoint) is unobservable on a
// cAdvisor-only cluster, so the match is honestly degraded with that gap named.
func TestMemoryLeakDegradedMatch(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	fps := m.MatchFingerprint(memLeakFingerprint(observe.StateAtThreshold, 1500, 6, false)) // rising
	var leak *Finding
	for i := range fps {
		if fps[i].Phenomenon == "PHEN_MEMORY_LEAK" {
			leak = &fps[i]
		}
	}
	if leak == nil {
		t.Fatalf("MEMORY_LEAK should fire on a rising working set; got %d findings", len(fps))
	}
	if leak.Quality != QualityDegraded {
		t.Errorf("quality = %s, want degraded (a required member is unobservable here)", leak.Quality)
	}
	if leak.RequiredTotal != 2 || leak.RequiredMet != 1 || leak.RequiredUnobserved != 1 {
		t.Errorf("required accounting = total %d / met %d / unobs %d, want 2/1/1", leak.RequiredTotal, leak.RequiredMet, leak.RequiredUnobserved)
	}
	if leak.Completeness != 0.5 {
		t.Errorf("completeness = %v, want 0.5", leak.Completeness)
	}
	if len(leak.Unobservable) != 1 {
		t.Errorf("the unobservable required member must be named: %v", leak.Unobservable)
	}
	// The finding carries the AUTHORED note (the only "why") + the honesty caveat.
	if len(leak.Members) != 1 || leak.Members[0].Note == "" {
		t.Errorf("evidence must carry the authored member note: %+v", leak.Members)
	}
	if leak.GraphVersion == "" {
		t.Error("finding must pin the graph version (replay)")
	}
}

// A flat or falling working set does NOT fire MEMORY_LEAK (the required member's
// rising condition is not met) — no false positive.
func TestMemoryLeakNoFireWhenNotRising(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	for _, slope := range []float64{0, -500} {
		for _, f := range m.MatchFingerprint(memLeakFingerprint(observe.StateAtThreshold, slope, 6, false)) {
			if f.Phenomenon == "PHEN_MEMORY_LEAK" {
				t.Errorf("MEMORY_LEAK must not fire at slope %v", slope)
			}
		}
	}
}

// Materiality guard (doc 07 §3.6): working set RISING but well BELOW its bar (an
// idle container warming up) must NOT fire MEMORY_LEAK — only a rise that is
// material relative to the limit (at-threshold+) counts.
func TestMemoryLeakMaterialityGuard(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	if got := m.MatchFingerprint(memLeakFingerprint(observe.StateBelow, 5000, 6, false)); hasLeak(got) {
		t.Error("a rising-but-idle (below-bar) working set must not fire MEMORY_LEAK (warmup noise)")
	}
	// At-threshold AND rising DOES fire (material).
	if got := m.MatchFingerprint(memLeakFingerprint(observe.StateAtThreshold, 5000, 6, false)); !hasLeak(got) {
		t.Error("at-threshold + rising should fire (material leak signature)")
	}
}

// A stale or single-sample slope is treated as missing → MEMORY_LEAK does not fire
// (no positive required evidence), never a fabricated match.
func TestMemoryLeakStaleOrThinIsNoFire(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	if got := m.MatchFingerprint(memLeakFingerprint(observe.StateAtThreshold, 1500, 6, true)); hasLeak(got) {
		t.Error("stale rising slope must not fire MEMORY_LEAK")
	}
	if got := m.MatchFingerprint(memLeakFingerprint(observe.StateAtThreshold, 1500, 1, false)); hasLeak(got) {
		t.Error("single-sample slope (no real trend) must not fire MEMORY_LEAK")
	}
}

func hasLeak(fs []Finding) bool {
	for _, f := range fs {
		if f.Phenomenon == "PHEN_MEMORY_LEAK" {
			return true
		}
	}
	return false
}

// An empty fingerprint (a healthy entity with nothing crossing) yields NO findings —
// a single absence of evidence is silence, not noise (doc 07 §3.5).
func TestHealthyEntityNoFindings(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	fp := observe.Fingerprint{CEIKey: "i|cl|shop|Pod|web-a|uid-a", Kind: "Container", EvaluatedAt: evalAt}
	if got := m.MatchFingerprint(fp); len(got) != 0 {
		t.Errorf("healthy entity should produce no findings, got %+v", got)
	}
}

// Determinism (doc 07 §3.7): same fingerprint + same graph => identical findings.
func TestMatchDeterministic(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	fp := memLeakFingerprint(observe.StateAtThreshold, 1500, 6, false)
	a := m.MatchFingerprint(fp)
	b := m.MatchFingerprint(fp)
	if !reflect.DeepEqual(a, b) {
		t.Error("MatchFingerprint is not deterministic")
	}
}

// The matcher only considers entity-local phenomena (M1 scope); first/second-order
// ones are not evaluated here (they need traversal, M2/M3).
func TestOnlyEntityLocalPhenomena(t *testing.T) {
	g := loadGraph(t)
	m := NewMatcher(g)
	// 19 after v0.9.0: + PHEN_DISK_FILLING (entity-local, the hanging-signal wire).
	// (18 after v0.4.0: + PHEN_UPSTREAM_DEGRADATION, entity-local.) This count is a
	// graph-structure assertion; PHEN_DISK_FILLING DOES carry a check, so unlike
	// PHEN_UPSTREAM_DEGRADATION it can fire on a container filling toward its limit.
	if m.EntityLocalCount() != 19 {
		t.Errorf("entity-local phenomena = %d, want 19", m.EntityLocalCount())
	}
	// OOM is first-order — must not be in the matcher's set.
	for _, p := range m.entityLoc {
		if p.ID == "PHEN_OOM_KILL_CGROUP" {
			t.Error("first-order OOM must not be evaluated by the entity-local matcher")
		}
	}
}

// Regression (07-M1 finding 1/8/10): a gap-inconclusive slope does NOT fire.
func TestSlopeInconclusiveNoFire(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	fp := memLeakFingerprint(observe.StateAtThreshold, 5000, 2, false)
	fp.Thresholds[0].SlopeInconclusive = true
	if hasLeak(m.MatchFingerprint(fp)) {
		t.Error("a gap-inconclusive slope must not fire MEMORY_LEAK")
	}
}

// Regression (07-M1 finding 7): a default-flagged bar's provenance reaches the
// finding evidence (never surfaces as plain config-sourced).
func TestFlaggedBarProvenanceCarried(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	fp := memLeakFingerprint(observe.StateAtThreshold, 5000, 6, false)
	fp.Thresholds[0].Flagged = true
	for _, f := range m.MatchFingerprint(fp) {
		if f.Phenomenon == "PHEN_MEMORY_LEAK" {
			if len(f.Members) != 1 || !f.Members[0].BarFlagged {
				t.Errorf("flagged-bar provenance dropped: %+v", f.Members)
			}
			return
		}
	}
	t.Fatal("MEMORY_LEAK did not fire")
}

// Regression (07-M1 finding 3): the unobservable required member surfaces its
// AUTHORED note, not just a bare signal id.
func TestUnobservableMemberNoteSurfaced(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	for _, f := range m.MatchFingerprint(memLeakFingerprint(observe.StateAtThreshold, 5000, 6, false)) {
		if f.Phenomenon == "PHEN_MEMORY_LEAK" {
			if len(f.Unobservable) != 1 || !strings.Contains(f.Unobservable[0], "—") {
				t.Errorf("unobservable member must carry its authored note: %v", f.Unobservable)
			}
			return
		}
	}
	t.Fatal("MEMORY_LEAK did not fire")
}
