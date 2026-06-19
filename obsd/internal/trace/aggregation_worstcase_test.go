package trace

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/candidate"
)

// ---------------------------------------------------------------------------
// Worst-case coverage of the span → observed call-graph aggregation.
//
// Three properties under attack, beyond what trace_test.go's forward/reverse
// pair proves:
//   1. INPUT-ORDER INVARIANCE under arbitrary shuffles (a deterministic, seeded
//      permutation sweep, not just reverse) — the graph AND the staged candidates
//      are byte-identical regardless of the order spans arrive in.
//   2. The graph is MEASURED OBSERVED TOPOLOGY, never causal — the arrow records
//      who called whom; a slow callee never flips the arrow, never becomes a
//      cause, and the words cause/root-cause appear nowhere in the output.
//   3. DROPPED / PARTIAL-SPAN HONESTY — orphans (parent not sampled) are COUNTED
//      and surfaced, never silently dropped or guessed; every candidate cites the
//      census-incomplete nature of sampled traces.
//
// No time.Now anywhere: timestamps are fixed; the shuffle PRNG is seeded with a
// fixed constant so the sweep itself is reproducible.
// ---------------------------------------------------------------------------

// ts builds a fixed timestamp at the given millisecond offset from a fixed epoch.
func ts(millis int) time.Time {
	base := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	return base.Add(time.Duration(millis) * time.Millisecond)
}

// richSpans is a non-trivial multi-trace batch exercising every path the builder
// distinguishes: cross-service calls, an intra-service child (no edge), a root
// (no caller), an orphan (parent absent), repeated callee spans (percentiles),
// an error span, AND a pathological spanID collision (a merged/concatenated
// export reusing one spanID for two differing parent spans). Two real traces so
// Traces accounting is exercised; a third (t3) carries the collision.
//
// The collision: spanID "dup-parent" appears twice as a t3 root — once as
// service "checkout", once as service "zz-loser" — and a t3 currencyservice
// child names it as parent. Dedup must pick a FIXED winner by lessSpan's total
// order (Service ascending ⇒ "checkout" wins, "zz-loser" loses) so the child's
// resolved caller — hence the whole graph — is INVARIANT to span arrival order.
// Under a last-write-wins dedup the winner flips with the shuffle (sometimes
// "zz-loser"), spawning a phantom zz-loser→currencyservice edge and splitting
// the currency call count; the shuffle sweep catches exactly that.
func richSpans() []Span {
	mk := func(trace, id, parent, svc, name string, start, end int, errd bool) Span {
		return Span{TraceID: trace, SpanID: id, ParentSpanID: parent, Service: svc,
			Name: name, StartTime: ts(start), EndTime: ts(end), StatusError: errd}
	}
	return []Span{
		// trace t1
		mk("t1", "t1-root", "", "frontend", "GET /checkout", 0, 100, false),
		mk("t1", "t1-co", "t1-root", "checkout", "Checkout.Place", 5, 90, false),
		mk("t1", "t1-cur-1", "t1-co", "currencyservice", "Convert", 10, 25, false), // 15ms
		mk("t1", "t1-cur-2", "t1-co", "currencyservice", "Convert", 30, 50, false), // 20ms
		mk("t1", "t1-cur-3", "t1-co", "currencyservice", "Convert", 55, 70, false), // 15ms
		mk("t1", "t1-pay", "t1-co", "paymentservice", "Charge", 30, 80, true),      // 50ms, error
		mk("t1", "t1-co-internal", "t1-co", "checkout", "validate", 40, 45, false), // intra-service: NO edge
		mk("t1", "t1-orphan", "t1-gone", "emailservice", "Send", 5, 15, false),     // ORPHAN
		// trace t2
		mk("t2", "t2-root", "", "frontend", "GET /checkout", 200, 320, false),
		mk("t2", "t2-co", "t2-root", "checkout", "Checkout.Place", 205, 300, false),
		mk("t2", "t2-cur", "t2-co", "currencyservice", "Convert", 210, 240, false), // 30ms
		// trace t3 — pathological spanID collision on "dup-parent":
		mk("t3", "dup-parent", "", "checkout", "Checkout.Place", 400, 480, false),       // winner (Service "checkout")
		mk("t3", "dup-parent", "", "zz-loser", "Bogus", 400, 480, false),                // loser (Service "zz-loser")
		mk("t3", "t3-cur", "dup-parent", "currencyservice", "Convert", 410, 428, false), // 18ms child of the collided parent
	}
}

