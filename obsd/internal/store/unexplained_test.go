package store

import (
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/unexplained"
)

var ut = time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)

func loud(metric string) []unexplained.LoudState {
	return []unexplained.LoudState{{Metric: metric, Kind: unexplained.LoudBarCrossing, State: "well-above"}}
}

// The unexplained timeline source: a card persists by (scope, signature); a
// recurrence advances last_seen + status; a closed card lands its terminal
// status with the span preserved.
func TestUpsertAndReadUnexplained(t *testing.T) {
	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	card := unexplained.Finding{
		Scope: "i|cl|shop|Container|app|uid/app", Namespace: "shop", Name: "app", Kind: "Container",
		LoudStates: loud("weird_metric"), Status: unexplained.StatusNew, Mark: unexplained.Mark,
		Occurrences: 1, FirstSeen: ut, LastSeen: ut, GraphVersion: "v",
	}
	if err := s.UpsertUnexplained(ut, []unexplained.Finding{card}); err != nil {
		t.Fatal(err)
	}

	// Recur: same scope+signature, aging, later last_seen.
	card.Status = unexplained.StatusAging
	card.Occurrences = 3
	card.LastSeen = ut.Add(30 * time.Second)
	if err := s.UpsertUnexplained(ut.Add(30*time.Second), []unexplained.Finding{card}); err != nil {
		t.Fatal(err)
	}

	rows, err := s.ActiveUnexplained(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("recurrence must update one row, not insert: %d rows", len(rows))
	}
	r := rows[0]
	if r.Status != "aging" || r.Occurrences != 3 {
		t.Errorf("status/occurrences not refreshed: %+v", r)
	}
	if !r.FirstSeen.Equal(ut) || !r.LastSeen.Equal(ut.Add(30*time.Second)) {
		t.Errorf("span not preserved (first kept, last advanced): %+v", r)
	}
	if len(r.Metrics) != 1 || r.Metrics[0] != "weird_metric" {
		t.Errorf("metrics signature wrong: %+v", r.Metrics)
	}

	// Close it (resolved) — the row stays for the timeline, with terminal status.
	card.Status = unexplained.StatusResolved
	card.LastSeen = ut.Add(45 * time.Second)
	if err := s.UpsertUnexplained(ut.Add(45*time.Second), []unexplained.Finding{card}); err != nil {
		t.Fatal(err)
	}
	rows, _ = s.ActiveUnexplained(10)
	if len(rows) != 1 || rows[0].Status != "resolved" {
		t.Errorf("resolved card must land its terminal status, row kept: %+v", rows)
	}

	// Distinct signature → distinct row.
	other := card
	other.LoudStates = loud("another_metric")
	other.Status = unexplained.StatusNew
	if err := s.UpsertUnexplained(ut.Add(time.Minute), []unexplained.Finding{other}); err != nil {
		t.Fatal(err)
	}
	rows, _ = s.ActiveUnexplained(10)
	if len(rows) != 2 {
		t.Errorf("a distinct loud signature is a distinct row: %d", len(rows))
	}
}
