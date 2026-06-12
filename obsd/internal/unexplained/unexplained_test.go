package unexplained

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/detect"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/observe"
)

var t0 = time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)

const (
	podKey  = "i|cl|shop|Pod|web-a|uid-a"
	nodeKey = "i|cl||Node|worker-1|nodeuid-1"
)

// crossedFP builds a fingerprint loud on one thresholded metric.
func crossedFP(key, ns, name, kind, metric string, state observe.ThresholdState, stale bool) observe.Fingerprint {
	return observe.Fingerprint{
		CEIKey: key, Namespace: ns, Name: name, Kind: kind, EvaluatedAt: t0,
		Thresholds: []observe.VariableThreshold{{
			RuleID: "R", Metric: metric, State: state, BarSource: "config", Stale: stale,
			Deriv: observe.DerivationRef{StreamID: "s", SampleAt: t0},
		}},
	}
}

// --- M1 loudness --------------------------------------------------------------

// The two-clause definition (doc 08 §3.1): above/well-above bar crossings and
// breached rate guards are loud; below/at-threshold/stale/inconclusive are not.
func TestLoudnessDefinition(t *testing.T) {
	cases := []struct {
		name string
		fp   observe.Fingerprint
		want int
	}{
		{"above", crossedFP(podKey, "shop", "web-a", "Container", "m", observe.StateAbove, false), 1},
		{"well-above", crossedFP(podKey, "shop", "web-a", "Container", "m", observe.StateWellAbove, false), 1},
		{"at-threshold not loud", crossedFP(podKey, "shop", "web-a", "Container", "m", observe.StateAtThreshold, false), 0},
		{"below not loud", crossedFP(podKey, "shop", "web-a", "Container", "m", observe.StateBelow, false), 0},
		{"stale crossing not loud", crossedFP(podKey, "shop", "web-a", "Container", "m", observe.StateWellAbove, true), 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := len(Loud(c.fp)); got != c.want {
				t.Errorf("Loud = %d states, want %d", got, c.want)
			}
		})
	}

	// A breached rate guard is loud; an inconclusive one is not.
	rateFP := func(breached, inconclusive, stale bool) observe.Fingerprint {
		return observe.Fingerprint{
			CEIKey: podKey, Kind: "Container", EvaluatedAt: t0,
			Rates: []observe.VariableRate{{
				RuleID: "RR", Metric: "restarts", Breached: breached, Inconclusive: inconclusive, Stale: stale,
				BarSource: "default", Flagged: true, Deriv: observe.DerivationRef{SampleAt: t0},
			}},
		}
	}
	if len(Loud(rateFP(true, false, false))) != 1 {
		t.Error("a breached rate guard must be loud")
	}
	if len(Loud(rateFP(true, true, false))) != 0 {
		t.Error("an inconclusive rate guard is not a confident excursion — not loud")
	}
	if got := Loud(rateFP(true, false, false)); got[0].Kind != LoudRateExcursion || !got[0].Flagged {
		t.Errorf("rate excursion must carry kind + flagged provenance: %+v", got)
	}
}

// --- M2 routing + dedup -------------------------------------------------------

// A loud entity with no covering match routes as NEW, then AGES on persistence
// (one card, not a stream of repeats — doc 08 §3.4), carrying the not-yet-
// explained mark and full MEASURED evidence.
func TestRouteNewThenAging(t *testing.T) {
	tr := NewTracker("sha256:test")
	fp := crossedFP(podKey, "shop", "web-a", "Container", "weird_metric", observe.StateAbove, false)

	out := tr.Route(t0, []observe.Fingerprint{fp}, nil)
	if len(out) != 1 || out[0].Status != StatusNew {
		t.Fatalf("first window must emit one NEW card: %+v", out)
	}
	f := out[0]
	if f.Scope != podKey || f.Mark != Mark || len(f.LoudStates) != 1 || f.Occurrences != 1 {
		t.Errorf("card shape wrong: %+v", f)
	}
	if f.GraphVersion != "sha256:test" {
		t.Error("card must pin the graph version")
	}

	// Same loudness next window: ONE aging card, FirstSeen preserved.
	out = tr.Route(t0.Add(15*time.Second), []observe.Fingerprint{fp}, nil)
	if len(out) != 1 || out[0].Status != StatusAging {
		t.Fatalf("persistence must age, not repeat: %+v", out)
	}
	if !out[0].FirstSeen.Equal(t0) || out[0].Occurrences != 2 {
		t.Errorf("aging card must keep FirstSeen and bump Occurrences: %+v", out[0])
	}
}

