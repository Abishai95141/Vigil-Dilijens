package governance

import (
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
	"testing"
)

// behaviouralChange returns from/to graphs whose diff is behavioural-medium (a new
// phenomenon), for workflow tests.
func behaviouralChange() (*graph.Graph, *graph.Graph) {
	from := baseGraph()
	to := baseGraph()
	to.Phenomena["PHEN_NEW"] = &graph.Phenomenon{ID: "PHEN_NEW", Label: "new"}
	return from, to
}

func allGatesPass(class ChangeClass) []GateResult {
	var rs []GateResult
	for _, g := range RequiredGates(class) {
		rs = append(rs, GateResult{Gate: string(g), Status: "passed", Detail: "ok", RanAt: "2026-06-13T00:00:00Z"})
	}
	return rs
}

// ledgerFor wraps gate results into a ledger pinned to a proposal + class (the
// binding the workflow enforces).
func ledgerFor(class ChangeClass, proposal string, rs []GateResult) *GateLedger {
	return &GateLedger{Class: class.String(), Proposal: proposal, Results: rs}
}

func TestWorkflowApproved(t *testing.T) {
	from, to := behaviouralChange()
	p := &Proposal{
		Release: "v0.4.0", From: "v0.3.0", Class: "behavioural-medium", Author: "curator-a",
		Evidence: []EvidenceRef{{Kind: "corpus-run", Ref: "leak-plateau"}},
		Review:   Review{Reviewer: "senior-b", Decision: "approve"},
	}
	w, err := EvaluateProposal(p, from, to, ledgerFor(ClassBehaviouralMedium, "v0.4.0", allGatesPass(ClassBehaviouralMedium)))
	if err != nil {
		t.Fatal(err)
	}
	if w.Verdict != VerdictApproved {
		t.Fatalf("verdict = %s, want approved; checks: %+v", w.Verdict, w.Checks)
	}
}

func TestWorkflowMisclassificationBlocks(t *testing.T) {
	// Declares additive-low, but the diff changes an existing default → normative.
	from := baseGraph()
	from.Rules = []*graph.ThresholdRule{{ID: "THR_X", Signal: "SIG_A", Metric: "m", Kind: "absolute", Default: f64(100), Direction: "above"}}
	to := baseGraph()
	to.Rules = []*graph.ThresholdRule{{ID: "THR_X", Signal: "SIG_A", Metric: "m", Kind: "absolute", Default: f64(50), Direction: "above"}}
	p := &Proposal{Release: "v0.4.0", Class: "additive-low", Author: "curator-a"}
	w, err := EvaluateProposal(p, from, to, nil)
	if err != nil {
		t.Fatal(err)
	}
	if w.Verdict != VerdictBlocked {
		t.Fatalf("verdict = %s, want blocked", w.Verdict)
	}
	if !hasBlock(w, "class-verification") {
		t.Errorf("expected class-verification block; checks: %+v", w.Checks)
	}
}

func TestWorkflowEvidenceRequired(t *testing.T) {
	from, to := behaviouralChange()
	p := &Proposal{Release: "v0.4.0", Class: "behavioural-medium", Author: "curator-a"} // no evidence
	w, err := EvaluateProposal(p, from, to, ledgerFor(ClassBehaviouralMedium, "v0.4.0", allGatesPass(ClassBehaviouralMedium)))
	if err != nil {
		t.Fatal(err)
	}
	if w.Verdict != VerdictBlocked || !hasBlock(w, "evidence") {
		t.Fatalf("expected evidence block; verdict %s checks %+v", w.Verdict, w.Checks)
	}
}

func TestWorkflowAuthorRequired(t *testing.T) {
	from, to := behaviouralChange()
	p := &Proposal{Release: "v0.4.0", Class: "behavioural-medium",
		Evidence: []EvidenceRef{{Kind: "corpus", Ref: "x"}}} // no author
	w, _ := EvaluateProposal(p, from, to, ledgerFor(ClassBehaviouralMedium, "v0.4.0", allGatesPass(ClassBehaviouralMedium)))
	if w.Verdict != VerdictBlocked || !hasBlock(w, "authorship") {
		t.Fatalf("expected authorship block; verdict %s", w.Verdict)
	}
}

func TestWorkflowReadyForReviewWithoutApproval(t *testing.T) {
	from, to := behaviouralChange()
	p := &Proposal{Release: "v0.4.0", Class: "behavioural-medium", Author: "curator-a",
		Evidence: []EvidenceRef{{Kind: "corpus", Ref: "x"}}} // no review decision
	w, _ := EvaluateProposal(p, from, to, ledgerFor(ClassBehaviouralMedium, "v0.4.0", allGatesPass(ClassBehaviouralMedium)))
	if w.Verdict != VerdictReadyForReview {
		t.Fatalf("verdict = %s, want ready-for-review (mechanical pass, human pending)", w.Verdict)
	}
}

