package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/events"
)

var evNow = time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)

func corroboratedFixture() []events.CorroboratedEvent {
	roleKey := "r|cl|online-boutique|Deployment|Deployment/currency"
	return []events.CorroboratedEvent{
		{ // corroborated: OOMKilled joined to the cgroup-OOM phenomenon on the same role
			Event: events.EventFinding{
				Reason: "OOMKilled", EntityCEI: "i|cl|online-boutique|Pod|currency-abc|u1", RoleCEI: roleKey,
				Namespace: "online-boutique", Name: "currency-abc", Kind: "Pod", Count: 2,
				FirstTimestamp: evNow.Add(-10 * time.Minute), LastTimestamp: evNow.Add(-1 * time.Minute),
			},
			Corroborates: "PHEN_OOM_KILL_CGROUP", GaugeRoleMatch: true,
			Why:    "An OOMKilled event co-occurs with the cgroup-OOM phenomenon on the same role; it corroborates, it does not prove it.",
			Author: "vigil-engineering", Version: "v1",
		},
		{ // standalone: CrashLoopBackOff, visible, never upgraded
			Event: events.EventFinding{
				Reason: "CrashLoopBackOff", EntityCEI: "i|cl|online-boutique|Pod|cart-xyz|u2", RoleCEI: "r|cl|online-boutique|Deployment|Deployment/cart",
				Namespace: "online-boutique", Name: "cart-xyz", Kind: "Pod", Count: 5,
				FirstTimestamp: evNow.Add(-5 * time.Minute), LastTimestamp: evNow.Add(-30 * time.Second),
			},
			Corroborates: "", GaugeRoleMatch: false,
		},
		{ // unresolved: a node OOM, instance-keyed, never a guessed role
			Event: events.EventFinding{
				Reason: "OOMKilling", EntityCEI: "i|cl||Node|vigil-worker|un", RoleCEI: "i|cl||Node|vigil-worker|un", RoleUnresolved: true,
				Name: "vigil-worker", Kind: "Node", Count: 1, LastTimestamp: evNow.Add(-2 * time.Minute),
			},
		},
	}
}

func TestBuildEventsMeasuredAndFramed(t *testing.T) {
	v := BuildEvents("gv", evNow, corroboratedFixture())
	if !v.Available || v.Class != "MEASURED" {
		t.Fatalf("class/available wrong: %q %v", v.Class, v.Available)
	}
	if v.Summary.Total != 3 || v.Summary.Corroborated != 1 || v.Summary.Standalone != 1 || v.Summary.Unresolved != 1 {
		t.Fatalf("summary = %+v, want total 3 corroborated 1 standalone 1 unresolved 1", v.Summary)
	}
	oom := v.Events[0]
	if oom.Reason != "OOMKilled" || !oom.Corroborated || oom.CorroborationWhy == "" {
		t.Errorf("OOMKilled card should be corroborated with an authored why: %+v", oom)
	}
	if oom.RoleLabel != "online-boutique/Deployment/currency" {
		t.Errorf("roleLabel = %q", oom.RoleLabel)
	}
	if oom.CorroborationProvenance != "vigil-engineering@v1" {
		t.Errorf("provenance = %q, want vigil-engineering@v1", oom.CorroborationProvenance)
	}
	if oom.LastSeenAgoSeconds <= 0 {
		t.Errorf("lastSeenAgoSeconds should be derived > 0, got %v", oom.LastSeenAgoSeconds)
	}
	// Standalone must NOT carry a why (nothing authored to surface), and must stay visible.
	if v.Events[1].Corroborated || v.Events[1].CorroborationWhy != "" {
		t.Error("standalone CrashLoopBackOff must not be corroborated / carry a why")
	}
	// Unresolved must keep roleUnresolved (honest-partial) and never be corroborated.
	if !v.Events[2].RoleUnresolved || v.Events[2].Corroborated {
		t.Error("a role-unresolved node event must stay unresolved and uncorroborated")
	}
}

// TestBuildEventsCharterClean: the events payload carries no banned register — an
// event is a co-occurrence, never a cause; the authored why is curated clean.
func TestBuildEventsCharterClean(t *testing.T) {
	payload, err := json.Marshal(BuildEvents("gv", evNow, corroboratedFixture()))
	if err != nil {
		t.Fatal(err)
	}
	if vs := CharterViolations("events", payload); len(vs) > 0 {
		t.Fatalf("events payload carried a banned register: %v", vs)
	}
}

func TestUnavailableEventsHonest(t *testing.T) {
	v := unavailableEvents("gv", evNow)
	if v.Available {
		t.Fatal("unavailable state must be Available=false")
	}
	if len(v.Events) != 0 || v.Note == "" {
		t.Fatal("unavailable state must be empty + state why")
	}
}