// Resolution retires the card (doc 08 §3.4): when the loud states drop below
// their bar, the card closes as RESOLVED, once.
func TestRouteResolves(t *testing.T) {
	tr := NewTracker("v")
	loud := crossedFP(podKey, "shop", "web-a", "Container", "m", observe.StateAbove, false)
	tr.Route(t0, []observe.Fingerprint{loud}, nil)

	calm := crossedFP(podKey, "shop", "web-a", "Container", "m", observe.StateBelow, false)
	out := tr.Route(t0.Add(15*time.Second), []observe.Fingerprint{calm}, nil)
	if len(out) != 1 || out[0].Status != StatusResolved {
		t.Fatalf("calmed loudness must resolve the card once: %+v", out)
	}
	// And it is gone — a third window emits nothing.
	if got := tr.Route(t0.Add(30*time.Second), []observe.Fingerprint{calm}, nil); len(got) != 0 {
		t.Errorf("a resolved card must not re-emit: %+v", got)
	}
}

// Supersede-on-match (doc 08 §3.4): the entity stays loud, but a phenomenon match
// now COVERS the loud states — the card supersedes with a link to the match (the
// unexplained became explained by authored knowledge, not inference).
func TestRouteSupersededByMatch(t *testing.T) {
	tr := NewTracker("v")
	metric := "container_memory_working_set_bytes"
	loud := crossedFP(podKey, "shop", "web-a", "Container", metric, observe.StateAbove, false)

	// Window 1: loud, no match → unexplained NEW.
	if out := tr.Route(t0, []observe.Fingerprint{loud}, nil); out[0].Status != StatusNew {
		t.Fatalf("expected NEW, got %+v", out)
	}

	// Window 2: still loud, but now a phenomenon match covers the metric.
	match := detect.Finding{
		Phenomenon: "PHEN_MEMORY_LEAK", EntityCEI: podKey,
		Members: []detect.MemberEvidence{{Metric: metric, Met: true}},
	}
	out := tr.Route(t0.Add(15*time.Second), []observe.Fingerprint{loud}, []detect.Finding{match})
	if len(out) != 1 || out[0].Status != StatusSuperseded {
		t.Fatalf("a covering match must supersede the card: %+v", out)
	}
	if out[0].SupersededBy != "PHEN_MEMORY_LEAK" {
		t.Errorf("supersede must link the covering phenomenon: %+v", out[0])
	}
}

// A loud state COVERED by a match from the first window never becomes an
// unexplained card at all (doc 08 §3.3 — only loud-AND-unmatched routes).
func TestCoveredLoudnessNeverRoutes(t *testing.T) {
	tr := NewTracker("v")
	metric := "container_memory_working_set_bytes"
	loud := crossedFP(podKey, "shop", "web-a", "Container", metric, observe.StateAbove, false)
	match := detect.Finding{Phenomenon: "PHEN_MEMORY_LEAK", EntityCEI: podKey,
		Members: []detect.MemberEvidence{{Metric: metric, Met: true}}}
	if out := tr.Route(t0, []observe.Fingerprint{loud}, []detect.Finding{match}); len(out) != 0 {
		t.Errorf("a covered loud state must not route to unexplained: %+v", out)
	}
}

// Coverage respects the SPAN: a node's loud PSI is covered by a container's
// first-order match that consumed it as a neighbour member (doc 08 §3.3).
func TestNeighbourCoverage(t *testing.T) {
	tr := NewTracker("v")
	psi := "node_pressure_cpu_waiting_seconds_total"
	loudNode := crossedFP(nodeKey, "", "worker-1", "Node", psi, observe.StateAbove, false)
	// A container's cascade match cites the node's PSI across a runs-on hop.
	match := detect.Finding{
		Phenomenon: "PHEN_THROTTLING_CASCADE", EntityCEI: podKey,
		Members: []detect.MemberEvidence{{Metric: psi, Met: true, Neighbour: nodeKey, Via: "runs-on"}},
	}
	if out := tr.Route(t0, []observe.Fingerprint{loudNode}, []detect.Finding{match}); len(out) != 0 {
		t.Errorf("neighbour-covered node loudness must not route: %+v", out)
	}
	// Without the match, the same node loudness DOES route.
	tr2 := NewTracker("v")
	if out := tr2.Route(t0, []observe.Fingerprint{loudNode}, nil); len(out) != 1 {
		t.Errorf("uncovered node loudness must route: %+v", out)
	}
}

// Partial coverage: an entity loud on TWO metrics where a match covers only one
// routes the OTHER as unexplained (doc 08 §3.3 — covering the loud STATES).
func TestPartialCoverageRoutesRemainder(t *testing.T) {
	tr := NewTracker("v")
	fp := observe.Fingerprint{
		CEIKey: podKey, Namespace: "shop", Name: "web-a", Kind: "Container", EvaluatedAt: t0,
		Thresholds: []observe.VariableThreshold{
			{RuleID: "R1", Metric: "explained_metric", State: observe.StateAbove, BarSource: "config", Deriv: observe.DerivationRef{SampleAt: t0}},
			{RuleID: "R2", Metric: "weird_metric", State: observe.StateWellAbove, BarSource: "config", Deriv: observe.DerivationRef{SampleAt: t0}},
		},
	}
	match := detect.Finding{Phenomenon: "PHEN_X", EntityCEI: podKey,
		Members: []detect.MemberEvidence{{Metric: "explained_metric", Met: true}}}
	out := tr.Route(t0, []observe.Fingerprint{fp}, []detect.Finding{match})
	if len(out) != 1 || len(out[0].LoudStates) != 1 || out[0].LoudStates[0].Metric != "weird_metric" {
		t.Fatalf("only the uncovered loud state must route: %+v", out)
	}
}

