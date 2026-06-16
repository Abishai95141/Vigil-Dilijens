package flow

import (
	"strings"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
)

// Capability D test harness (doc 15 §4.D): the multi-hop PROJECTED cascade. One forecast
// root; every downstream node inherits the root's band WIDENED per hop. Network-free,
// deterministic. The load-bearing assertion is the producer invariant: the band never
// narrows downstream (earliest_child ≤ earliest_parent, latest_child ≥ latest_parent).

// projRoot builds a forecast root with a band [now+lo, now+hi] minutes.
func projRoot(ns, name, metric string, now time.Time, loMin, hiMin int) ProjectedDegradedWorkload {
	return ProjectedDegradedWorkload{
		CEI: roleCEId(ns, name), Label: ns + "/" + name, Metric: metric,
		Confidence: "moderate",
		CrossAt:    now.Add(time.Duration((loMin+hiMin)/2) * time.Minute),
		EarliestAt: now.Add(time.Duration(loMin) * time.Minute),
		LatestAt:   now.Add(time.Duration(hiMin) * time.Minute),
	}
}

// mustParseRFC parses an RFC3339 (date-bearing) band edge — the comparable form the band
// carries, so ordering checks are chronological even across the UTC midnight boundary.
func mustParseRFC(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("bad band edge %q: %v", s, err)
	}
	return parsed
}

func TestProjectedTransitiveTwoHopWidens(t *testing.T) {
	at := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	edges := newFlowStore(at)
	// flow: front -> mid -> back (call edges). impact propagates back -> mid -> front.
	edges.Assert(EdgeTypeFlow, roleCEId("traffic", "mid"), roleCEId("traffic", "back"), at)
	edges.Assert(EdgeTypeFlow, roleCEId("traffic", "front"), roleCEId("traffic", "mid"), at)
	w := identity.TimeWindow{Start: at, End: at}

	// back is the forecast root, band [+30, +50] min (width 20, half 10).
	roots := []ProjectedDegradedWorkload{projRoot("traffic", "back", "working_set", at, 30, 50)}
	chains := ProjectedTransitiveChains(edges, roots, transRel(), w, at, 0)
	if len(chains) != 1 {
		t.Fatalf("want 1 projected chain, got %d", len(chains))
	}
	c := chains[0]
	if c.MostUpstreamDegradedNode != "traffic/back" {
		t.Fatalf("root=%q, want traffic/back", c.MostUpstreamDegradedNode)
	}
	if len(c.Path) != 2 {
		t.Fatalf("want 2 hops, got %d: %+v", len(c.Path), c.Path)
	}
	// every step carries a PROJECTED band; the edge stays MEASURED, the why AUTHORED.
	for i, s := range c.Path {
		if s.Band == nil || s.Band.Class != "PROJECTED" {
			t.Fatalf("step %d missing PROJECTED band: %+v", i, s)
		}
		if s.EdgeClass != "MEASURED observed flow" || s.WhyClass != "AUTHORED" {
			t.Errorf("step %d class labels wrong: edge=%q why=%q", i, s.EdgeClass, s.WhyClass)
		}
		if s.Band.RootMetric != "working_set" {
			t.Errorf("step %d band root metric=%q, want working_set", i, s.Band.RootMetric)
		}
	}
	// hop1 = back->mid, hop2 = mid->front.
	if c.Path[0].Downstream != "traffic/mid" || c.Path[1].Downstream != "traffic/front" {
		t.Fatalf("path order wrong: %s then %s", c.Path[0].Downstream, c.Path[1].Downstream)
	}
	// THE PRODUCER INVARIANT: band widens (never narrows) hop1 -> hop2.
	h1, h2 := c.Path[0].Band, c.Path[1].Band
	h1e, h1l := mustParseRFC(t, h1.Earliest), mustParseRFC(t, h1.Latest)
	h2e, h2l := mustParseRFC(t, h2.Earliest), mustParseRFC(t, h2.Latest)
	if h2e.After(h1e) {
		t.Errorf("BAND NARROWED (earliest): hop2 %s > hop1 %s", h2.Earliest, h1.Earliest)
	}
	if h2l.Before(h1l) {
		t.Errorf("BAND NARROWED (latest): hop2 %s < hop1 %s", h2.Latest, h1.Latest)
	}
	// hop1 must itself widen relative to the root band [+30,+50].
	root := roots[0]
	if !h1e.Before(root.EarliestAt) || !h1l.After(root.LatestAt) {
		t.Errorf("hop1 band did not widen vs root: hop1 [%s,%s] root [+30,+50]", h1.Earliest, h1.Latest)
	}
	if h1.HopsFromRoot != 1 || h2.HopsFromRoot != 2 {
		t.Errorf("hop distances wrong: %d, %d", h1.HopsFromRoot, h2.HopsFromRoot)
	}
	// Charter: no causal token in the SYSTEM-GENERATED scaffolding (authored why excluded).
	b, _ := ScaffoldingForCharter(c).JSON()
	if tok, bad := HasForbiddenToken(string(b)); bad {
		t.Errorf("forbidden token %q in projected chain scaffolding:\n%s", tok, b)
	}
}

