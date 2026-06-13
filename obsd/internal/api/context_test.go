package api

import (
	"testing"
	"time"
)

func TestContextWindowAddValidation(t *testing.T) {
	s := NewContextWindowStore()
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	start := now.Add(-time.Hour)

	bad := []ContextWindow{
		{Kind: WindowDeploy, StartAt: start, Author: "a"},                                             // no label
		{Label: "x", StartAt: start, Author: "a"},                                                     // bad kind
		{Label: "x", Kind: WindowDeploy, Author: "a"},                                                 // no start
		{Label: "x", Kind: WindowDeploy, StartAt: start},                                              // no author
		{Label: "x", Kind: WindowDeploy, StartAt: start, EndAt: start.Add(-time.Minute), Author: "a"}, // end < start
	}
	for i, w := range bad {
		if _, err := s.Add(w, now); err == nil {
			t.Errorf("case %d: expected validation error", i)
		}
	}

	good, err := s.Add(ContextWindow{Label: "v0.4.0 rollout", Kind: WindowDeploy, StartAt: start, Author: "ops"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if good.ID == "" || good.CreatedAt != now || !good.SpliceEligible {
		t.Errorf("stored window wrong: %+v", good)
	}
}

func TestContextWindowSplicePoints(t *testing.T) {
	s := NewContextWindowStore()
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	mk := func(kind ContextWindowKind, mins int) {
		_, err := s.Add(ContextWindow{Label: string(kind), Kind: kind, StartAt: now.Add(time.Duration(mins) * time.Minute), Author: "a"}, now)
		if err != nil {
			t.Fatal(err)
		}
	}
	mk(WindowDeploy, -30)     // splice
	mk(WindowConfig, -20)     // splice
	mk(WindowIncident, -10)   // NOT a splice (a region to avoid forecasting across)
	mk(WindowMaintenance, -5) // NOT a splice

	pts := s.SplicePoints()
	if len(pts) != 2 {
		t.Fatalf("want 2 splice points (deploy + config), got %d", len(pts))
	}
	// In time order.
	if !pts[0].Before(pts[1]) {
		t.Errorf("splice points not time-ordered")
	}
}

func TestContextWindowListOrdering(t *testing.T) {
	s := NewContextWindowStore()
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	s.Add(ContextWindow{Label: "old", Kind: WindowDeploy, StartAt: now.Add(-2 * time.Hour), Author: "a"}, now)
	s.Add(ContextWindow{Label: "new", Kind: WindowDeploy, StartAt: now.Add(-1 * time.Hour), Author: "a"}, now)
	list := s.List()
	if len(list) != 2 || list[0].Label != "new" {
		t.Errorf("expected newest-start-first ordering, got %+v", list)
	}
}
