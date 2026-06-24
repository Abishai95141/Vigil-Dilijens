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
	// 23 after v0.15.0: + PHEN_PVC_FILLING (entity-local, pvc-filling-v1).
	// 22 after v0.14.0: + 3 PSI saturation phenomena (CPU/MEMORY/IO_PRESSURE_SATURATION,
	// entity-local, each carries a rate-guard check, psi-pressure-v1).
	// (19 after v0.9.0: + PHEN_DISK_FILLING, the hanging-signal wire; 18 after v0.4.0:
	// + PHEN_UPSTREAM_DEGRADATION.) This count is a graph-structure assertion.
	if m.EntityLocalCount() != 25 {
		t.Errorf("entity-local phenomena = %d, want 25", m.EntityLocalCount())
	}
	// OOM is first-order — must not be in the matcher's set.
	for _, p := range m.entityLoc {
		if p.ID == "PHEN_OOM_KILL_CGROUP" {
			t.Error("first-order OOM must not be evaluated by the entity-local matcher")
		}
	}
}

// cpuAggressorFingerprint builds a CPU-limited container carrying BOTH config-relative CPU bars
// on container_cpu_usage_seconds_total: the pre-existing 0.90 near-limit bar AND the new 1.0
// over-limit aggressor bar (docs/33 closure 2, v0.18.0). overState is the over-limit bar's ladder
// state. Two bars on one metric is exactly the ambiguity that made CPU_AGGRESSOR unobservable
// before the check rule-discriminator fix.
func cpuAggressorFingerprint(overState observe.ThresholdState) observe.Fingerprint {
	return observe.Fingerprint{
		CEIKey: "i|cl|shop|Pod|web-a|uid-a", Namespace: "shop", Name: "web-a", Kind: "Container",
		EvaluatedAt: evalAt,
		Thresholds: []observe.VariableThreshold{
			{
				RuleID: "THR_CONTAINER_CPU_USAGE_VS_LIMIT", // the co-existing 0.90 near-limit bar
				Metric: "container_cpu_usage_seconds_total", State: observe.StateAbove, BarSource: "config",
				Deriv: observe.DerivationRef{StreamID: "s-cpu", SampleAt: evalAt, How: "counter-rate"},
			},
			{
				RuleID: "THR_CONTAINER_CPU_OVER_OWN_LIMIT", // the 1.0 over-limit aggressor bar
				Metric: "container_cpu_usage_seconds_total", State: overState, BarSource: "config", Flagged: true,
				Deriv: observe.DerivationRef{StreamID: "s-cpu", SampleAt: evalAt, How: "counter-rate"},
			},
		},
	}
}

// CPU_AGGRESSOR fires on an over-limit container EVEN THOUGH the 0.90 near-limit bar is also bound
// on the same metric — the check's rule discriminator resolves the right variable, so the second
// bar no longer trips findThreshold's ambiguity guard. Without the fix this phenomenon was 100% inert.
func TestCPUAggressorFiresDespiteCoexistingNearLimitBar(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	fps := m.MatchFingerprint(cpuAggressorFingerprint(observe.StateAbove))
	var agg *Finding
	for i := range fps {
		if fps[i].Phenomenon == "PHEN_CPU_AGGRESSOR" {
			agg = &fps[i]
		}
	}
	if agg == nil {
		t.Fatalf("CPU_AGGRESSOR must fire over its own limit despite the 0.90 bar present (rule discriminator); got %d findings", len(fps))
	}
	if agg.RequiredTotal != 1 || agg.RequiredMet != 1 {
		t.Errorf("required accounting = total %d / met %d, want 1/1", agg.RequiredTotal, agg.RequiredMet)
	}
}

// Under its own limit (the over-limit bar in the approach band, not crossed) CPU_AGGRESSOR must
// NOT fire — even though the 0.90 near-limit bar IS crossed. The aggressor is the over-limit one.
func TestCPUAggressorNoFireWhenUnderOwnLimit(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	for _, f := range m.MatchFingerprint(cpuAggressorFingerprint(observe.StateAtThreshold)) {
		if f.Phenomenon == "PHEN_CPU_AGGRESSOR" {
			t.Error("CPU_AGGRESSOR must not fire when under the container's own CPU limit")
		}
	}
}

// workloadFingerprint builds a StatefulSet-anchored Workload fingerprint carrying the ready/desired
// ratio variable at the given ladder state (docs/33 closure 1, v0.18.0).
func workloadFingerprint(state observe.ThresholdState) observe.Fingerprint {
	return observe.Fingerprint{
		CEIKey: "i|cl|shop|StatefulSet|historian|uid-sts", Namespace: "shop", Name: "historian", Kind: "Workload",
		EvaluatedAt: evalAt,
		Thresholds: []observe.VariableThreshold{{
			RuleID: "THR_STS_READY_BELOW_DESIRED", Metric: "kube_statefulset_status_replicas_ready",
			State: state, BarSource: "default", Flagged: true,
			Deriv: observe.DerivationRef{StreamID: "ready1", DivisorID: "desired1", SampleAt: evalAt, How: "counter-ratio"},
		}},
	}
}