// Earliest clamps to `now` — the impact cannot precede now, even after deep widening.
func TestProjectedTransitiveEarliestClampedToNow(t *testing.T) {
	at := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	edges := newFlowStore(at)
	// a 3-deep chain so the widening would push earliest before now without the clamp.
	edges.Assert(EdgeTypeFlow, roleCEId("t", "n2"), roleCEId("t", "n1"), at)
	edges.Assert(EdgeTypeFlow, roleCEId("t", "n3"), roleCEId("t", "n2"), at)
	w := identity.TimeWindow{Start: at, End: at}
	// root band [+4, +8] min (half 2) — by hop 3 earliest would be +4-6 = -2 min (before now).
	roots := []ProjectedDegradedWorkload{projRoot("t", "n1", "m", at, 4, 8)}
	chains := ProjectedTransitiveChains(edges, roots, transRel(), w, at, 0)
	if len(chains) != 1 {
		t.Fatalf("want 1 chain, got %d", len(chains))
	}
	for _, s := range chains[0].Path {
		e := mustParseRFC(t, s.Band.Earliest)
		if e.Before(at) {
			t.Errorf("band earliest %s is BEFORE now — impact cannot precede now (clamp failed)", s.Band.Earliest)
		}
	}
}

// MIDNIGHT-WRAP regression: a forecast band that straddles 00:00 UTC must still produce a
// chain with WIDENING bands — the comparison is on the date-bearing RFC3339 edges, never
// the HH:MMZ render (which would misorder "00:02" < "23:58" and silently drop the chain).
func TestProjectedTransitiveMidnightWrap(t *testing.T) {
	at := time.Date(2026, 6, 16, 23, 40, 0, 0, time.UTC) // 20 min before midnight UTC
	edges := newFlowStore(at)
	edges.Assert(EdgeTypeFlow, roleCEId("t", "mid"), roleCEId("t", "back"), at)
	edges.Assert(EdgeTypeFlow, roleCEId("t", "front"), roleCEId("t", "mid"), at)
	w := identity.TimeWindow{Start: at, End: at}
	// root band [+8, +14] min ⇒ [23:48Z, 23:54Z]; widening pushes the far edge PAST midnight.
	roots := []ProjectedDegradedWorkload{projRoot("t", "back", "working_set", at, 8, 14)}
	chains := ProjectedTransitiveChains(edges, roots, transRel(), w, at, 0)
	if len(chains) != 1 || len(chains[0].Path) != 2 {
		t.Fatalf("midnight-straddling band must NOT drop the chain, got %d chains", len(chains))
	}
	// the band must still widen across the boundary (chronologically, not lexically by HH:MM).
	h1, h2 := chains[0].Path[0].Band, chains[0].Path[1].Band
	if !mustParseRFC(t, h2.Latest).After(mustParseRFC(t, h1.Latest)) {
		t.Errorf("band did not widen across midnight: hop1 latest %s, hop2 latest %s", h1.Latest, h2.Latest)
	}
	if !mustParseRFC(t, h1.Latest).After(at) { // hop1's far edge is after now (crosses midnight)
		t.Errorf("expected hop1 far edge past now, got %s", h1.Latest)
	}
}

// A zero-width (over-confident) forecast root must NOT collapse the band to a line — doc 01
// forbids a band that collapses. The producer surfaces the far edge OPEN instead.
func TestProjectedTransitiveZeroWidthRootNeverCollapses(t *testing.T) {
	at := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	edges := newFlowStore(at)
	edges.Assert(EdgeTypeFlow, roleCEId("t", "caller"), roleCEId("t", "root"), at)
	w := identity.TimeWindow{Start: at, End: at}
	roots := []ProjectedDegradedWorkload{projRoot("t", "root", "m", at, 30, 30)} // EarliestAt == LatestAt
	chains := ProjectedTransitiveChains(edges, roots, transRel(), w, at, 0)
	if len(chains) != 1 || len(chains[0].Path) != 1 {
		t.Fatalf("want 1 chain 1 hop, got %d", len(chains))
	}
	b := chains[0].Path[0].Band
	if !b.Open || b.Latest != "" {
		t.Errorf("a zero-width root must surface an OPEN band (never a line), got open=%v latest=%q", b.Open, b.Latest)
	}
	if chains[0].RootBand == nil || !chains[0].RootBand.Open {
		t.Errorf("the root band of a zero-width forecast must also be open, got %+v", chains[0].RootBand)
	}
}

// A root whose far edge is open (crossing may exceed the horizon) propagates an open band.
func TestProjectedTransitiveOpenBandPropagates(t *testing.T) {
	at := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	edges := newFlowStore(at)
	edges.Assert(EdgeTypeFlow, roleCEId("t", "caller"), roleCEId("t", "root"), at)
	w := identity.TimeWindow{Start: at, End: at}
	root := projRoot("t", "root", "m", at, 30, 50)
	root.LatestBeyondHorizon = true
	chains := ProjectedTransitiveChains(edges, []ProjectedDegradedWorkload{root}, transRel(), w, at, 0)
	if len(chains) != 1 || len(chains[0].Path) != 1 {
		t.Fatalf("want 1 chain 1 hop, got %d", len(chains))
	}
	b := chains[0].Path[0].Band
	if !b.Open || b.Latest != "" {
		t.Errorf("open root must propagate an open band (latest empty), got open=%v latest=%q", b.Open, b.Latest)
	}
}

