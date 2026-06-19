package replay

import (
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/binding"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/detect"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/observe"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/qss"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/selection"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/unexplained"
)

// METAMORPHIC PROPERTY — determinism under WITHIN-STREAM within-tick reordering
// (doc 05 §3.1/§3.5, doc 07 §3.7, doc 14 A12). The replay determinism contract is
// stated over "same readings", and one scrape round can land MORE THAN ONE sample
// on the SAME stream whose event timestamps are NOT monotonic in arrival order:
// cAdvisor stamps each reading with its own collection time, which can regress
// across a scrape (doc 14 A12). The hot ring stores samples in arrival order and
// is documented NOT to reorder (doc 05 §3.1) — so the read side must do the work:
//
//   - qss.HotStore.Latest (hot.go) must scan the ring for the TIMESTAMP-newest
//     sample, not return the arrival tail. The cited value, threshold rung and
//     staleness a fingerprint carries all come from Latest.
//   - observe.EvalRate / EvalGaugeSlope over HotStore.LastN must sort the window
//     by event time before differencing, so the slope/rate is over the
//     chronological run, not the arrival order.
//
// If EITHER read path used arrival order instead of event time, then permuting the
// order in which a round's same-stream samples were appended would change a tick's
// fingerprints — and therefore its digest — and "same readings ⇒ same digest"
// would be false. This test STRESSES that: every round appends two samples to the
// leak (working-set gauge) stream — a fresh reading stamped at recv and a
// cAdvisor-regressed reading stamped 5 s earlier carrying a LOWER value, so the
// two readings sit on DIFFERENT threshold rungs in the crossing rounds. It then
// permutes the within-round arrival order of ALL the round's samples and asserts
// the recorded live digests are byte-identical tick-for-tick AND that the permuted
// bundle replays byte-identically to the canonical one.
//
// It is NOT vacuous and it has teeth at the qss/observe interfaces a digest
// actually depends on: a HotStore.Latest that returned the arrival tail, or an
// EvalRate that trusted arrival order, makes the permutation where the regressed
// (lower-value, earlier-timestamp) leak sample arrives LAST cite the wrong value
// and diverge. Proven in TestReorderTestHasTeeth_LatestArrivalTail below with an
// in-test broken StreamReader double — no real source is touched.

// roundSample is one within-round append: which stream, the event timestamp
// (which may regress relative to the scrape's recv instant), and the value.
type roundSample struct {
	def qss.StreamDef
	at  time.Time
	v   float64
}

// canonicalRound builds the canonical (arrival == declaration order) list of the
// samples a single scrape round lands, for round index i anchored at recv. The
// leak stream gets TWO samples: the fresh reading at recv (the rising leak value)
// and a cAdvisor-regressed reading stamped 5 s earlier carrying a clearly lower
// value — so timestamp-newest != arrival order is exercised on a SINGLE stream.
func canonicalRound(i int, recv time.Time) []roundSample {
	val := float64((110 + 4*i) << 20)
	// The regressed reading is older by event time and ~30Mi lower in value. In the
	// rounds where the leak crosses the 121.6Mi bar, the two readings land on
	// different threshold rungs, so which one Latest cites is digest-bearing.
	regressed := val - float64(30<<20)
	oom := 0.0
	if i >= 6 {
		oom = 1.0
	}
	return []roundSample{
		{leakDef(), recv, val},                             // fresh: the timestamp-newest leak reading
		{leakDef(), recv.Add(-5 * time.Second), regressed}, // cAdvisor regression (doc 14 A12)
		{throttledDef(), recv, float64(10 * i)},
		{periodsDef(), recv, float64(15 * i)},
		{psiDef(), recv, float64(3 * i)},
		{oomDef(), recv, oom},
	}
}

