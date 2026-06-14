package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/store"
)

var incNow = time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)

func incRows() []store.IncidentRow {
	return []store.IncidentRow{
		{
			Phenomenon: "PHEN_MEMORY_LEAK", RoleCEI: "r|cl|online-boutique|Deployment|Deployment/currency",
			RecurrenceCount: 3, FirstSeen: incNow.Add(-2 * time.Hour), LastSeen: incNow.Add(-5 * time.Minute),
			LifespanSeconds: 6900,
		},
		{
			Phenomenon: "PHEN_OOM_KILL_SYSTEM", RoleCEI: "i|cl||Node|vigil-worker|uid-n", RoleUnresolved: true,
			RecurrenceCount: 1, FirstSeen: incNow.Add(-30 * time.Minute), LastSeen: incNow.Add(-30 * time.Minute),
		},
	}
}

func TestBuildIncidentsMeasuredAndFramed(t *testing.T) {
	v := BuildIncidents("gv", incNow, incRows())
	if !v.Available || v.Class != "MEASURED" {
		t.Fatalf("class/available wrong: %q %v", v.Class, v.Available)
	}
	if v.Summary.Total != 2 || v.Summary.Recurring != 1 || v.Summary.Unresolved != 1 {
		t.Fatalf("summary = %+v, want total 2 recurring 1 unresolved 1", v.Summary)
	}
	leak := v.Incidents[0]
	if leak.RecurrenceCount != 3 {
		t.Errorf("recurrence = %d, want 3", leak.RecurrenceCount)
	}
	if leak.RoleLabel != "online-boutique/Deployment/currency" {
		t.Errorf("roleLabel = %q", leak.RoleLabel)
	}
	if leak.LastSeenAgoSeconds <= 0 {
		t.Errorf("lastSeenAgoSeconds should be derived > 0, got %v", leak.LastSeenAgoSeconds)
	}
	node := v.Incidents[1]
	if !node.RoleUnresolved {
		t.Error("node incident must keep roleUnresolved (honest-partial)")
	}
}

// TestBuildIncidentsCharterClean: the incidents payload carries no banned register —
// recurrence is a count, never a cause or a forecast.
func TestBuildIncidentsCharterClean(t *testing.T) {
	payload, err := json.Marshal(BuildIncidents("gv", incNow, incRows()))
	if err != nil {
		t.Fatal(err)
	}
	if vs := CharterViolations("incidents", payload); len(vs) > 0 {
		t.Fatalf("incidents payload carried a banned register: %v", vs)
	}
}

func TestUnavailableIncidentsHonest(t *testing.T) {
	v := unavailableIncidents("gv", incNow)
	if v.Available {
		t.Fatal("unavailable state must be Available=false")
	}
	if len(v.Incidents) != 0 || v.Note == "" {
		t.Fatal("unavailable state must be empty + state why")
	}
}
