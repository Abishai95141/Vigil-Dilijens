package unexplained

import (
	"strings"
	"testing"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/observe"
)

// This file rigorously pins the loudness DEFINITION at its boundary (doc 08 §3.1,
// §3.2). Loudness is the entire gate that decides whether activity may even reach
// the unexplained channel; getting the threshold off by one rung either floods the
// channel with calm signals or silently drops a real excursion. The two-clause
// definition is exact — a bar crossing OR a rate excursion, nothing else — and the
// channel must state its own residual blind spot plainly.
//
// Names here are deliberately distinct from unexplained_test.go (bdyKey, bdyT0,
// barFP, rateComponent, …) so the two files compose without collision.

const bdyKey = "i|cl|shop|Pod|loud-edge|uid-edge"

// bdyAt is the canonical loudness boundary clock — fixed, never wall-clock.
var bdyAt = t0 // reuse the package test epoch; no time.Now anywhere.

// barFP builds a fingerprint whose single thresholded variable sits at `state`.
// Direction/Stale are explicit so the boundary cases are unambiguous.
func barFP(metric string, state observe.ThresholdState, stale bool) observe.Fingerprint {
	return observe.Fingerprint{
		CEIKey: bdyKey, Namespace: "shop", Name: "loud-edge", Kind: "Container", EvaluatedAt: bdyAt,
		Thresholds: []observe.VariableThreshold{{
			RuleID: "RB", Metric: metric, State: state, BarSource: "config",
			Stale: stale, Deriv: observe.DerivationRef{StreamID: "sb", SampleAt: bdyAt},
		}},
	}
}

// rateComponent builds a single rate-guard fingerprint component at the chosen
// breached/inconclusive/stale combination — the three flags that bound the second
// loudness clause.
func rateComponent(metric string, breached, inconclusive, stale bool) observe.VariableRate {
	return observe.VariableRate{
		RuleID: "RR", Metric: metric, Breached: breached, Inconclusive: inconclusive,
		Stale: stale, BarSource: "config", Deriv: observe.DerivationRef{StreamID: "sr", SampleAt: bdyAt},
	}
}

// TestLoudnessBoundaryThreshold walks the bar-crossing clause across the ladder
// boundary FROM BOTH SIDES. The loudness threshold sits exactly between
// `at-threshold` (the approach rung, NOT loud) and `above` (the first violating
// rung, loud). The pair `at-threshold` / `above` are adjacent on the ladder, so
// this is the tightest possible boundary probe: a regression that moves the gate
// one rung in either direction flips exactly one of these.
func TestLoudnessBoundaryThreshold(t *testing.T) {
	// Quiet side of the boundary: must NOT route to the unexplained channel.
	quiet := []struct {
		name  string
		state observe.ThresholdState
		stale bool
	}{
		{"below is calm", observe.StateBelow, false},
		{"at-threshold is the approach rung, still quiet", observe.StateAtThreshold, false},
		{"unknown is no usable sample, not loud", observe.StateUnknown, false},
		// A stale crossing is NOT a confident excursion: same rung as the loud case
		// but suppressed because the newest sample aged out (doc 07). This is the
		// boundary in the staleness dimension, loud side held constant.
		{"stale above is suppressed", observe.StateAbove, true},
		{"stale well-above is suppressed", observe.StateWellAbove, true},
	}
	tr := NewTracker("v")
	for _, q := range quiet {
		t.Run("quiet/"+q.name, func(t *testing.T) {
			if states := Loud(barFP("m", q.state, q.stale)); len(states) != 0 {
				t.Fatalf("%s must not be loud: %+v", q.name, states)
			}
			if out := tr.Route(bdyAt, []observe.Fingerprint{barFP("m", q.state, q.stale)}, nil); len(out) != 0 {
				t.Fatalf("%s must NOT route to the unexplained channel: %+v", q.name, out)
			}
		})
	}

	// Loud side of the boundary: the immediate next rung up (`above`) and the deep
	// rung (`well-above`) both route, each carrying a bar-crossing classification.
	loud := []struct {
		name  string
		state observe.ThresholdState
		rung  string
	}{
		{"above is the first violating rung", observe.StateAbove, "above"},
		{"well-above is deep violation", observe.StateWellAbove, "well-above"},
	}
	for _, l := range loud {
		t.Run("loud/"+l.name, func(t *testing.T) {
			states := Loud(barFP("m", l.state, false))
			if len(states) != 1 {
				t.Fatalf("%s must be loud (exactly one state): %+v", l.name, states)
			}
			if states[0].Kind != LoudBarCrossing {
				t.Errorf("a crossing must classify as %q, got %q", LoudBarCrossing, states[0].Kind)
			}
			if states[0].State != l.rung {
				t.Errorf("loud state must carry its ladder rung %q, got %q", l.rung, states[0].State)
			}
			fresh := NewTracker("v")
			out := fresh.Route(bdyAt, []observe.Fingerprint{barFP("edge_metric", l.state, false)}, nil)
			if len(out) != 1 || out[0].Status != StatusNew {
				t.Fatalf("%s must route as one NEW unexplained card: %+v", l.name, out)
			}
			if out[0].Mark != Mark {
				t.Errorf("routed card must carry the not-yet-explained mark: %q", out[0].Mark)
			}
		})
	}
}

