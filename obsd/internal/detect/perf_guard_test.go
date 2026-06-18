package detect

import (
	"fmt"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/observe"
)

// perf_guard_test.go — a CI-runnable, machine-INDEPENDENT performance regression guard for
// the detection hot path. ns/op is too flaky across shared CI runners to pin; ALLOCATIONS
// are a deterministic property of the compiled code path (testing.AllocsPerRun returns the
// identical count on a fast or slow machine), so we gate on them.
//
// CRITICAL (the lesson from the adversarial review of the first design): the scale BENCHMARK
// calls Match with topo==nil, which SKIPS every spanned phenomenon AND the blast-radius walk
// (match.go) — exactly the O(entities²)-prone code (blastRadius loops selected-keys per
// finding, cascade.go). Production always passes a REAL topology (binder.go). So this guard
// builds a REAL topology + a realistic `selected` participation map and exercises that
// production path; a guard over the nil-topo path would certify code production never runs.
//
// Two teeth:
//   1. ALLOC CEILING — allocs-per-entity stays under a pinned ceiling (a doubling of
//      allocations — an accidental per-entity copy/marshal — blows through it).
//   2. ALLOC-SCALING FLATNESS — allocs-per-entity at 5000 entities is within a small factor
//      of allocs-per-entity at 1000. A genuinely super-linear (O(n²)) regression makes
//      allocs-per-entity GROW with n, tripping the ratio. This is machine-independent (it is
//      a ratio of two deterministic alloc counts) AND graph-version-robust (both n use the
//      SAME graph, so the per-entity phenomenon-count cancels out of the ratio).
//
// HONEST LIMIT: a constant-factor CPU regression (e.g. 2× the work, still O(n), with FLAT
// allocations) is caught by NEITHER tooth — that needs an absolute-ns floor, the flaky
// quantity we deliberately avoid. `just bench` stays the informational manual catch for it.
// So this guard PARTIALLY closes the perf gap: it locks allocation cost + super-linear
// scaling, not constant-factor wall-time.

// nodeFP builds a loud Node fingerprint (PSI above) so the spanned-phenomenon walk has a
// real neighbour to evaluate — the spanned code path executes at scale.
func nodeFP(i int) observe.Fingerprint {
	return observe.Fingerprint{
		CEIKey:      fmt.Sprintf("i|cl|cl|Node|node-%d|nodeuid-%d", i, i),
		Name:        fmt.Sprintf("node-%d", i),
		Kind:        "Node",
		EvaluatedAt: evalAt,
		Thresholds: []observe.VariableThreshold{{
			RuleID: "THR_NODE_CPU_PSI_STALL", Metric: "node_pressure_cpu_waiting_seconds_total",
			State: observe.StateAbove, BarSource: "config",
			Deriv: observe.DerivationRef{StreamID: "s", SampleAt: evalAt, How: "counter-ratio"},
		}},
	}
}

// perfTopology wires each container onto one of nNodes nodes via a fresh runs-on edge, so
// the spanned walk and blast-radius related() actually traverse topology at scale.
func perfTopology(t *testing.T, fps []observe.Fingerprint, nNodes int) *identity.EdgeStore {
	t.Helper()
	s := identity.NewEdgeStore(func() time.Time { return evalAt },
		map[identity.EdgeType]time.Duration{identity.EdgeRunsOn: 90 * time.Second}, 24*time.Hour)
	for i := range fps {
		if fps[i].Kind != "Container" {
			continue
		}
		nodeKey := fmt.Sprintf("i|cl|cl|Node|node-%d|nodeuid-%d", i%nNodes, i%nNodes)
		from, err := identity.ParseKey(fps[i].CEIKey)
		if err != nil {
			t.Fatalf("parse container key: %v", err)
		}
		to, err := identity.ParseKey(nodeKey)
		if err != nil {
			t.Fatalf("parse node key: %v", err)
		}
		s.Assert(identity.EdgeRunsOn, from, to, evalAt.Add(-10*time.Second))
	}
	return s
}

// perfSelected mirrors production participation: every container participates in the memory
// phenomena (so the blast-radius participation loop runs over all entities, the O(n²)-prone
// loop), and the downstream OOM phenomenon is present so blast radius actually walks.
func perfSelected(fps []observe.Fingerprint) map[string][]string {
	sel := make(map[string][]string, len(fps))
	for i := range fps {
		if fps[i].Kind == "Node" {
			sel[fps[i].CEIKey] = []string{"PHEN_OOM_KILL_CGROUP"}
			continue
		}
		sel[fps[i].CEIKey] = []string{"PHEN_MEMORY_LEAK", "PHEN_OOM_KILL_CGROUP"}
	}
	return sel
}

