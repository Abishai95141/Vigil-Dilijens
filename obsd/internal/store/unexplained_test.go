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

// Same-tick rows share one last_seen; the ORDER BY tiebreak (scope, signature)
// must make the read-back order total, and the fixed-width sqlTime format must
// keep lexicographic == chronological (RFC3339Nano trims trailing zeros, which
// breaks that: "…00Z" sorts after "…00.5Z").
func TestUnexplainedReadbackOrderTotal(t *testing.T) {
	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	at := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC) // zero fraction on purpose
	cards := []unexplained.Finding{
		card("i|cl|shop|Pod|b|uid-b", "m2", at),
		card("i|cl|shop|Pod|a|uid-a", "m1", at),
		card("i|cl|shop|Pod|c|uid-c", "m1", at.Add(500*time.Millisecond)), // fractional, NEWER
	}
	if err := s.UpsertUnexplained(at, cards); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ActiveUnexplained(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("want 3 rows, got %d", len(rows))
	}
	if rows[0].Scope != "i|cl|shop|Pod|c|uid-c" {
		t.Errorf("the chronologically newest row must come first (fixed-width ordering): %+v", rows[0])
	}
	if rows[1].Scope != "i|cl|shop|Pod|a|uid-a" || rows[2].Scope != "i|cl|shop|Pod|b|uid-b" {
		t.Errorf("same-stamp rows must read back in total (scope) order: %s, %s", rows[1].Scope, rows[2].Scope)
	}
	if !rows[0].LastSeen.Equal(at.Add(500 * time.Millisecond)) {
		t.Errorf("fixed-width stamp must round-trip: %v", rows[0].LastSeen)
	}
}

func card(scope, metric string, seen time.Time) unexplained.Finding {
	return unexplained.Finding{
		Scope: scope, Namespace: "shop", Name: "x", Kind: "Pod", Status: unexplained.StatusNew,
		LoudStates: []unexplained.LoudState{{Metric: metric, Kind: unexplained.LoudBarCrossing, State: "above"}},
		FirstSeen:  seen, LastSeen: seen, Occurrences: 1, GraphVersion: "v",
	}
}