// TestLoudnessBoundaryRateExcursion walks the rate-guard clause across its
// boundary from both sides. A cleanly breached guard is loud; a guard that is not
// breached, OR breached-but-inconclusive (a scrape gap left the window delta
// untrustworthy), OR stale, is NOT loud. Inconclusive/stale are the silent-failure
// guards: they must never collapse into a "quiet, not breached" reading — the
// boundary holds the breached flag TRUE while the suppressing flag flips.
func TestLoudnessBoundaryRateExcursion(t *testing.T) {
	mk := func(c observe.VariableRate) observe.Fingerprint {
		return observe.Fingerprint{
			CEIKey: bdyKey, Namespace: "shop", Name: "loud-edge", Kind: "Container",
			EvaluatedAt: bdyAt, Rates: []observe.VariableRate{c},
		}
	}

	// Loud side: a confident, fresh breach.
	loud := mk(rateComponent("restarts", true, false, false))
	states := Loud(loud)
	if len(states) != 1 || states[0].Kind != LoudRateExcursion || states[0].State != "breached" {
		t.Fatalf("a clean rate breach must be one loud excursion: %+v", states)
	}
	tr := NewTracker("v")
	if out := tr.Route(bdyAt, []observe.Fingerprint{loud}, nil); len(out) != 1 || out[0].Status != StatusNew {
		t.Fatalf("a clean rate breach must route as one NEW card: %+v", out)
	}

	// Quiet side: each suppressing condition, breached held TRUE where applicable so
	// the ONLY difference from the loud case is the boundary flag under test.
	quiet := []struct {
		name string
		comp observe.VariableRate
	}{
		{"not breached", rateComponent("restarts", false, false, false)},
		{"breached but inconclusive (gap — not a confident excursion)", rateComponent("restarts", true, true, false)},
		{"breached but stale (sample aged out)", rateComponent("restarts", true, false, true)},
	}
	for _, q := range quiet {
		t.Run("quiet/"+q.name, func(t *testing.T) {
			if s := Loud(mk(q.comp)); len(s) != 0 {
				t.Fatalf("%s must not be loud: %+v", q.name, s)
			}
			fresh := NewTracker("v")
			if out := fresh.Route(bdyAt, []observe.Fingerprint{mk(q.comp)}, nil); len(out) != 0 {
				t.Fatalf("%s must NOT route to the unexplained channel: %+v", q.name, out)
			}
		})
	}
}

