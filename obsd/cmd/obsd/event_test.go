package main

import (
	"testing"
	"time"

	vapi "github.com/Abishai95141/Vigil-Dilijens/obsd/internal/api"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/dgx"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/unexplained"
)

func TestDiffNewExplorationEvents(t *testing.T) {
	now := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	unexp := &vapi.UnexplainedView{
		OpenCards: []unexplained.Finding{
			{Scope: "i|c|ns|Pod|a|u", Namespace: "ns", Name: "a", Kind: "Pod", Status: unexplained.StatusNew},
			{Scope: "i|c|ns|Pod|b|u", Namespace: "ns", Name: "b", Kind: "Pod", Status: unexplained.StatusAging}, // NOT new → skipped
		},
	}
	events := &vapi.EventsView{Available: true, Events: []vapi.EventCard{
		{Reason: "OOMKilled", Namespace: "ns", Name: "a", Kind: "Pod"},
	}}
	strays := []dgx.Observation{{Ref: "stray:redis_memory_used_bytes", Detail: "unmapped"}}

	seen := map[string]bool{}
	fresh := diffNewExplorationEvents(seen, unexp, events, strays, now)
	if len(fresh) != 3 {
		t.Fatalf("first diff: got %d events, want 3 (new card + event + stray)\n%+v", len(fresh), fresh)
	}
	kinds := map[string]int{}
	for _, e := range fresh {
		kinds[e.Kind]++
		if e.EmittedAt != now {
			t.Errorf("event %q has wrong EmittedAt", e.Ref)
		}
	}
	if kinds["unexplained"] != 1 || kinds["event"] != 1 || kinds["stray"] != 1 {
		t.Fatalf("kind mix = %v, want one each", kinds)
	}

	// Second diff over the SAME snapshots → nothing new (the seen-set suppresses re-firing).
	if again := diffNewExplorationEvents(seen, unexp, events, strays, now); len(again) != 0 {
		t.Fatalf("second diff re-fired %d events; the seen-set must suppress them: %+v", len(again), again)
	}

	// A genuinely new event fires exactly once.
	events.Events = append(events.Events, vapi.EventCard{Reason: "CrashLoopBackOff", Namespace: "ns", Name: "c", Kind: "Pod"})
	third := diffNewExplorationEvents(seen, unexp, events, strays, now)
	if len(third) != 1 || third[0].Kind != "event" {
		t.Fatalf("third diff = %+v, want exactly the one new event", third)
	}
}

func TestDiffNilSnapshotsSafe(t *testing.T) {
	now := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	// Nil views + an unavailable events view must not panic and must emit nothing extra.
	seen := map[string]bool{}
	if fresh := diffNewExplorationEvents(seen, nil, &vapi.EventsView{Available: false}, nil, now); len(fresh) != 0 {
		t.Fatalf("nil/unavailable snapshots produced %d events, want 0", len(fresh))
	}
}