// shuffled returns a permutation of spans driven by a seeded PRNG (deterministic,
// no wall-clock). Returns a fresh slice; the input is untouched.
func shuffled(spans []Span, seed int64) []Span {
	out := make([]Span, len(spans))
	copy(out, spans)
	r := rand.New(rand.NewSource(seed))
	r.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

// TestCallGraphInvariantUnderShuffleSweep is the worst-case order-invariance gate:
// across a deterministic sweep of arbitrary permutations, BOTH the call graph and
// the staged topology candidates are byte-identical to the canonical run. This
// fails for any impl that lets Go's randomized map iteration, last-write-wins
// dedup, or unsorted percentile inputs leak into the output.
func TestCallGraphInvariantUnderShuffleSweep(t *testing.T) {
	canon := richSpans()
	gCanon := BuildCallGraph(canon)
	cCanon := ProposeTopology(gCanon, testResolver, "cl", "vtest")

	// A correct graph over this batch has exactly these cross-service edges; assert it
	// up front so a degenerate (e.g. empty) graph can't make the sweep vacuously pass.
	// Edges aggregate by (caller,callee) SERVICE pair across ALL traces, so the two
	// frontend→checkout and the five checkout→currencyservice calls collapse to one
	// edge each: 3 distinct edges. The collision MUST resolve to "checkout" (the
	// lessSpan winner), so NO phantom zz-loser→currencyservice edge exists — assert
	// it up front, since a last-write-wins dedup makes this 4 edges on some shuffles.
	if len(gCanon.Edges) != 3 {
		t.Fatalf("canonical edges = %d, want 3 (frontend→checkout, checkout→currencyservice, checkout→paymentservice): %+v",
			len(gCanon.Edges), gCanon.Edges)
	}
	if _, phantom := edge(gCanon, "zz-loser", "currencyservice"); phantom {
		t.Fatalf("collision loser leaked an edge: dedup did not pick the deterministic lessSpan winner: %+v", gCanon.Edges)
	}
	// Cross-trace aggregation is itself a MEASURED property: checkout→currencyservice
	// has 5 calls (3 in t1 + 1 in t2 + 1 in t3, the t3 call witnessed only because the
	// collided "dup-parent" deterministically resolves to "checkout"), proving edges
	// key on the service pair and dedup is order-stable.
	if cur, ok := edge(gCanon, "checkout", "currencyservice"); !ok || cur.Calls != 5 {
		t.Fatalf("checkout→currencyservice = %+v, want calls=5 aggregated across all traces (incl. the collision winner)", cur)
	}

	for seed := int64(1); seed <= 200; seed++ {
		ss := shuffled(canon, seed)
		g := BuildCallGraph(ss)
		if !reflect.DeepEqual(g, gCanon) {
			t.Fatalf("seed %d: call graph NOT invariant to span order\n got: %+v\nwant: %+v", seed, g, gCanon)
		}
		c := ProposeTopology(g, testResolver, "cl", "vtest")
		if !reflect.DeepEqual(c, cCanon) {
			t.Fatalf("seed %d: topology candidates NOT invariant to span order", seed)
		}
	}
}

// TestPercentilesInvariantToDurationArrivalOrder isolates the percentile path: a
// single edge fed many callee spans in shuffled order must yield byte-identical
// p50/p95/max every time (percentiles are computed over SORTED durations). Catches
// a regression that computes a quantile before sorting, or that lets the slice
// order (hence the interpolation) depend on span arrival order.
func TestPercentilesInvariantToDurationArrivalOrder(t *testing.T) {
	// Caller "a" → callee "b"; 9 children with spread durations (10..90ms).
	build := func(seed int64) CallEdge {
		spans := []Span{{TraceID: "t", SpanID: "root", Service: "a", StartTime: ts(0), EndTime: ts(1000)}}
		for i := 1; i <= 9; i++ {
			d := i * 10
			spans = append(spans, Span{
				TraceID: "t", SpanID: "c" + string(rune('0'+i)), ParentSpanID: "root",
				Service: "b", StartTime: ts(0), EndTime: ts(d),
			})
		}
		g := BuildCallGraph(shuffled(spans, seed))
		e, ok := edge(g, "a", "b")
		if !ok {
			t.Fatalf("seed %d: a→b edge missing", seed)
		}
		return e
	}

	want := build(0)
	// durations sorted: 10,20,30,40,50,60,70,80,90 (n=9).
	// p50 → rank 4.0 → 50; p95 → rank 7.6 → 80*0.4+90*0.6=86; max → 90.
	if !approx(want.P50Millis, 50) || !approx(want.P95Millis, 86) || !approx(want.MaxMillis, 90) {
		t.Fatalf("baseline percentiles wrong: %+v (want p50=50 p95=86 max=90)", want)
	}
	if want.Calls != 9 {
		t.Fatalf("baseline calls = %d, want 9", want.Calls)
	}
	for seed := int64(1); seed <= 100; seed++ {
		got := build(seed)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("seed %d: edge stats drift with arrival order\n got: %+v\nwant: %+v", seed, got, want)
		}
	}
}

