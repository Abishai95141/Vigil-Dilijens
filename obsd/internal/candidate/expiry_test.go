package candidate

import (
	"fmt"
	"testing"
	"time"
)

func coHyp(subj string) Candidate {
	return Candidate{
		Kind: KindCausalHypothesis, Relation: "co-occurrence", Subject: subj,
		Payload:  map[string]any{"a": "x", "b": "y"},
		Evidence: []EvidenceRef{{Kind: "co-occurrence", Ref: "co"}},
		Lineage:  Lineage{Source: "test"},
	}
}

func TestExpireStale(t *testing.T) {
	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	base := time.Unix(1700000000, 0).UTC()
	if _, err := s.Put(base, coHyp("old ~ pair")); err != nil { // updated_at = base
		t.Fatal(err)
	}
	if _, err := s.Put(base.Add(20*time.Minute), coHyp("fresh ~ pair")); err != nil { // recent
		t.Fatal(err)
	}
	now := base.Add(20 * time.Minute) // cutoff = now-15m = base+5m

	// wrong kind ⇒ nothing expired (kind-filtered)
	if n, err := s.ExpireStale(now, 15*time.Minute, KindEdge); err != nil || n != 0 {
		t.Fatalf("expire wrong-kind: n=%d err=%v (want 0)", n, err)
	}
	// disabled ttl ⇒ nothing expired
	if n, _ := s.ExpireStale(now, 0, KindCausalHypothesis); n != 0 {
		t.Fatalf("ttl<=0 should be a no-op, expired %d", n)
	}
	// the >15m-stale co-occurrence ages out; the fresh one stays
	n, err := s.ExpireStale(now, 15*time.Minute, KindCausalHypothesis)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("expired %d, want 1", n)
	}
	rows, _ := s.List(Filter{Kind: KindCausalHypothesis})
	if len(rows) != 1 || rows[0].Subject != "fresh ~ pair" {
		t.Errorf("kept %+v, want only 'fresh ~ pair'", rows)
	}
}

func TestExpireSpareDecided(t *testing.T) {
	s, _ := Open("")
	defer s.Close()
	base := time.Unix(1700000000, 0).UTC()
	id, _ := s.Put(base, coHyp("decided ~ pair"))
	// a promoted (decided) row must NEVER be expired — it is the audit trail
	if err := s.Decide(base, id, StatusPromoted, "operator", "authored"); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.ExpireStale(base.Add(time.Hour), 15*time.Minute, KindCausalHypothesis); n != 0 {
		t.Errorf("expired %d decided rows, want 0 (audit trail is immutable)", n)
	}
}

func TestCapKind(t *testing.T) {
	s, _ := Open("")
	defer s.Close()
	base := time.Unix(1700000000, 0).UTC()
	for i := 0; i < 5; i++ {
		if _, err := s.Put(base.Add(time.Duration(i)*time.Minute), coHyp(fmt.Sprintf("p%d ~ q%d", i, i))); err != nil {
			t.Fatal(err)
		}
	}
	rm, err := s.CapKind(KindCausalHypothesis, 3)
	if err != nil {
		t.Fatal(err)
	}
	if rm != 2 {
		t.Errorf("removed %d, want 2", rm)
	}
	rows, _ := s.List(Filter{Kind: KindCausalHypothesis})
	if len(rows) != 3 {
		t.Errorf("kept %d, want 3 (the most-recently-updated)", len(rows))
	}
	// the kept ones must be the 3 most recent (p2,p3,p4)
	for _, r := range rows {
		if r.Subject == "p0 ~ q0" || r.Subject == "p1 ~ q1" {
			t.Errorf("CapKind kept a stale row %q; should keep the most recent", r.Subject)
		}
	}
	// max<=0 is a no-op
	if n, _ := s.CapKind(KindCausalHypothesis, 0); n != 0 {
		t.Errorf("max<=0 should be a no-op, removed %d", n)
	}
}