// buildBundleOrdered is the live path parameterized by a within-round arrival
// permutation: order[k] selects which of the round's canonical samples is
// appended k-th. The live VALUES, event timestamps, bars, topology and evaluation
// are otherwise identical regardless of the permutation, so any digest difference
// is attributable SOLELY to within-round arrival order. Returns the recorded live
// tick digests.
func buildBundleOrdered(t *testing.T, dir string, g *graph.Graph, order []int) []string {
	t.Helper()
	cap, err := NewCapture(dir, qss.WarmConfig{SegmentDuration: 2 * time.Hour, Retention: 7 * 24 * time.Hour})
	if err != nil {
		t.Fatalf("NewCapture: %v", err)
	}
	if err := cap.WriteManifest(Manifest{
		CreatedAt: base, ClusterID: "test-cluster", GraphVersion: g.Version,
		ParamsVersion: "0", Profile: "dev", FPParams: fpParams(), ScrapeInterval: 15 * time.Second,
		HotRingCapacity: qss.HotCapacity(), EdgeBudgets: fixtureEdgeBudgets(),
		Contents: []string{"readings (qss segments)", "resolved bars per epoch",
			"topology snapshot per tick", "evaluation ticks with digests"},
	}); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	epoch, err := cap.SetBars(base, fixtureBindings())
	if err != nil || epoch != 1 {
		t.Fatalf("SetBars: epoch=%d err=%v", epoch, err)
	}

	rules := make(map[string]*graph.ThresholdRule, len(g.Rules))
	for _, r := range g.Rules {
		rules[r.ID] = r
	}
	matcher := detect.NewMatcher(g)
	res := &binding.Result{Bindings: fixtureBindings()}
	selected := selection.TierASet(res, g)
	tracker := detect.NewCascadeTracker(fpParams().CooccurrenceWindow)
	unexpTracker := unexplained.NewTracker(g.Version)
	live := newBundleReader()
	defs := []qss.StreamDef{leakDef(), throttledDef(), periodsDef(), psiDef(), oomDef()}
	for _, d := range defs {
		live.register(d)
	}

	pod, err := identity.ParseKey(podCEI)
	if err != nil {
		t.Fatal(err)
	}
	node, err := identity.ParseKey(nodeCEI)
	if err != nil {
		t.Fatal(err)
	}
	edges := identity.NewEdgeStore(func() time.Time { return base }, typedBudgets(), 24*time.Hour)

	var digests []string
	for i := 0; i < 8; i++ {
		recv := base.Add(time.Duration(i) * 15 * time.Second)
		edges.Assert(identity.EdgeRunsOn, pod, node, recv)

		round := canonicalRound(i, recv)
		if len(order) != len(round) {
			t.Fatalf("order permutation must cover all %d round samples, got %d", len(round), len(order))
		}
		// Append in the PERMUTED arrival order. The two leak samples share the round
		// but carry different event timestamps; the others share recv. Every sample
		// records with recv as its WARM receive stamp (one scrape round) so replay
		// re-appends in the same permuted arrival order it was written.
		for _, k := range order {
			s := round[k]
			live.hot.Append(s.def.ID, qss.Sample{At: s.at, Value: s.v})
			if err := cap.Warm().Append(s.def, recv, qss.Sample{At: s.at, Value: s.v}); err != nil {
				t.Fatalf("warm append: %v", err)
			}
		}

		evalNow := recv.Add(time.Second)
		topoSnap := edges.Snapshot()
		topo, err := identity.NewEdgeStoreFromSnapshot(topoSnap, typedBudgets())
		if err != nil {
			t.Fatal(err)
		}
		w := identity.TimeWindow{Start: evalNow.Add(-fpParams().CooccurrenceWindow), End: evalNow}
		fps := observe.Materialize(res, rules, live, fpParams(), evalNow)
		findings := matcher.Match(fps, selected, topo, w)
		cascades := matcher.Cascades(evalNow, findings, tracker, topo, w)
		tracker.Observe(evalNow, findings)
		unexp := unexpTracker.Route(evalNow, fps, findings)
		digest, _, derr := Digest(evalNow, fps, findings, cascades, unexp)
		if derr != nil {
			t.Fatalf("digest: %v", derr)
		}
		if err := cap.Tick(TickRecord{EvalNow: evalNow, BarsEpoch: epoch, Topology: topoSnap, Digest: digest,
			Fingerprints: len(fps), Findings: len(findings)}); err != nil {
			t.Fatalf("tick: %v", err)
		}
		digests = append(digests, digest)
	}
	if err := cap.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return digests
}