// TestGraphIsObservedTopologyNeverCausal enforces the cardinal charter rule for
// this lane at depth: the discovered call graph is MEASURED OBSERVED STRUCTURE,
// never a cause. No candidate may be a causal hypothesis; the only relation is
// the structural "topology"; and the literal vocabulary of causation must not
// appear in ANY surfaced field (subject, relation, evidence, lineage). A slow,
// erroring callee (paymentservice) must NOT be promoted to a "cause" of its caller.
func TestGraphIsObservedTopologyNeverCausal(t *testing.T) {
	g := BuildCallGraph(richSpans())
	cands := ProposeTopology(g, testResolver, "cl", "vtest")
	if len(cands) == 0 {
		t.Fatal("no candidates produced; cannot assert the charter property")
	}

	// Words that would betray a fused causal claim. The arrow is who-called-whom;
	// "caller"/"callee" are positional and allowed — anything in this list is not.
	banned := []string{"cause", "caused", "causal", "root-cause", "root cause", "because", "blame", "responsible"}

	for _, c := range cands {
		if c.Kind == candidate.KindCausalHypothesis {
			t.Fatalf("CHARTER BREACH: trace emitted a causal hypothesis (a call is observed structure, not a cause): %+v", c)
		}
		if c.Kind != candidate.KindEdge || c.Relation != "topology" {
			t.Fatalf("CHARTER BREACH: kind=%q relation=%q, want edge/topology (the store also rejects a causal edge): %+v",
				c.Kind, c.Relation, c)
		}
		// The structural guard must accept it (a causal relation would be rejected here).
		if err := candidate.Validate(c); err != nil {
			t.Fatalf("candidate failed the structural guard: %v (%+v)", err, c)
		}

		// Sweep every surfaced string for the causal vocabulary.
		blob := strings.ToLower(c.Subject + "\x00" + c.Relation + "\x00" +
			c.Lineage.Source + "\x00" + c.Lineage.Method + "\x00" + strings.Join(c.Lineage.Inputs, "\x00"))
		for _, ev := range c.Evidence {
			blob += "\x00" + strings.ToLower(ev.Kind+"\x00"+ev.Ref+"\x00"+ev.Detail)
		}
		for _, w := range banned {
			if strings.Contains(blob, w) {
				t.Errorf("CHARTER BREACH: candidate surfaces causal word %q (a call is co-occurrence/structure, never a proof of cause): %+v", w, c)
			}
		}
	}

	// The arrow direction is OBSERVED parent→child, fixed by structure — a slow/error
	// callee does NOT invert it. paymentservice errored + was slow; it must remain the
	// CALLEE of checkout, never re-pointed as a "cause" pointing at checkout.
	pay, ok := edge(g, "checkout", "paymentservice")
	if !ok {
		t.Fatal("expected observed edge checkout→paymentservice")
	}
	if pay.Errors != 1 {
		t.Errorf("paymentservice error not counted as a MEASURED error count: %+v", pay)
	}
	if _, inverted := edge(g, "paymentservice", "checkout"); inverted {
		t.Error("CHARTER BREACH: the arrow inverted — a slow/error callee was turned into a cause pointing back at its caller")
	}
}

// TestNoInventedBarOrThreshold guards borrowed normativity for the lane: latency
// is MEASURED (p50/p95/max) but NEVER judged against a data-fit cutoff. No
// candidate may carry a "slow"/"threshold"/"slo"/"breach" verdict field — where no
// DECLARED SLO exists, the latency is surfaced unbounded, never invented.
func TestNoInventedBarOrThreshold(t *testing.T) {
	g := BuildCallGraph(richSpans())
	cands := ProposeTopology(g, testResolver, "cl", "vtest")
	bannedKeys := []string{"slow", "threshold", "slo", "breach", "violated", "exceeds", "bar", "verdict", "severity"}
	for _, c := range cands {
		for k := range c.Payload {
			lk := strings.ToLower(k)
			for _, bad := range bannedKeys {
				if strings.Contains(lk, bad) {
					t.Errorf("CHARTER BREACH: payload key %q invents a judgment (no data-fit cutoff; latency is unbounded unless a DECLARED SLO resolves): %+v", k, c.Payload)
				}
			}
		}
		// p95 is present and MEASURED (a raw float), not a boolean verdict.
		if _, ok := c.Payload["p95Millis"].(float64); !ok {
			t.Errorf("p95Millis should be a measured float latency, not a judgment: %#v", c.Payload["p95Millis"])
		}
	}
}