// Determinism (doc 08 / replay): same inputs + same tracker state ⇒ identical
// output, and a Reset reproduces a fresh tracker exactly.
func TestRouteDeterministicAndReset(t *testing.T) {
	mk := func() *Tracker { return NewTracker("v") }
	fp := crossedFP(podKey, "shop", "web-a", "Container", "m", observe.StateAbove, false)
	a := mk().Route(t0, []observe.Fingerprint{fp}, nil)
	b := mk().Route(t0, []observe.Fingerprint{fp}, nil)
	if !reflect.DeepEqual(a, b) {
		t.Error("Route is not deterministic")
	}
	tr := mk()
	tr.Route(t0, []observe.Fingerprint{fp}, nil)
	tr.Reset()
	// After reset the same input is NEW again (the open cards were emptied).
	if out := tr.Route(t0, []observe.Fingerprint{fp}, nil); out[0].Status != StatusNew {
		t.Errorf("after Reset the card must be NEW again: %+v", out)
	}
}

// --- M3 charter language audit ------------------------------------------------

// Charter (doc 01 / doc 08 §4): everything the channel emits is MEASURED and
// conspicuously NOT a reason. NO causal vocabulary may appear in any surfaced
// string — the absence of an explanation is the point.
func TestNoCausalVocabulary(t *testing.T) {
	banned := []string{
		"because", "caused", "causes", "causing", "due to", "leads to", "led to",
		"results in", "resulted in", "triggers", "triggered", "reason", "root cause",
		"explains", "explained by", "therefore", "responsible for", "blame",
	}
	tr := NewTracker("v")
	metric := "container_memory_working_set_bytes"
	loud := crossedFP(podKey, "shop", "web-a", "Container", metric, observe.StateAbove, false)
	tr.Route(t0, []observe.Fingerprint{loud}, nil)
	match := detect.Finding{Phenomenon: "PHEN_MEMORY_LEAK", EntityCEI: podKey,
		Members: []detect.MemberEvidence{{Metric: metric, Met: true}}}

	var strs []string
	collect := func(fs []Finding) {
		for _, f := range fs {
			strs = append(strs, f.Mark, f.MatchCheck, string(f.Status))
		}
	}
	collect(tr.Route(t0.Add(15*time.Second), []observe.Fingerprint{loud}, []detect.Finding{match})) // superseded
	tr.recordRecurrence(&card{scope: podKey, kind: "Container", metrics: []string{metric}, lastSeen: t0})
	for _, c := range tr.Candidates(1, 1) {
		strs = append(strs, c.Rationale)
	}
	strs = append(strs, Mark, BlindSpotNotice)

	for _, s := range strs {
		low := strings.ToLower(s)
		for _, w := range banned {
			if strings.Contains(low, w) {
				t.Errorf("CAUSAL VOCABULARY %q in surfaced string: %q", w, s)
			}
		}
	}
}

// --- M4 curation feedback -----------------------------------------------------

// Recurrence aggregates into a candidate-phenomenon report (doc 08 §3.6): the
// SAME signal set recurring across windows/entities is the graph's growth signal.
func TestCandidateReports(t *testing.T) {
	tr := NewTracker("v")
	// web-a loud on weird_metric for 3 windows; web-b once.
	a := crossedFP(podKey, "shop", "web-a", "Container", "weird_metric", observe.StateAbove, false)
	b := crossedFP("i|cl|shop|Pod|web-b|uid-b", "shop", "web-b", "Container", "weird_metric", observe.StateAbove, false)
	tr.Route(t0, []observe.Fingerprint{a}, nil)
	tr.Route(t0.Add(15*time.Second), []observe.Fingerprint{a}, nil)
	tr.Route(t0.Add(30*time.Second), []observe.Fingerprint{a, b}, nil)

	// Below threshold: no candidate.
	if got := tr.Candidates(100, 100); len(got) != 0 {
		t.Errorf("below recurrence thresholds, no candidate: %+v", got)
	}
	// Recurs across windows (web-a: 3) OR entities (2 distinct).
	got := tr.Candidates(3, 5)
	if len(got) != 1 {
		t.Fatalf("the recurring signature must surface one candidate: %+v", got)
	}
	c := got[0]
	if c.EntityKind != "Container" || len(c.Metrics) != 1 || c.Metrics[0] != "weird_metric" {
		t.Errorf("candidate signature wrong: %+v", c)
	}
	if len(c.Entities) != 2 || c.Windows < 3 {
		t.Errorf("candidate must aggregate distinct entities + windows: %+v", c)
	}
}