// perfInputs builds the full real-topology input set at entity count n.
func perfInputs(t *testing.T, m *Matcher, n int) (func() int, int) {
	t.Helper()
	nNodes := n / 100
	if nNodes < 2 {
		nNodes = 2
	}
	// A FIXED incident count (~10 leaking entities) regardless of n — a realistic cluster has
	// a bounded number of SIMULTANEOUS incidents, not a fixed fraction. This keeps blast radius
	// O(findings × entities) = O(n) (findings constant), so allocs-per-entity is flat in the
	// correct code and a super-linear regression shows as growth. (A fixed FRACTION would make
	// findings O(n) and blast radius O(n²) — a real property of broad participation, but a
	// stress envelope, not a regression signal.)
	faultyEvery := n / 10
	fps := benchFingerprints(n, faultyEvery) // ~10 MEMORY_LEAK signatures total
	for i := 0; i < nNodes; i++ {
		fps = append(fps, nodeFP(i))
	}
	topo := perfTopology(t, fps, nNodes)
	selected := perfSelected(fps)
	w := identity.TimeWindow{Start: evalAt.Add(-time.Minute), End: evalAt}
	run := func() int { return len(m.Match(fps, selected, topo, w)) }
	return run, len(fps)
}

// allocsPerEntity measures deterministic allocations per entity for the real-topology Match.
// AllocsPerRun(2) keeps the (race-instrumented) cost bounded; the count is deterministic, so
// two runs suffice — this is an allocation count, not a noisy timing.
func allocsPerEntity(t *testing.T, m *Matcher, n int) (float64, int) {
	run, total := perfInputs(t, m, n)
	findings := run() // warm + sanity
	allocs := testing.AllocsPerRun(2, func() { _ = run() })
	return allocs / float64(total), findings
}

// Pinned ceilings. Calibrated empirically against the REAL released graph under `go test
// -race` (the CI run mode) on 2026-06-18, Go 1.26: allocs/entity ≈ 353–357, flat across
// n=1000→5000 (ratio ≈ 0.99). A graph release that adds entity-local phenomena raises the
// per-entity baseline (the ceiling is graph-pinned); re-derive it deliberately if so. The
// FLATNESS ratio is graph-version-robust (the per-entity phenomenon count cancels in the
// ratio), so it is the primary O(n²) teeth; the absolute ceiling catches a doubling.
const (
	allocCeilingPerEntity = 450.0 // ~26% headroom over the race-mode baseline; a doubling (~710) trips it
	flatnessMaxRatio      = 1.5   // baseline ~0.99; an O(n²) regression makes allocs/entity grow ~5×
)

// TestMatchPerfGuard locks the detection hot path's allocation cost + super-linear scaling on
// the REAL-topology production path (spanned phenomena + blast radius active). See the file
// header for the two teeth and the honest constant-factor limit.
func TestMatchPerfGuard(t *testing.T) {
	if testing.Short() {
		t.Skip("perf guard is skipped under -short (it runs in CI's `go test -race ./...`)")
	}
	const small, large = 1000, 4000
	m := NewMatcher(loadGraph(t))

	apeSmall, fSmall := allocsPerEntity(t, m, small)
	apeLarge, fLarge := allocsPerEntity(t, m, large)
	t.Logf("allocs/entity: n=%d → %.1f (findings=%d), n=%d → %.1f (findings=%d)", small, apeSmall, fSmall, large, apeLarge, fLarge)

	// The blast-radius / spanned path must actually be EXERCISED, or the guard is vacuous.
	if fSmall == 0 || fLarge == 0 {
		t.Fatalf("no findings fired — the blast-radius path is not exercised (guard would be vacuous)")
	}

	// Tooth 1: absolute allocation ceiling (catches a doubling — an accidental per-entity copy).
	for _, c := range []struct {
		n   int
		ape float64
	}{{small, apeSmall}, {large, apeLarge}} {
		if c.ape > allocCeilingPerEntity {
			t.Errorf("n=%d: allocs/entity=%.1f exceeds ceiling %.0f — a per-entity allocation regression "+
				"(or a deliberate graph-content change needing a re-pinned baseline). See file header.",
				c.n, c.ape, allocCeilingPerEntity)
		}
	}

	// Tooth 2: scaling flatness — allocs/entity must not GROW super-linearly with n.
	if ratio := apeLarge / apeSmall; ratio > flatnessMaxRatio {
		t.Errorf("allocs/entity grew %.2f× from n=%d to n=%d (max %.1f) — a SUPER-LINEAR (O(n²)) "+
			"regression in the spanned/blast-radius walk (cascade.go). Baseline ratio ≈ 1.0.",
			ratio, small, large, flatnessMaxRatio)
	}
}
