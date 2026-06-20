package main

import (
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/candidate"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/unexplained"
)

// The recurrence `windows` count grows every eval tick, which changes the candidate content id —
// so the producer MUST dedup on the stable signature (subject), or the governance queue floods
// with duplicate phenomenon candidates for one recurring anomaly. This pins that contract.
func TestPhenomenonProducerDedupBySignature(t *testing.T) {
	st, err := candidate.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	rep := unexplained.CandidateReport{
		EntityKind: "Node", Metrics: []string{"node_pressure_cpu_waiting_seconds_total"},
		Windows: 20, Entities: []string{"i|c|ns|Node|w1|u1"}, Rationale: "recurring",
	}
	if err := stagePhenomenonCandidate(st, time.Now().UTC(), rep, "v0.9.0"); err != nil {
		t.Fatalf("stage: %v", err)
	}

	// The subject must be STABLE regardless of the growing windows count.
	subj0 := candidate.PhenomenonCandidateSubject(phenomenonSignature(rep))
	grown := rep
	grown.Windows = 57
	grown.Entities = append(grown.Entities, "i|c|ns|Node|w2|u2")
	if subj1 := candidate.PhenomenonCandidateSubject(phenomenonSignature(grown)); subj1 != subj0 {
		t.Fatalf("signature subject changed as recurrence grew: %q vs %q", subj0, subj1)
	}

	// The producer's dedup: a seen-set built from the store already contains the subject, so the
	// grown report is skipped — exactly one row per recurring anomaly.
	rows, err := st.List(candidate.Filter{Kind: candidate.KindPhenomenonCandidate})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 phenomenon_candidate row, got %d", len(rows))
	}
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r.Subject] = true
	}
	if !seen[candidate.PhenomenonCandidateSubject(phenomenonSignature(grown))] {
		t.Fatal("grown-windows report must dedup to the already-staged subject (the producer would skip it)")
	}
}
