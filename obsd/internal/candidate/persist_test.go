package candidate

import (
	"testing"
	"time"
)

// TestPromotionPersistsAcrossRestage is the docs/33 P5 guarantee ("once causality is mapped,
// it is used in upcoming events"): after a NAMED human authors a causal direction (promotes a
// causal_hypothesis), the deterministic co-onset lane re-staging the SAME pair on a future
// occurrence must NOT reset it to a fresh candidate — the authored decision sticks, so the pair
// is never re-surfaced as an unexplained hypothesis. store.Put's ON CONFLICT updates only
// payload/evidence/lineage, never status/decision; this pins that.
func TestPromotionPersistsAcrossRestage(t *testing.T) {
	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	t0 := time.Unix(1_700_000_000, 0).UTC()

	c := Candidate{
		Kind: KindCausalHypothesis, Subject: "x ~ y", Relation: "co-occurrence",
		Evidence: []EvidenceRef{{Kind: "co-onset", Ref: "cohyp:xy"}},
		Lineage:  Lineage{Source: "co-onset"},
	}
	id, err := s.Put(t0, c)
	if err != nil {
		t.Fatal(err)
	}
	// A named human authors the direction A → B.
	if err := s.Decide(t0.Add(time.Minute), id, StatusPromoted, "operator", "A → B per system knowledge"); err != nil {
		t.Fatal(err)
	}
	// A future co-onset of the SAME pair re-stages the identical content.
	id2, err := s.Put(t0.Add(time.Hour), c)
	if err != nil {
		t.Fatal(err)
	}
	if id2 != id {
		t.Fatalf("re-staging the same content must be the same candidate (content-id stable): %s != %s", id2, id)
	}
	got, ok, err := s.Get(id)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.Status != StatusPromoted {
		t.Fatalf("re-staging a promoted hypothesis must PRESERVE the authored promotion, got status %q", got.Status)
	}
	if got.DecidedBy != "operator" {
		t.Fatalf("the named author must persist across re-stage, got %q", got.DecidedBy)
	}
}