// TestOrphanAndPartialSpanHonesty is the dropped/partial-span gate. Spans are
// SAMPLED; a child whose parent is absent is an ORPHAN (caller unknowable) and
// must be COUNTED, never silently dropped and never guessed into an edge. The
// census fields (SpansObserved, Traces, OrphanSpans) must be exact, and every
// candidate must cite the census-incomplete nature of the source.
func TestOrphanAndPartialSpanHonesty(t *testing.T) {
	spans := richSpans()
	g := BuildCallGraph(spans)

	if g.SpansObserved != len(spans) {
		t.Errorf("SpansObserved = %d, want %d (every parsed span counted)", g.SpansObserved, len(spans))
	}
	if g.Traces != 3 {
		t.Errorf("Traces = %d, want 3 (t1, t2, t3)", g.Traces)
	}
	// Exactly one orphan: t1-orphan's parent t1-gone was not sampled.
	if g.OrphanSpans != 1 {
		t.Fatalf("OrphanSpans = %d, want 1 (emailservice child whose parent was not sampled)", g.OrphanSpans)
	}
	// The orphan must NOT have been guessed into ANY edge (no callee=emailservice
	// edge with a fabricated caller).
	for _, e := range g.Edges {
		if e.Callee == "emailservice" || e.Caller == "emailservice" {
			t.Errorf("CHARTER BREACH: an orphan span was guessed into an edge (caller unknowable): %+v", e)
		}
	}

	// Dropping the sampled parent of real calls turns each child into an orphan: the
	// orphan count must RISE and the edges must DISAPPEAR (never re-attributed). Every
	// trace carries a checkout parent span (t1-co, t2-co, and the t3 "dup-parent"
	// collision whose winner is checkout), so drop them all to remove every checkout→*
	// call — including the t3 call that only existed via the collision winner.
	pruned := make([]Span, 0, len(spans))
	for _, s := range spans {
		if s.SpanID == "t1-co" || s.SpanID == "t2-co" || s.SpanID == "dup-parent" { // remove checkout's span in every trace
			continue
		}
		pruned = append(pruned, s)
	}
	gp := BuildCallGraph(pruned)
	if gp.OrphanSpans <= g.OrphanSpans {
		t.Errorf("dropping a parent must INCREASE orphan count: before=%d after=%d", g.OrphanSpans, gp.OrphanSpans)
	}
	for _, e := range gp.Edges {
		if e.Caller == "checkout" {
			t.Errorf("CHARTER BREACH: checkout→* survived after checkout's spans were dropped — orphan children were re-attributed instead of counted: %+v", e)
		}
	}

	// Every candidate cites the census/sampled-incomplete honesty.
	for _, c := range ProposeTopology(g, testResolver, "cl", "vtest") {
		hasCensus := false
		for _, e := range c.Evidence {
			if e.Kind == "census" && strings.Contains(strings.ToLower(e.Detail), "partial") {
				hasCensus = true
			}
		}
		if !hasCensus {
			t.Errorf("candidate omits the census-incomplete (partial) honesty evidence: %+v", c.Evidence)
		}
	}
}

// TestUnresolvedServiceNeverGuessesCEI: a service the resolver doesn't know must
// surface resolved=false with an EMPTY cei — never a fabricated identity. This is
// the honest-partial-coverage discipline at the identity join.
func TestUnresolvedServiceNeverGuessesCEI(t *testing.T) {
	g := BuildCallGraph(richSpans())
	cands := ProposeTopology(g, testResolver, "cl", "vtest")
	for _, c := range cands {
		// paymentservice + frontend + emailservice are unknown to testResolver.
		if c.Payload["callee"] == "paymentservice" {
			if c.Payload["calleeResolved"] != false || c.Payload["calleeCei"] != "" {
				t.Errorf("CHARTER BREACH: unknown service got a guessed CEI: %+v", c.Payload)
			}
		}
		if c.Payload["caller"] == "frontend" {
			if c.Payload["callerResolved"] != false || c.Payload["callerCei"] != "" {
				t.Errorf("CHARTER BREACH: unknown caller got a guessed CEI: %+v", c.Payload)
			}
		}
	}
}
