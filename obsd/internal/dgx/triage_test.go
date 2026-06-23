package dgx

import (
	"context"
	"testing"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/candidate"
)

func triageCand() candidate.Candidate {
	return candidate.Candidate{
		Kind: candidate.KindCausalHypothesis, Subject: "a ~ b", Relation: "co-occurrence",
		Evidence: []candidate.EvidenceRef{{Kind: "co-occurrence", Ref: "cohyp:1", Detail: "both stepped; lead-lag +4s (AGREES with onset order)"}},
		Payload:  map[string]any{"a": "seriesA", "b": "seriesB", "deltaSeconds": int64(4)},
	}
}

// TestTriagePromoteWithDirection: a promote verdict with a witness-supported direction parses
// fully and tags the model.
func TestTriagePromoteWithDirection(t *testing.T) {
	a := New(NewStaticProvider("deepseek",
		`{"verdict":"promote","confidence":"high","direction":"a-to-b","rationale":"lead-lag agrees with onset order"}`), Params{})
	d, err := a.TriageCandidate(context.Background(), triageCand())
	if err != nil {
		t.Fatal(err)
	}
	if d.Verdict != VerdictPromote || d.Direction != "a-to-b" || d.Confidence != "high" || d.Model != "deepseek" {
		t.Errorf("decision = %+v, want promote/a-to-b/high/deepseek", d)
	}
	if d.Rationale == "" {
		t.Error("rationale must survive")
	}
}

// TestTriageReject: an unnecessary candidate is rejected (no direction kept).
func TestTriageReject(t *testing.T) {
	a := New(NewStaticProvider("deepseek",
		`{"verdict":"reject","confidence":"medium","direction":"a-to-b","rationale":"redundant with an existing mapping"}`), Params{})
	d, _ := a.TriageCandidate(context.Background(), triageCand())
	if d.Verdict != VerdictReject {
		t.Errorf("verdict = %q, want reject", d.Verdict)
	}
	if d.Direction != "" {
		t.Errorf("a non-promote verdict must not carry a direction, got %q", d.Direction)
	}
}

// TestTriageFailsSafeToHold: a malformed / unknown verdict must NEVER promote — it coerces to hold.
func TestTriageFailsSafeToHold(t *testing.T) {
	for _, resp := range []string{
		`{"verdict":"yes-promote-it","confidence":"high","rationale":"x"}`, // unknown verdict
		`{"confidence":"high","rationale":"no verdict field"}`,             // absent verdict
		"```json\n{\"verdict\":\"\",\"rationale\":\"empty\"}\n```",         // empty verdict, fenced
	} {
		a := New(NewStaticProvider("deepseek", resp), Params{})
		d, err := a.TriageCandidate(context.Background(), triageCand())
		if err != nil {
			t.Fatalf("resp %q: %v", resp, err)
		}
		if d.Verdict != VerdictHold {
			t.Errorf("resp %q → verdict %q, want hold (fail-safe)", resp, d.Verdict)
		}
	}
}

// TestTriageMalformedJSONErrors: non-JSON text is an error, never a silent decision.
func TestTriageMalformedJSONErrors(t *testing.T) {
	a := New(NewStaticProvider("deepseek", "I think you should promote it!"), Params{})
	if _, err := a.TriageCandidate(context.Background(), triageCand()); err == nil {
		t.Error("non-JSON model output must error, never a silent verdict")
	}
}
