package store

import (
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/detect"
)

var t0 = time.Date(2026, 6, 12, 9, 0, 0, 0, time.UTC)

func finding(entity, phen string, q detect.MatchQuality) detect.Finding {
	return detect.Finding{
		Phenomenon: phen, Label: "Memory leak", EntityCEI: entity,
		Namespace: "shop", Name: "web-a", Kind: "Container", GraphVersion: "sha256:abc",
		Quality: q, Completeness: 0.5, RequiredTotal: 2, RequiredMet: 1, RequiredUnobserved: 1,
		Members:      []detect.MemberEvidence{{SignalID: "SIG_x", Note: "rising"}},
		Unobservable: []string{"SIG_y"},
	}
}

func TestUpsertAndRead(t *testing.T) {
	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.UpsertFindings(t0, []detect.Finding{finding("e1", "PHEN_MEMORY_LEAK", detect.QualityDegraded)}); err != nil {
		t.Fatal(err)
	}
	// Same finding a tick later: advances last_seen, keeps first_seen, one row.
	if err := s.UpsertFindings(t0.Add(time.Minute), []detect.Finding{finding("e1", "PHEN_MEMORY_LEAK", detect.QualityDegraded)}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ActiveFindings(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("a recurring finding must upsert to ONE row, got %d", len(rows))
	}
	r := rows[0]
	if !r.FirstSeen.Equal(t0) || !r.LastSeen.Equal(t0.Add(time.Minute)) {
		t.Errorf("span wrong: first=%v last=%v", r.FirstSeen, r.LastSeen)
	}
	if r.Quality != "degraded" || r.Completeness != 0.5 || r.RequiredUnobserved != 1 {
		t.Errorf("snapshot wrong: %+v", r)
	}
	if string(r.Unobservable) == "" || string(r.Members) == "" {
		t.Error("members/unobservable evidence must round-trip")
	}
}

func TestDistinctEntitiesAndOrdering(t *testing.T) {
	s, _ := Open("")
	defer s.Close()
	_ = s.UpsertFindings(t0, []detect.Finding{finding("e1", "PHEN_MEMORY_LEAK", detect.QualityDegraded)})
	_ = s.UpsertFindings(t0.Add(time.Minute), []detect.Finding{finding("e2", "PHEN_MEMORY_LEAK", detect.QualityFull)})
	rows, _ := s.ActiveFindings(10)
	if len(rows) != 2 {
		t.Fatalf("distinct entities = distinct rows: got %d", len(rows))
	}
	// Newest last_seen first.
	if rows[0].EntityCEI != "e2" {
		t.Errorf("ordering wrong: %s before %s", rows[0].EntityCEI, rows[1].EntityCEI)
	}
	n, _ := s.Count()
	if n != 2 {
		t.Errorf("count = %d, want 2", n)
	}
}

func TestEmptyUpsertIsNoop(t *testing.T) {
	s, _ := Open("")
	defer s.Close()
	if err := s.UpsertFindings(t0, nil); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.ActiveFindings(10)
	if len(rows) != 0 {
		t.Errorf("empty upsert should persist nothing, got %d", len(rows))
	}
}