func TestWorkflowMissingGateBlocks(t *testing.T) {
	from, to := behaviouralChange()
	// Supply gates but drop the falsification one — a skipped gate cannot release.
	results := []GateResult{
		{Gate: string(GateLints), Status: "passed"},
		{Gate: string(GateBindingQA), Status: "passed"},
		{Gate: string(GateReplayDiff), Status: "passed"},
		// GateFalsification missing
	}
	p := &Proposal{Release: "v0.4.0", Class: "behavioural-medium", Author: "a",
		Evidence: []EvidenceRef{{Kind: "c", Ref: "x"}}, Review: Review{Reviewer: "b", Decision: "approve"}}
	w, _ := EvaluateProposal(p, from, to, ledgerFor(ClassBehaviouralMedium, "v0.4.0", results))
	if w.Verdict != VerdictBlocked || !hasBlock(w, "gate:falsification") {
		t.Fatalf("expected falsification gate block; verdict %s checks %+v", w.Verdict, w.Checks)
	}
}

func TestWorkflowFailedGateBlocks(t *testing.T) {
	from, to := behaviouralChange()
	results := allGatesPass(ClassBehaviouralMedium)
	results[0].Status = "failed" // lints failed
	p := &Proposal{Release: "v0.4.0", Class: "behavioural-medium", Author: "a",
		Evidence: []EvidenceRef{{Kind: "c", Ref: "x"}}, Review: Review{Reviewer: "b", Decision: "approve"}}
	w, _ := EvaluateProposal(p, from, to, ledgerFor(ClassBehaviouralMedium, "v0.4.0", results))
	if w.Verdict != VerdictBlocked {
		t.Fatalf("expected blocked on failed gate; verdict %s", w.Verdict)
	}
}

// TestWorkflowDryRunLedgerBlocks pins the BLOCKING fix: a dry-run ledger (status
// "dry-run", nothing executed) can never approve a release.
func TestWorkflowDryRunLedgerBlocks(t *testing.T) {
	from, to := behaviouralChange()
	var rs []GateResult
	for _, g := range RequiredGates(ClassBehaviouralMedium) {
		rs = append(rs, GateResult{Gate: string(g), Status: "dry-run", Detail: "dry-run (not executed)"})
	}
	p := &Proposal{Release: "v0.4.0", Class: "behavioural-medium", Author: "a",
		Evidence: []EvidenceRef{{Kind: "c", Ref: "x"}}, Review: Review{Reviewer: "b", Decision: "approve"}}
	w, _ := EvaluateProposal(p, from, to, ledgerFor(ClassBehaviouralMedium, "v0.4.0", rs))
	if w.Verdict != VerdictBlocked {
		t.Fatalf("a dry-run ledger must BLOCK (nothing ran), got %s", w.Verdict)
	}
}

// TestWorkflowWeakerClassLedgerBlocks: a ledger run for a weaker class cannot satisfy
// a stronger-class proposal.
func TestWorkflowWeakerClassLedgerBlocks(t *testing.T) {
	// A normative change, but the ledger was run for behavioural-medium.
	from := baseGraph()
	from.Rules = []*graph.ThresholdRule{{ID: "THR_X", Signal: "SIG_A", Metric: "m", Kind: "absolute", Default: f64(100), Direction: "above"}}
	to := baseGraph()
	to.Rules = []*graph.ThresholdRule{{ID: "THR_X", Signal: "SIG_A", Metric: "m", Kind: "absolute", Default: f64(50), Direction: "above"}}
	p := &Proposal{Release: "v0.4.0", Class: "normative-high", Author: "a",
		Evidence: []EvidenceRef{{Kind: "c", Ref: "x"}}, Review: Review{Reviewer: "b", Decision: "approve"}}
	// Ledger declares behavioural-medium (weaker) — even if its gates pass.
	w, _ := EvaluateProposal(p, from, to, ledgerFor(ClassBehaviouralMedium, "v0.4.0", allGatesPass(ClassNormativeHigh)))
	if w.Verdict != VerdictBlocked || !hasBlock(w, "ledger-binding") {
		t.Fatalf("a weaker-class ledger must block; verdict %s checks %+v", w.Verdict, w.Checks)
	}
}

// TestWorkflowWrongProposalLedgerBlocks: a ledger for a different proposal cannot be
// reused.
func TestWorkflowWrongProposalLedgerBlocks(t *testing.T) {
	from, to := behaviouralChange()
	p := &Proposal{Release: "v0.4.0", Class: "behavioural-medium", Author: "a",
		Evidence: []EvidenceRef{{Kind: "c", Ref: "x"}}, Review: Review{Reviewer: "b", Decision: "approve"}}
	w, _ := EvaluateProposal(p, from, to, ledgerFor(ClassBehaviouralMedium, "v0.9.9-other", allGatesPass(ClassBehaviouralMedium)))
	if w.Verdict != VerdictBlocked || !hasBlock(w, "ledger-binding") {
		t.Fatalf("a ledger for another proposal must block; verdict %s", w.Verdict)
	}
}

func hasBlock(w *WorkflowResult, name string) bool {
	for _, c := range w.Checks {
		if c.Name == name && c.Status == StatusBlock {
			return true
		}
	}
	return false
}
