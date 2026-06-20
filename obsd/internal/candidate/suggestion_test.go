package candidate

import (
	"testing"
	"time"
)

// The PROJECTED suggestion is content-id-STABLE: setting it does not change the candidate's id,
// and a later re-Put of the same content (the deterministic producer re-staging) preserves it —
// the model annotation never perturbs the deterministic candidate.
func TestSetSuggestionContentStable(t *testing.T) {
	st, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)

	prop := PhenomenonCandidateProposal{
		Signature: "Node\x1fnode_pressure_cpu_waiting_seconds_total", EntityKind: "Node",
		Metrics: []string{"node_pressure_cpu_waiting_seconds_total"}, Windows: 20, Entities: []string{"e1"},
	}
	c := Candidate{
		Kind: KindPhenomenonCandidate, Subject: PhenomenonCandidateSubject(prop.Signature),
		Payload:  PhenomenonCandidatePayload(prop),
		Evidence: []EvidenceRef{{Kind: "context:unexplained", Ref: "unexplained:" + prop.Signature}},
	}
	id, err := st.Put(now, c)
	if err != nil {
		t.Fatal(err)
	}

	sg := &Suggestion{Label: "Node CPU scheduling pressure", Description: "tasks waiting on CPU", Model: "deepseek"}
	if err := st.SetSuggestion(now, id, sg); err != nil {
		t.Fatalf("set suggestion: %v", err)
	}
	got, found, err := st.Get(id)
	if err != nil || !found {
		t.Fatalf("get: %v found=%v", err, found)
	}
	if got.Suggestion == nil || got.Suggestion.Label != "Node CPU scheduling pressure" || got.Suggestion.Model != "deepseek" {
		t.Fatalf("suggestion not persisted: %+v", got.Suggestion)
	}

	// Re-Put the SAME content → SAME id (suggestion is not part of identity), and the suggestion
	// must survive (the producer re-staging the deterministic candidate never clears the hint).
	id2, err := st.Put(now, c)
	if err != nil {
		t.Fatal(err)
	}
	if id2 != id {
		t.Fatalf("content id changed by the suggestion: %q vs %q", id2, id)
	}
	got2, _, _ := st.Get(id)
	if got2.Suggestion == nil || got2.Suggestion.Label != "Node CPU scheduling pressure" {
		t.Fatalf("re-Put cleared the suggestion: %+v", got2.Suggestion)
	}

	// Clearing works, and a missing id is an error.
	if err := st.SetSuggestion(now, id, nil); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if got3, _, _ := st.Get(id); got3.Suggestion != nil {
		t.Fatalf("suggestion not cleared: %+v", got3.Suggestion)
	}
	if err := st.SetSuggestion(now, "cand:does-not-exist", sg); err == nil {
		t.Fatal("expected an error annotating a missing candidate")
	}
}