func TestDeterminismUnderWithinTickReordering(t *testing.T) {
	g := loadGraph(t)

	// 6 within-round samples (two on the leak stream, one each on the other four).
	canonical := []int{0, 1, 2, 3, 4, 5}
	reversed := []int{5, 4, 3, 2, 1, 0}
	rotated := []int{2, 5, 1, 4, 0, 3} // an arbitrary non-trivial permutation (regressed[1] before fresh[0])

	// All three permutations must place the two leak samples (indices 0 and 1) in a
	// DIFFERENT relative arrival order across the set, or we would never exercise
	// the regressing-timestamp scan. canonical: fresh-then-regressed; reversed and
	// rotated: regressed-then-fresh.
	leakFreshBeforeRegressed := func(order []int) bool {
		var fresh, regressed int
		for pos, k := range order {
			switch k {
			case 0:
				fresh = pos
			case 1:
				regressed = pos
			}
		}
		return fresh < regressed
	}
	if !leakFreshBeforeRegressed(canonical) {
		t.Fatal("canonical must append the fresh leak sample before the regressed one")
	}
	if leakFreshBeforeRegressed(reversed) || leakFreshBeforeRegressed(rotated) {
		t.Fatal("the permutations must flip the two leak samples' arrival order, or the scan is never stressed")
	}

	canonDir := t.TempDir()
	canonDigests := buildBundleOrdered(t, canonDir, g, canonical)

	// Guard against a vacuous test: the canonical bundle must carry real findings,
	// or "identical digests" would just mean "both empty".
	canonRep, err := Run(Options{BundleDir: canonDir, Graph: g})
	if err != nil {
		t.Fatalf("canonical Run: %v", err)
	}
	fired := 0
	for _, tk := range canonRep.Ticks {
		fired += tk.Findings
	}
	if fired == 0 {
		t.Fatal("the canonical bundle must carry real findings, or reordering proves nothing")
	}

	for _, perm := range []struct {
		name  string
		order []int
	}{
		{"reversed", reversed},
		{"rotated", rotated},
	} {
		t.Run(perm.name, func(t *testing.T) {
			permDir := t.TempDir()
			permDigests := buildBundleOrdered(t, permDir, g, perm.order)

			// (1) Live capture: a permuted within-round arrival order — including the
			// two same-stream leak readings whose timestamps regress — recorded the
			// SAME per-tick digest. If Latest returned the arrival tail (or EvalRate
			// trusted arrival order) this fails.
			if len(permDigests) != len(canonDigests) {
				t.Fatalf("tick count differs: canon=%d perm=%d", len(canonDigests), len(permDigests))
			}
			for i := range canonDigests {
				if permDigests[i] != canonDigests[i] {
					t.Fatalf("tick %d: within-tick reordering changed the live digest\n canon=%s\n perm =%s",
						i, canonDigests[i], permDigests[i])
				}
			}

			// (2) Replay: the permuted bundle replays clean AND to the identical
			// replayed digests as the canonical bundle — same readings (modulo
			// arrival order) ⇒ same fingerprints/findings ⇒ same bytes.
			permRep, err := Run(Options{BundleDir: permDir, Graph: g})
			if err != nil {
				t.Fatalf("permuted Run: %v", err)
			}
			if permRep.Mismatches != 0 {
				t.Fatalf("permuted bundle did not replay clean: %d mismatches", permRep.Mismatches)
			}
			if len(permRep.Ticks) != len(canonRep.Ticks) {
				t.Fatalf("replay tick count differs: canon=%d perm=%d", len(canonRep.Ticks), len(permRep.Ticks))
			}
			for i := range canonRep.Ticks {
				if permRep.Ticks[i].ReplayedDigest != canonRep.Ticks[i].ReplayedDigest {
					t.Fatalf("tick %d: within-tick reordering changed the REPLAYED digest\n canon=%s\n perm =%s",
						i, canonRep.Ticks[i].ReplayedDigest, permRep.Ticks[i].ReplayedDigest)
				}
			}
		})
	}
}

// arrivalTailReader is a broken observe.StreamReader double whose Latest returns
// the ARRIVAL-tail sample (most-recently-appended) instead of the timestamp-newest
// one — exactly the bug HotStore.Latest's regressing-timestamp scan exists to
// avoid (hot.go, doc 14 A12). LastN/StreamType/StreamsFor delegate to a real
// reader. It exists only to PROVE the reordering test has teeth, by showing the
// metamorphic property below diverges under it.
type arrivalTailReader struct {
	inner *bundleReader
}

func (r *arrivalTailReader) Latest(streamID string) (qss.Sample, bool) {
	tail := r.inner.LastN(streamID, 1) // LastN returns up to n most recent BY ARRIVAL
	if len(tail) == 0 {
		return qss.Sample{}, false
	}
	return tail[len(tail)-1], true
}
func (r *arrivalTailReader) LastN(streamID string, n int) []qss.Sample {
	return r.inner.LastN(streamID, n)
}
func (r *arrivalTailReader) StreamType(streamID string) (string, bool) {
	return r.inner.StreamType(streamID)
}
func (r *arrivalTailReader) StreamsFor(uid, metric string) []string {
	return r.inner.StreamsFor(uid, metric)
}

var _ observe.StreamReader = (*arrivalTailReader)(nil)