// One forecast root per chain: two independently-warned roots → two chains.
func TestProjectedTransitiveOneRootPerChain(t *testing.T) {
	at := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	edges := newFlowStore(at)
	edges.Assert(EdgeTypeFlow, roleCEId("a", "ca"), roleCEId("a", "ra"), at)
	edges.Assert(EdgeTypeFlow, roleCEId("b", "cb"), roleCEId("b", "rb"), at)
	w := identity.TimeWindow{Start: at, End: at}
	roots := []ProjectedDegradedWorkload{projRoot("a", "ra", "m", at, 10, 20), projRoot("b", "rb", "m", at, 10, 20)}
	chains := ProjectedTransitiveChains(edges, roots, transRel(), w, at, 0)
	if len(chains) != 2 {
		t.Fatalf("two warned roots → two chains, got %d", len(chains))
	}
	if chains[0].MostUpstreamDegradedNode != "a/ra" || chains[1].MostUpstreamDegradedNode != "b/rb" {
		t.Errorf("roots wrong/unsorted: %s, %s", chains[0].MostUpstreamDegradedNode, chains[1].MostUpstreamDegradedNode)
	}
}

// A lone forecast root with no caller is a warning, not a cascade — no chain.
func TestProjectedTransitiveNoCallerNoChain(t *testing.T) {
	at := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	edges := newFlowStore(at)
	w := identity.TimeWindow{Start: at, End: at}
	roots := []ProjectedDegradedWorkload{projRoot("t", "lonely", "m", at, 10, 20)}
	if chains := ProjectedTransitiveChains(edges, roots, transRel(), w, at, 0); chains != nil {
		t.Fatalf("a root with no caller → no cascade, got %d", len(chains))
	}
}

// No authored relation ⇒ no chain.
func TestProjectedTransitiveNoRelationNoChain(t *testing.T) {
	at := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	edges := newFlowStore(at)
	edges.Assert(EdgeTypeFlow, roleCEId("t", "c"), roleCEId("t", "r"), at)
	w := identity.TimeWindow{Start: at, End: at}
	roots := []ProjectedDegradedWorkload{projRoot("t", "r", "m", at, 10, 20)}
	if chains := ProjectedTransitiveChains(edges, roots, Relation{}, w, at, 0); chains != nil {
		t.Fatalf("no relation → no chain, got %d", len(chains))
	}
}

// maxHops bounds the projected walk and states the ceiling.
func TestProjectedTransitiveMaxHops(t *testing.T) {
	at := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	edges := newFlowStore(at)
	edges.Assert(EdgeTypeFlow, roleCEId("t", "n2"), roleCEId("t", "n1"), at)
	edges.Assert(EdgeTypeFlow, roleCEId("t", "n3"), roleCEId("t", "n2"), at)
	edges.Assert(EdgeTypeFlow, roleCEId("t", "n4"), roleCEId("t", "n3"), at)
	w := identity.TimeWindow{Start: at, End: at}
	roots := []ProjectedDegradedWorkload{projRoot("t", "n1", "m", at, 30, 50)}
	chains := ProjectedTransitiveChains(edges, roots, transRel(), w, at, 2)
	if len(chains) != 1 || len(chains[0].Path) != 2 {
		t.Fatalf("maxHops=2 must bound to 2 steps, got %d steps", len(chains[0].Path))
	}
	hasCeiling := false
	for _, g := range chains[0].Gaps {
		if strings.Contains(g.Reason, "hop ceiling") {
			hasCeiling = true
		}
	}
	if !hasCeiling {
		t.Errorf("bounded walk with further callers must state a hop-ceiling gap; gaps=%+v", chains[0].Gaps)
	}
}

// Determinism: identical inputs reproduce byte-identical chains.
func TestProjectedTransitiveDeterministic(t *testing.T) {
	at := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	build := func() string {
		edges := newFlowStore(at)
		edges.Assert(EdgeTypeFlow, roleCEId("t", "b"), roleCEId("t", "a"), at)
		edges.Assert(EdgeTypeFlow, roleCEId("t", "c"), roleCEId("t", "a"), at)
		edges.Assert(EdgeTypeFlow, roleCEId("t", "d"), roleCEId("t", "b"), at)
		w := identity.TimeWindow{Start: at, End: at}
		roots := []ProjectedDegradedWorkload{projRoot("t", "a", "m", at, 30, 50)}
		var sb strings.Builder
		for _, c := range ProjectedTransitiveChains(edges, roots, transRel(), w, at, 0) {
			b, _ := c.JSON()
			sb.Write(b)
		}
		return sb.String()
	}
	if build() != build() {
		t.Error("ProjectedTransitiveChains is non-deterministic")
	}
}
