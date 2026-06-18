package trace

import (
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/candidate"
)

func fixtureSpans(t *testing.T) []Span {
	t.Helper()
	b, err := os.ReadFile("testdata/spans_sample.jsonl")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return ParseSpans(strings.Split(strings.TrimRight(string(b), "\n"), "\n"))
}

// testResolver resolves checkout + currencyservice to role CEIs; others unresolved.
func testResolver(service string) (string, bool) {
	switch service {
	case "checkout":
		return "role:Deployment/checkout", true
	case "currencyservice":
		return "role:Deployment/currencyservice", true
	}
	return "", false
}

func edge(g CallGraph, caller, callee string) (CallEdge, bool) {
	for _, e := range g.Edges {
		if e.Caller == caller && e.Callee == callee {
			return e, true
		}
	}
	return CallEdge{}, false
}

func TestBuildCallGraphAggregatesObservedCalls(t *testing.T) {
	g := BuildCallGraph(fixtureSpans(t))

	// 3 cross-service edges; intra-service (checkout→checkout) is NOT an edge; the root
	// (frontend) and the orphan (emailservice, parent not sampled) produce no edge.
	if len(g.Edges) != 3 {
		t.Fatalf("edges = %d, want 3: %+v", len(g.Edges), g.Edges)
	}
	if g.OrphanSpans != 1 {
		t.Errorf("orphanSpans = %d, want 1 (the emailservice span whose parent was not sampled)", g.OrphanSpans)
	}

	cur, ok := edge(g, "checkout", "currencyservice")
	if !ok || cur.Calls != 2 || cur.Errors != 0 {
		t.Fatalf("checkout→currencyservice = %+v, want calls=2 errors=0", cur)
	}
	// durations 15ms and 20ms ⇒ p50 17.5, max 20.
	if !approx(cur.P50Millis, 17.5) || !approx(cur.MaxMillis, 20) {
		t.Errorf("currency latencies wrong: %+v", cur)
	}

	pay, ok := edge(g, "checkout", "paymentservice")
	if !ok || pay.Calls != 1 || pay.Errors != 1 {
		t.Errorf("checkout→paymentservice = %+v, want calls=1 errors=1 (the error span)", pay)
	}

	fe, ok := edge(g, "frontend", "checkout")
	if !ok || fe.Calls != 1 || !approx(fe.MaxMillis, 85) {
		t.Errorf("frontend→checkout = %+v, want calls=1 max≈85ms", fe)
	}

	// Cardinal: NO same-service edge ever (intra-service linkage is not a call).
	for _, e := range g.Edges {
		if e.Caller == e.Callee {
			t.Errorf("a same-service edge leaked through: %+v", e)
		}
	}
}

func TestProposeTopologyIsStructuralNeverCausal(t *testing.T) {
	g := BuildCallGraph(fixtureSpans(t))
	cands := ProposeTopology(g, testResolver, "cl", "vtest")
	if len(cands) != 3 {
		t.Fatalf("candidates = %d, want 3", len(cands))
	}
	for _, c := range cands {
		// CARDINAL: a discovered call is STRUCTURAL topology, never a causal edge/hypothesis.
		if c.Kind != candidate.KindEdge || c.Relation != "topology" {
			t.Fatalf("CHARTER BREACH: trace produced kind=%q relation=%q, want edge/topology (observed structure, never a cause): %+v",
				c.Kind, c.Relation, c)
		}
		if err := candidate.Validate(c); err != nil {
			t.Errorf("candidate failed the structural guard: %v (%+v)", err, c)
		}
		// census honesty: every candidate cites the sampled/partial nature of traces.
		hasCensus := false
		for _, e := range c.Evidence {
			if e.Kind == "census" {
				hasCensus = true
			}
		}
		if !hasCensus {
			t.Errorf("candidate missing the census-incomplete evidence: %+v", c.Evidence)
		}
	}
	// The checkout→currencyservice candidate must carry BOTH resolved CEIs.
	for _, c := range cands {
		if c.Subject == "trace-call:checkout->currencyservice" {
			if c.Payload["callerResolved"] != true || c.Payload["calleeResolved"] != true {
				t.Errorf("checkout→currency should resolve both endpoints to CEIs: %+v", c.Payload)
			}
		}
		if c.Subject == "trace-call:checkout->paymentservice" {
			if c.Payload["calleeResolved"] != false {
				t.Errorf("paymentservice is unknown to the store; calleeResolved must be false (no guessed CEI): %+v", c.Payload)
			}
		}
	}
}