// reorderDigests replays one round-permutation through Materialize with the given
// StreamReader and returns the per-tick digests — the same live evaluation
// buildBundleOrdered performs, without touching the warm capture (we only need the
// digest sequence). It is the kernel the teeth proof and the real property share.
func reorderDigests(t *testing.T, g *graph.Graph, order []int, makeReader func(*bundleReader) observe.StreamReader) []string {
	t.Helper()
	rules := make(map[string]*graph.ThresholdRule, len(g.Rules))
	for _, r := range g.Rules {
		rules[r.ID] = r
	}
	matcher := detect.NewMatcher(g)
	res := &binding.Result{Bindings: fixtureBindings()}
	selected := selection.TierASet(res, g)
	tracker := detect.NewCascadeTracker(fpParams().CooccurrenceWindow)
	unexpTracker := unexplained.NewTracker(g.Version)
	live := newBundleReader()
	for _, d := range []qss.StreamDef{leakDef(), throttledDef(), periodsDef(), psiDef(), oomDef()} {
		live.register(d)
	}
	reader := makeReader(live)

	pod, err := identity.ParseKey(podCEI)
	if err != nil {
		t.Fatal(err)
	}
	node, err := identity.ParseKey(nodeCEI)
	if err != nil {
		t.Fatal(err)
	}
	edges := identity.NewEdgeStore(func() time.Time { return base }, typedBudgets(), 24*time.Hour)

	var digests []string
	for i := 0; i < 8; i++ {
		recv := base.Add(time.Duration(i) * 15 * time.Second)
		edges.Assert(identity.EdgeRunsOn, pod, node, recv)
		round := canonicalRound(i, recv)
		for _, k := range order {
			s := round[k]
			live.hot.Append(s.def.ID, qss.Sample{At: s.at, Value: s.v})
		}
		evalNow := recv.Add(time.Second)
		topoSnap := edges.Snapshot()
		topo, err := identity.NewEdgeStoreFromSnapshot(topoSnap, typedBudgets())
		if err != nil {
			t.Fatal(err)
		}
		w := identity.TimeWindow{Start: evalNow.Add(-fpParams().CooccurrenceWindow), End: evalNow}
		fps := observe.Materialize(res, rules, reader, fpParams(), evalNow)
		findings := matcher.Match(fps, selected, topo, w)
		cascades := matcher.Cascades(evalNow, findings, tracker, topo, w)
		tracker.Observe(evalNow, findings)
		unexp := unexpTracker.Route(evalNow, fps, findings)
		digest, _, derr := Digest(evalNow, fps, findings, cascades, unexp)
		if derr != nil {
			t.Fatalf("digest: %v", derr)
		}
		digests = append(digests, digest)
	}
	return digests
}

// TestReorderTestHasTeeth_LatestArrivalTail is the RED of the red-green: it proves
// the metamorphic property above is not vacuous by showing it DIVERGES under a
// broken Latest. With the real HotStore.Latest (timestamp-newest scan) the
// canonical and reversed arrival orders produce identical digests; with the
// arrival-tail double they do NOT — because in the reversed order the regressed
// (lower-value, earlier-timestamp) leak sample is appended last, so the broken
// Latest cites it and lands the working set on a different threshold rung. No real
// source is mutated: the bug lives entirely in the in-test double.
func TestReorderTestHasTeeth_LatestArrivalTail(t *testing.T) {
	g := loadGraph(t)
	canonical := []int{0, 1, 2, 3, 4, 5}
	reversed := []int{5, 4, 3, 2, 1, 0}

	real := func(r *bundleReader) observe.StreamReader { return r }
	broken := func(r *bundleReader) observe.StreamReader { return &arrivalTailReader{inner: r} }

	// Sanity: with the REAL reader the property holds (canonical == reversed).
	realCanon := reorderDigests(t, g, canonical, real)
	realRev := reorderDigests(t, g, reversed, real)
	identical := true
	for i := range realCanon {
		if realCanon[i] != realRev[i] {
			identical = false
			break
		}
	}
	if !identical {
		t.Fatal("with the real timestamp-newest Latest the property must HOLD; it diverged — the fixture is wrong")
	}

	// Teeth: with the broken arrival-tail Latest the property is VIOLATED. If this
	// did NOT diverge, the test could not distinguish a correct Latest from a
	// broken one — i.e. it would be vacuous.
	brokenCanon := reorderDigests(t, g, canonical, broken)
	brokenRev := reorderDigests(t, g, reversed, broken)
	diverged := false
	for i := range brokenCanon {
		if brokenCanon[i] != brokenRev[i] {
			diverged = true
			break
		}
	}
	if !diverged {
		t.Fatal("a broken arrival-tail Latest did NOT change any digest under reordering — " +
			"the reordering property is vacuous (it cannot catch an order-sensitive read)")
	}
}