// TestBothClausesRouteTogether is the headline requirement: an entity that is loud
// via BOTH clauses at once — a bar crossing AND a rate excursion — routes to the
// unexplained channel carrying BOTH loud states, each classified by its own clause.
// Neither clause is fused into the other; they are presented adjacently and
// labelled (the join, never the fusion). A quiet/stationary entity in the same
// window does not route at all.
func TestBothClausesRouteTogether(t *testing.T) {
	loudBoth := observe.Fingerprint{
		CEIKey: bdyKey, Namespace: "shop", Name: "loud-edge", Kind: "Container", EvaluatedAt: bdyAt,
		Thresholds: []observe.VariableThreshold{{
			RuleID: "RB", Metric: "working_set_bytes", State: observe.StateAbove, BarSource: "config",
			Deriv: observe.DerivationRef{SampleAt: bdyAt},
		}},
		Rates: []observe.VariableRate{rateComponent("restart_total", true, false, false)},
	}
	// A second, stationary entity in the SAME window: at-threshold (approach) plus a
	// not-breached guard — calm on both clauses, must never route.
	stationary := observe.Fingerprint{
		CEIKey: "i|cl|shop|Pod|calm|uid-calm", Namespace: "shop", Name: "calm", Kind: "Container",
		EvaluatedAt: bdyAt,
		Thresholds: []observe.VariableThreshold{{
			RuleID: "RC", Metric: "working_set_bytes", State: observe.StateAtThreshold, BarSource: "config",
			Deriv: observe.DerivationRef{SampleAt: bdyAt},
		}},
		Rates: []observe.VariableRate{rateComponent("restart_total", false, false, false)},
	}

	states := Loud(loudBoth)
	if len(states) != 2 {
		t.Fatalf("both clauses must fire: want 2 loud states, got %+v", states)
	}
	// Both classifications present and distinct — the two clauses are not collapsed.
	kinds := map[LoudKind]bool{}
	for _, s := range states {
		kinds[s.Kind] = true
	}
	if !kinds[LoudBarCrossing] || !kinds[LoudRateExcursion] {
		t.Fatalf("the two loud states must keep their separate clause labels: %+v", states)
	}

	tr := NewTracker("v")
	out := tr.Route(bdyAt, []observe.Fingerprint{loudBoth, stationary}, nil)
	if len(out) != 1 {
		t.Fatalf("exactly one entity (the loud one) must route; the stationary one must not: %+v", out)
	}
	got := out[0]
	if got.Scope != bdyKey {
		t.Fatalf("the routed card must be the loud entity, not the stationary one: %q", got.Scope)
	}
	if len(got.LoudStates) != 2 {
		t.Fatalf("the routed card must carry BOTH loud states (crossing + excursion): %+v", got.LoudStates)
	}
	var sawCrossing, sawExcursion bool
	for _, s := range got.LoudStates {
		switch s.Kind {
		case LoudBarCrossing:
			sawCrossing = true
		case LoudRateExcursion:
			sawExcursion = true
		}
	}
	if !sawCrossing || !sawExcursion {
		t.Errorf("the routed card must surface both clauses, labelled separately: %+v", got.LoudStates)
	}
}

// TestBlindSpotStatedNotHidden pins doc 08 §3.2: the channel states its OWN
// residual blind spot — a signal carrying neither a resolved bar nor a rate guard
// can never be loud, so a novel failure expressing itself only through
// un-thresholded, un-guarded signals is invisible even here. The residual must be
// stated honestly, never implied away.
func TestBlindSpotStatedNotHidden(t *testing.T) {
	// 1) The notice EXISTS and is non-empty — a hidden blind spot is no notice at all.
	if strings.TrimSpace(BlindSpotNotice) == "" {
		t.Fatal("the residual blind spot must be a stated, non-empty notice")
	}
	// 2) It names the actual mechanism of the gap, so it cannot be a vacuous platitude:
	//    loudness requires a resolved bar or an authored rate guard.
	low := strings.ToLower(BlindSpotNotice)
	for _, must := range []string{"blind spot", "bar", "rate guard", "never be loud"} {
		if !strings.Contains(low, must) {
			t.Errorf("blind-spot notice must name the gap mechanism; missing %q in: %q", must, BlindSpotNotice)
		}
	}
	// 3) It must frame the limit honestly: known signals, unknown patterns — NOT a
	//    claim of total coverage.
	if !strings.Contains(low, "known signals") || !strings.Contains(low, "unknown") {
		t.Errorf("blind-spot notice must frame the honest scope (known signals / unknown patterns): %q", BlindSpotNotice)
	}

	// 4) The mechanism the notice DESCRIBES is real: a signal with no resolved bar
	//    AND no rate guard produces an empty fingerprint here — genuinely invisible,
	//    exactly as stated. A fingerprint carrying only Unknown / non-crossing
	//    components yields zero loud states and routes nothing.
	invisible := observe.Fingerprint{
		CEIKey: bdyKey, Namespace: "shop", Name: "loud-edge", Kind: "Container", EvaluatedAt: bdyAt,
		Thresholds: []observe.VariableThreshold{{
			RuleID: "RU", Metric: "unthresholded_signal", State: observe.StateUnknown, BarSource: "config",
			Deriv: observe.DerivationRef{SampleAt: bdyAt},
		}},
	}
	if states := Loud(invisible); len(states) != 0 {
		t.Fatalf("a signal with no resolved bar/guard must yield zero loud states (the stated blind spot): %+v", states)
	}
	tr := NewTracker("v")
	if out := tr.Route(bdyAt, []observe.Fingerprint{invisible}, nil); len(out) != 0 {
		t.Fatalf("the blind-spot case must route nothing — and this is stated, not hidden: %+v", out)
	}
}
