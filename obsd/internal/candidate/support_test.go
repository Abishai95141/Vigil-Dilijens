package candidate

import (
	"testing"
	"time"
)

func TestScoreCounts(t *testing.T) {
	c := Candidate{
		Kind:    KindEdge,
		Subject: "stray:redis_x ~> entity:e1",
		Evidence: []EvidenceRef{
			{Kind: "context:stray-metric", Ref: "stray:redis_x/ab"},
			{Kind: "context:entity", Ref: "entity:e1"},
			{Kind: "context:entity", Ref: "entity:e1"}, // dup entity ref → still 1 distinct
			{Kind: "context:entity", Ref: "entity:e2"},
		},
		CreatedAt: time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 6, 19, 12, 0, 30, 0, time.UTC),
	}
	s := Score(c, 3)
	if s.EvidenceCount != 4 {
		t.Errorf("evidence count = %d, want 4", s.EvidenceCount)
	}
	if s.DistinctEntities != 2 {
		t.Errorf("distinct entities = %d, want 2 (e1 deduped)", s.DistinctEntities)
	}
	if s.Recurrence != 3 {
		t.Errorf("recurrence = %d, want 3", s.Recurrence)
	}
	if s.AgeSeconds != 30 {
		t.Errorf("age = %d, want 30", s.AgeSeconds)
	}
	if s.CaptureSample != 0 {
		t.Errorf("non-equiv capture = %d, want 0", s.CaptureSample)
	}
	// Determinism: identical inputs ⇒ identical Support.
	if Score(c, 3) != s {
		t.Error("Score is not deterministic")
	}
	// A negative recurrence floors to 0 (never crashes / inflates).
	if Score(c, -5).Recurrence != 0 {
		t.Error("negative recurrence must floor to 0")
	}
}

func TestScoreEquivGroupCaptureSample(t *testing.T) {
	c := Candidate{
		Kind:    KindEquivGroup,
		Subject: EquivGroupSubject("redis_connected_clients"),
		Payload: EquivGroupPayload(EquivGroupProposal{
			Metric: "redis_connected_clients", Pattern: "^redis_.*$",
			NewGroup:      &NewEquivGroup{ID: "EQG_REDIS", Label: "R", CanonicalOTel: "c"},
			CaptureSample: []string{"redis_connected_clients", "redis_blocked_clients", "redis_keys"},
		}),
		Evidence: []EvidenceRef{{Kind: "context:stray-metric", Ref: "stray:redis_connected_clients/ab"}},
	}
	s := Score(c, 0)
	if s.CaptureSample != 3 {
		t.Errorf("capture sample = %d, want 3", s.CaptureSample)
	}
	if s.EvidenceCount != 1 {
		t.Errorf("evidence = %d, want 1", s.EvidenceCount)
	}
}

func TestSupportLessLexicographic(t *testing.T) {
	// Strongest-first ordering: evidence dominates, then capture, then recurrence, then
	// distinct entities, then newer (smaller age).
	cases := []struct {
		name   string
		weak   Support
		strong Support
	}{
		{"evidence dominates", Support{EvidenceCount: 1, CaptureSample: 99}, Support{EvidenceCount: 2}},
		{"capture next", Support{EvidenceCount: 2, CaptureSample: 1, Recurrence: 99}, Support{EvidenceCount: 2, CaptureSample: 5}},
		{"recurrence next", Support{EvidenceCount: 2, Recurrence: 1, DistinctEntities: 9}, Support{EvidenceCount: 2, Recurrence: 4}},
		{"distinct next", Support{EvidenceCount: 2, DistinctEntities: 1, AgeSeconds: 1}, Support{EvidenceCount: 2, DistinctEntities: 3, AgeSeconds: 999}},
		{"newer wins tiebreak", Support{EvidenceCount: 2, AgeSeconds: 100}, Support{EvidenceCount: 2, AgeSeconds: 10}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.weak.Less(tc.strong) {
				t.Errorf("%+v should rank below %+v", tc.weak, tc.strong)
			}
			if tc.strong.Less(tc.weak) {
				t.Errorf("%+v should NOT rank below %+v", tc.strong, tc.weak)
			}
		})
	}
}