// THE TRACE-GATE (doc 20 P4 TRACE): the call graph + the staged topology candidates are
// byte-identical across runs AND invariant to the order spans arrive in (aggregation +
// percentiles are computed over sorted data). And the cardinal charter rule: the lane
// emits ONLY structural topology edges — never a causal edge or hypothesis.
func TestTraceGateDeterministicAndCharterClean(t *testing.T) {
	spans := fixtureSpans(t)
	run := func(ss []Span) (CallGraph, []candidate.Candidate) {
		g := BuildCallGraph(ss)
		return g, ProposeTopology(g, testResolver, "cl", "vtest")
	}

	g1, c1 := run(spans)
	g2, c2 := run(spans)
	if !reflect.DeepEqual(g1, g2) || !reflect.DeepEqual(c1, c2) {
		t.Fatalf("trace core not deterministic across runs")
	}
	// reverse span order — outputs must be identical.
	rev := make([]Span, len(spans))
	for i := range spans {
		rev[i] = spans[len(spans)-1-i]
	}
	gR, cR := run(rev)
	if !reflect.DeepEqual(g1, gR) {
		t.Fatalf("call graph not invariant to span order:\n%+v\n%+v", g1, gR)
	}
	if !reflect.DeepEqual(c1, cR) {
		t.Fatalf("topology candidates not invariant to span order")
	}
	for _, c := range c1 {
		if c.Kind == candidate.KindCausalHypothesis {
			t.Fatalf("CHARTER BREACH: trace emitted a causal hypothesis (a call is structure, not a cause): %+v", c)
		}
	}
}

func TestProposeAndStage(t *testing.T) {
	st, err := candidate.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	g := BuildCallGraph(fixtureSpans(t))
	now := time.Date(2026, 6, 18, 12, 1, 0, 0, time.UTC)
	n, err := ProposeAndStage(st, now, g, testResolver, "cl", "vtest")
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("staged = %d, want 3", n)
	}
	rows, err := st.List(candidate.Filter{Kind: candidate.KindEdge})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("store has %d topology edge rows, want 3", len(rows))
	}
	for _, r := range rows {
		if r.Relation != "topology" || r.Lineage.Source != "trace" || r.Status != candidate.StatusCandidate {
			t.Errorf("staged row wrong: %+v", r)
		}
	}
	// idempotent re-stage (content-derived id).
	n2, _ := ProposeAndStage(st, now, g, testResolver, "cl", "vtest")
	rows2, _ := st.List(candidate.Filter{Kind: candidate.KindEdge})
	if n2 != 3 || len(rows2) != 3 {
		t.Errorf("re-stage should be idempotent: n2=%d rows=%d, want 3/3", n2, len(rows2))
	}
}

// A duplicate spanID carrying a DIFFERENT service (a merged/concatenated export) must
// dedup to ONE deterministic winner, so a child's resolved caller is invariant to span
// arrival order — not last-write-wins.
func TestBuildCallGraphDedupDeterministicOnSpanCollision(t *testing.T) {
	mk := func(id, parent, svc string) Span {
		return Span{TraceID: "t", SpanID: id, ParentSpanID: parent, Service: svc,
			StartTime: time.Unix(0, 0), EndTime: time.Unix(0, int64(10*time.Millisecond))}
	}
	pa := mk("p", "", "svcA")
	pb := mk("p", "", "svcB") // same spanID "p", different service
	child := mk("c", "p", "svcC")
	fwd := BuildCallGraph([]Span{pa, pb, child})
	rev := BuildCallGraph([]Span{child, pb, pa})
	if !reflect.DeepEqual(fwd, rev) {
		t.Fatalf("span-collision dedup is order-dependent (last-write-wins):\n%+v\n%+v", fwd, rev)
	}
	if len(fwd.Edges) != 1 || fwd.Edges[0].Caller != "svcA" { // svcA < svcB ⇒ deterministic winner
		t.Errorf("want 1 edge caller=svcA (deterministic min), got %+v", fwd.Edges)
	}
}

func TestParseSpansSkipsMalformed(t *testing.T) {
	spans := fixtureSpans(t)
	// 7 well-formed spans; the malformed trailing line is skipped.
	if len(spans) != 7 {
		t.Fatalf("parsed %d spans, want 7 (malformed line skipped): %+v", len(spans), spans)
	}
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-6 }
