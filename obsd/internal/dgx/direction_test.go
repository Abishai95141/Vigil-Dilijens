package dgx

import (
	"testing"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/candidate"
)

func TestNormalizeDirection(t *testing.T) {
	cases := map[string]struct {
		want string
		ok   bool
	}{
		"a-to-b": {"a-to-b", true}, "a->b": {"a-to-b", true}, "A→B": {"a-to-b", true}, "ab": {"a-to-b", true},
		"b-to-a": {"b-to-a", true}, "b->a": {"b-to-a", true},
		"sideways": {"", false}, "": {"", false}, "a-to-c": {"", false},
	}
	for in, exp := range cases {
		got, ok := normalizeDirection(in)
		if got != exp.want || ok != exp.ok {
			t.Errorf("normalizeDirection(%q) = (%q,%v), want (%q,%v)", in, got, ok, exp.want, exp.ok)
		}
	}
}

func TestWitnessAgrees(t *testing.T) {
	idx := map[string]Observation{
		"cohyp:agree":    {Ref: "cohyp:agree", Detail: "x ~ y r=0.9 onset-first=x Δ4s lead-lag +4s (AGREES with onset order)"},
		"cohyp:disagree": {Ref: "cohyp:disagree", Detail: "x ~ y lead-lag -2s (DISAGREES with onset order)"},
		"cohyp:nowit":    {Ref: "cohyp:nowit", Detail: "x ~ y no significant lead-lag witness"},
		"dep:foo":        {Ref: "dep:foo", Detail: "AGREES with onset order"}, // not a cohyp ref → must NOT count
	}
	if !witnessAgrees([]string{"cohyp:agree"}, idx) {
		t.Error("a cited cohyp whose lead-lag AGREES must pass the dual-witness gate")
	}
	if witnessAgrees([]string{"cohyp:disagree"}, idx) {
		t.Error("a cohyp whose lead-lag DISAGREES must NOT pass")
	}
	if witnessAgrees([]string{"cohyp:nowit"}, idx) {
		t.Error("a cohyp with no lead-lag witness must NOT pass (the gate needs BOTH witnesses)")
	}
	if witnessAgrees([]string{"dep:foo"}, idx) {
		t.Error("a non-cohyp ref must never satisfy the gate, even if its detail says AGREES")
	}
	if witnessAgrees([]string{"cohyp:missing"}, idx) {
		t.Error("an ungrounded ref must not pass")
	}
}

// TestDirectionGate exercises the end-to-end verify path: a directional causal_hypothesis is
// admitted as a PROJECTED Suggestion ONLY when the cited witness AGREES; otherwise the
// hypothesis is kept but stays direction-free (withheld), never fabricating an arrow.
func TestDirectionGate(t *testing.T) {
	a := New(NewStaticProvider("fake", ""), Params{MinEvidence: 1, MaxProposals: 10, MaxContextChars: 6000})
	idx := map[string]Observation{
		"cohyp:agree":    {Ref: "cohyp:agree", Detail: "lead-lag +4s (AGREES with onset order)"},
		"cohyp:disagree": {Ref: "cohyp:disagree", Detail: "lead-lag -2s (DISAGREES with onset order)"},
	}
	doc := proposalDoc{Proposals: []rawProposal{
		{Kind: "causal_hypothesis", Subject: "x ~ y", Relation: "co-occurrence", Direction: "a-to-b", Evidence: []string{"cohyp:agree"}, Rationale: "x leads y"},
		{Kind: "causal_hypothesis", Subject: "p ~ q", Relation: "co-occurrence", Direction: "a-to-b", Evidence: []string{"cohyp:disagree"}, Rationale: "guess"},
		// not-causal is admitted FREELY (a negative judgement never invents an edge) — here it
		// even cites the DISAGREEING witness, which is fine for not-causal.
		{Kind: "causal_hypothesis", Subject: "m ~ n", Relation: "co-occurrence", Direction: "not-causal", Evidence: []string{"cohyp:disagree"}, Rationale: "shared confounder"},
	}}
	var rep Report
	cands := a.verifyProposals(doc, Context{}, idx, &rep)
	if len(cands) != 3 {
		t.Fatalf("all hypotheses should survive as candidates, got %d", len(cands))
	}
	if cands[2].Suggestion == nil || cands[2].Suggestion.Direction != "not-causal" {
		t.Errorf("not-causal must be admitted freely (no witness gate), got %+v", cands[2].Suggestion)
	}
	// #1 cited an AGREEING witness → direction admitted as a PROJECTED suggestion.
	if cands[0].Suggestion == nil || cands[0].Suggestion.Direction != "a-to-b" {
		t.Errorf("witness-agreeing proposal must carry suggested direction a-to-b, got %+v", cands[0].Suggestion)
	}
	if cands[0].Relation != "co-occurrence" {
		t.Errorf("the candidate relation must stay direction-free co-occurrence, got %q", cands[0].Relation)
	}
	// #2 cited a DISAGREEING witness → direction withheld, hypothesis kept direction-free.
	if cands[1].Suggestion != nil && cands[1].Suggestion.Direction != "" {
		t.Errorf("witness-disagreeing proposal must NOT carry a direction, got %+v", cands[1].Suggestion)
	}
	// suggested = the agreeing positive + the not-causal; withheld = the disagreeing positive.
	if rep.DirectionsSuggested != 2 || rep.DirectionsWithheld != 1 {
		t.Errorf("telemetry: suggested=%d withheld=%d, want 2/1", rep.DirectionsSuggested, rep.DirectionsWithheld)
	}
	_ = candidate.KindCausalHypothesis
}
