package candidate

import (
	"testing"
	"time"
)

// TestAgentDecisionAuditAndRevert pins the docs/33 build-4 audit log + revert: an agent
// promotion is logged, listed newest-first, and reversible — the revert re-opens the candidate
// and flips the SAME audit row to reverted (append-only trail, never deleted).
func TestAgentDecisionAuditAndRevert(t *testing.T) {
	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)

	id, err := s.Put(now, Candidate{Kind: KindCausalHypothesis, Subject: "a ~ b", Relation: "co-occurrence"})
	if err != nil {
		t.Fatal(err)
	}

	// The agent promotes it (operator-authorized), and we log the decision.
	if err := s.Decide(now, id, StatusPromoted, "agent-triage:deepseek", "operator-authored causal direction: a → b. (agent)"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LogAgentDecision(now, AgentDecision{
		CandidateID: id, Verdict: "promote", Applied: true, Confidence: "high",
		Rationale: "lead-lag agrees", Direction: "a-to-b", Model: "deepseek", AuthorizedBy: "alice",
	}); err != nil {
		t.Fatal(err)
	}

	// A "hold" on the same candidate is ALSO logged (an agent action, not applied).
	if _, err := s.LogAgentDecision(now.Add(time.Minute), AgentDecision{
		CandidateID: id, Verdict: "hold", Applied: false, Model: "deepseek", AuthorizedBy: "alice",
	}); err != nil {
		t.Fatal(err)
	}

	log, err := s.ListAgentDecisions(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(log) != 2 {
		t.Fatalf("audit log = %d entries, want 2", len(log))
	}
	if log[0].Verdict != "hold" { // newest-first
		t.Errorf("newest entry = %q, want hold", log[0].Verdict)
	}
	promote := log[1]
	if promote.Verdict != "promote" || !promote.Applied || promote.AuthorizedBy != "alice" || promote.Model != "deepseek" {
		t.Errorf("promote audit row wrong: %+v", promote)
	}

	// Revert the promotion: the candidate re-opens, and the promote audit row flips to reverted.
	if err := s.Revert(now.Add(2*time.Minute), id, "bob"); err != nil {
		t.Fatal(err)
	}
	c, ok, _ := s.Get(id)
	if !ok || c.Status != StatusCandidate {
		t.Fatalf("after revert status = %v (ok=%v), want candidate (re-opened)", c.Status, ok)
	}
	if c.DecidedBy != "" {
		t.Errorf("revert must clear decidedBy, got %q", c.DecidedBy)
	}
	log, _ = s.ListAgentDecisions(0)
	var reverted *AgentDecision
	for i := range log {
		if log[i].Verdict == "promote" {
			reverted = &log[i]
		}
	}
	if reverted == nil || !reverted.Reverted || reverted.RevertedBy != "bob" {
		t.Errorf("promote row must be marked reverted by bob: %+v", reverted)
	}

	// Reverting a non-promoted candidate is an error (only promotions revert).
	if err := s.Revert(now, id, "bob"); err == nil {
		t.Error("reverting a re-opened (candidate) row must error")
	}
	// An agent decision with no authorizing operator is refused.
	if _, err := s.LogAgentDecision(now, AgentDecision{CandidateID: id, Verdict: "promote"}); err == nil {
		t.Error("an agent decision with no authorizedBy must be refused")
	}
}