// WORKLOAD_UNAVAILABLE fires when ready/desired has crossed below the 1.0 bar (a StatefulSet with
// fewer Ready replicas than declared) — the MEASURED degraded flow node that roots a cascade on the
// real upstream. This is the matcher half of the closure-1 fix (the Materialize half is in observe).
func TestWorkloadUnavailableFires(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	fps := m.MatchFingerprint(workloadFingerprint(observe.StateAbove)) // ratio crossed below 1.0
	var wl *Finding
	for i := range fps {
		if fps[i].Phenomenon == "PHEN_WORKLOAD_UNAVAILABLE" {
			wl = &fps[i]
		}
	}
	if wl == nil {
		t.Fatalf("WORKLOAD_UNAVAILABLE must fire when ready<desired; got %d findings", len(fps))
	}
	if wl.RequiredTotal != 1 || wl.RequiredMet != 1 {
		t.Errorf("required accounting = total %d / met %d, want 1/1", wl.RequiredTotal, wl.RequiredMet)
	}
}

// A fully-ready StatefulSet (ratio at the 1.0 bar, healthy approach band, never crossed) must NOT
// fire WORKLOAD_UNAVAILABLE — no false positive on a healthy backbone workload.
func TestWorkloadUnavailableNoFireWhenFullyReady(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	for _, f := range m.MatchFingerprint(workloadFingerprint(observe.StateAtThreshold)) {
		if f.Phenomenon == "PHEN_WORKLOAD_UNAVAILABLE" {
			t.Error("WORKLOAD_UNAVAILABLE must not fire when all declared replicas are Ready")
		}
	}
}

// pvcFillingFingerprint builds a PVC carrying BOTH PVC-scoped rules on kubelet_volume_stats_used_bytes
// (the base THR_PVC_USED_VS_REQUESTED and pvc-filling's THR_PVC_USED_VS_REQUESTED_STORAGE) — the real
// duplicate-rule collision that made PVC_FILLING inert on every live cluster since v0.15.0. The storage
// rule's variable is rising over its bar; the check's rule discriminator must resolve it past the
// findThreshold ambiguity guard.
func pvcFillingFingerprint(slope float64, samples int) observe.Fingerprint {
	return observe.Fingerprint{
		CEIKey: "i|cl|shop|PersistentVolumeClaim|data-x|uid-pvc", Namespace: "shop", Name: "data-x", Kind: "PVC",
		EvaluatedAt: evalAt,
		Thresholds: []observe.VariableThreshold{
			{ // the co-existing base PVC bar on the same metric — the source of the ambiguity
				RuleID: "THR_PVC_USED_VS_REQUESTED", Metric: "kubelet_volume_stats_used_bytes",
				State: observe.StateAbove, Slope: slope, SlopeSamples: samples,
				Deriv: observe.DerivationRef{StreamID: "vol", SampleAt: evalAt, How: "gauge-level"},
			},
			{ // pvc-filling's own bar, named by the check discriminator
				RuleID: "THR_PVC_USED_VS_REQUESTED_STORAGE", Metric: "kubelet_volume_stats_used_bytes",
				State: observe.StateAbove, Slope: slope, SlopeSamples: samples, Flagged: true,
				Deriv: observe.DerivationRef{StreamID: "vol", SampleAt: evalAt, How: "gauge-level"},
			},
		},
	}
}

// PVC_FILLING fires on a rising volume EVEN THOUGH the base THR_PVC_USED_VS_REQUESTED bar is also bound
// on the same metric — the check's rule discriminator resolves the right variable past findThreshold's
// ambiguity guard. Without the fix this phenomenon was 100% inert on any cluster loading both overlays.
func TestPVCFillingFiresDespiteDuplicateBaseRule(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	fps := m.MatchFingerprint(pvcFillingFingerprint(1500, 6)) // rising, enough samples
	var pf *Finding
	for i := range fps {
		if fps[i].Phenomenon == "PHEN_PVC_FILLING" {
			pf = &fps[i]
		}
	}
	if pf == nil {
		t.Fatalf("PVC_FILLING must fire on a rising volume despite the base PVC bar on the same metric (rule discriminator); got %d findings", len(fps))
	}
}

// A full-but-flat volume (level over the bar, slope ~0) must NOT fire PVC_FILLING — it detects the
// fill TREND (the write-failure precursor), not a full disk (that is STORAGE_SATURATION's job).
func TestPVCFillingNoFireWhenNotRising(t *testing.T) {
	m := NewMatcher(loadGraph(t))
	for _, f := range m.MatchFingerprint(pvcFillingFingerprint(0, 6)) {
		if f.Phenomenon == "PHEN_PVC_FILLING" {
			t.Error("PVC_FILLING must not fire on a flat (full but not filling) volume")
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
