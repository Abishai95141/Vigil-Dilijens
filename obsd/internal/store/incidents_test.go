package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/incident"
)

var incT0 = time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)

const (
	incWeek    = 7 * 24 * time.Hour
	incResolve = 90 * time.Second
)

// TestUpsertIncidentMatchesAccumulator cross-checks the durable store against the pure
// incident.Accumulator over the same observation sequence: the two implementations of
// the recurrence logic must agree exactly (no divergence between live memory and the
// replay-gate fold).
func TestUpsertIncidentMatchesAccumulator(t *testing.T) {
	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	acc := incident.NewAccumulator(incResolve, incWeek)

	obs := []struct {
		phen, role string
		at         time.Time
	}{
		{"PHEN_MEMORY_LEAK", "r|c|ns|Deployment|web", incT0},
		{"PHEN_MEMORY_LEAK", "r|c|ns|Deployment|web", incT0.Add(30 * time.Second)},    // same episode
		{"PHEN_MEMORY_LEAK", "r|c|ns|Deployment|web", incT0.Add(10 * time.Minute)},    // resolve→refire
		{"PHEN_OOM_KILL", "r|c|ns|Deployment|web", incT0.Add(time.Minute)},            // distinct phenomenon
		{"PHEN_MEMORY_LEAK", "r|c|ns|Deployment|payment", incT0.Add(2 * time.Minute)}, // distinct role
	}
	for _, o := range obs {
		if err := s.UpsertIncident(o.at, o.phen, o.role, false, "v", incResolve, incWeek); err != nil {
			t.Fatalf("UpsertIncident: %v", err)
		}
		acc.Observe(o.phen, o.role, false, "v", o.at)
	}

	want := map[string]incident.Incident{}
	for _, inc := range acc.Snapshot() {
		want[inc.Key] = inc
	}
	rows, err := s.ActiveIncidents(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(want) {
		t.Fatalf("store has %d incidents, accumulator %d", len(rows), len(want))
	}
	for _, r := range rows {
		w, ok := want[r.Key]
		if !ok {
			t.Fatalf("store produced a key the accumulator did not: %s", r.Key)
		}
		if r.RecurrenceCount != w.RecurrenceCount {
			t.Errorf("%s: store recurrence %d != accumulator %d", r.Phenomenon, r.RecurrenceCount, w.RecurrenceCount)
		}
		if !r.FirstSeen.Equal(w.FirstSeen) || !r.LastSeen.Equal(w.LastSeen) {
			t.Errorf("%s: store span [%v,%v] != accumulator [%v,%v]", r.Phenomenon, r.FirstSeen, r.LastSeen, w.FirstSeen, w.LastSeen)
		}
	}
}

// TestIncidentSurvivesRestart proves the durable claim: an incident persists across a
// real process restart (close the DB, reopen), and a later resolve→refire continues
// the same incident with recurrence 2 — what the windowed CascadeTracker cannot do.
func TestIncidentSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.db")
	phen, role := "PHEN_MEMORY_LEAK", "r|c|ns|Deployment|web"

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertIncident(incT0, phen, role, false, "v", incResolve, incWeek); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertIncident(incT0.Add(30*time.Second), phen, role, false, "v", incResolve, incWeek); err != nil {
		t.Fatal(err)
	}
	s.Close() // process restart

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	// The refire after the gap must continue the SAME incident, not start a new one.
	if err := s2.UpsertIncident(incT0.Add(10*time.Minute), phen, role, false, "v", incResolve, incWeek); err != nil {
		t.Fatal(err)
	}
	rows, err := s2.ActiveIncidents(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 incident across restart, got %d", len(rows))
	}
	if rows[0].RecurrenceCount != 2 {
		t.Fatalf("recurrence across restart = %d, want 2", rows[0].RecurrenceCount)
	}
	if rows[0].LifespanSeconds != int64((10 * time.Minute).Seconds()) {
		t.Errorf("lifespan = %ds, want 600s", rows[0].LifespanSeconds)
	}
}

// TestIncidentContinuousSpanNoInflation: many sub-horizon ticks of one continuous
// condition stay recurrence 1 (no count-every-tick inflation), durably.
func TestIncidentContinuousSpanNoInflation(t *testing.T) {
	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i := 0; i < 12; i++ {
		if err := s.UpsertIncident(incT0.Add(time.Duration(i)*30*time.Second),
			"PHEN_MEMORY_LEAK", "r|c|ns|Deployment|web", false, "v", incResolve, incWeek); err != nil {
			t.Fatal(err)
		}
	}
	rows, _ := s.ActiveIncidents(10)
	if len(rows) != 1 || rows[0].RecurrenceCount != 1 {
		t.Fatalf("continuous span: got %d incidents, recurrence %v; want 1 incident recurrence 1", len(rows), rows)
	}
}
