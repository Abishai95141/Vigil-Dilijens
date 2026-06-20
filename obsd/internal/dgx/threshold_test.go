package dgx_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/dgx"
)

// The DECLARED support floor (slice 5) is OPT-IN: off by default it changes nothing; raised, it
// rejects proposals whose agreed-evidence support is below the floor — before a human sees them.
func TestMinSupportFloor(t *testing.T) {
	resp := `{"proposals":[
	 {"kind":"edge","subject":"one-ev","relation":"associated-with","evidence":["assoc:node|cpu~node|mem"],"rationale":"x"},
	 {"kind":"causal_hypothesis","subject":"two-ev","relation":"co-occurrence","evidence":["silence:foo","assoc:node|cpu~node|mem"],"rationale":"x"}
	]}`

	// Floor OFF (default): both the 1-evidence and the 2-evidence proposals pass — NO regression.
	off := dgx.New(dgx.NewStaticProvider("fake", resp), dgx.DefaultParams)
	candsOff, _, err := off.Propose(context.Background(), testContext())
	if err != nil {
		t.Fatal(err)
	}
	if len(candsOff) != 2 {
		t.Fatalf("floor off: accepted %d, want 2 (no regression)", len(candsOff))
	}

	// Floor = 2: the 1-evidence proposal is rejected with the declared-floor reason.
	p := dgx.DefaultParams
	p.MinSupport = 2
	on := dgx.New(dgx.NewStaticProvider("fake", resp), p)
	candsOn, rep, err := on.Propose(context.Background(), testContext())
	if err != nil {
		t.Fatal(err)
	}
	if len(candsOn) != 1 {
		t.Fatalf("floor=2: accepted %d, want 1 (only the 2-evidence proposal): %+v", len(candsOn), rep.Rejected)
	}
	found := false
	for _, r := range rep.Rejected {
		if strings.Contains(r.Reason, "support below floor") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a 'support below floor' rejection, got %+v", rep.Rejected)
	}
}

// The equiv_group capture-sample floor rejects a stray→group mapping whose pattern absorbs too
// few seen strays — so a new group must be a real dialect bridge, not a one-off pattern.
func TestMinCaptureSampleFloor(t *testing.T) {
	// "^redis_.*clients$" captures BOTH redis strays in equivContext → capture sample = 2.
	resp := `{"proposals":[
	 {"kind":"equiv_group","subject":"redis_connected_clients","group":"EQG_REDIS_CLIENTS","canonical":"db.redis.connected_clients","label":"Redis connected clients","pattern":"^redis_.*clients$","evidence":["stray:redis_connected_clients/abc"],"rationale":"r"}
	]}`

	// Floor = 2: capture sample (2) meets the floor → accepted.
	p2 := dgx.DefaultParams
	p2.MinCaptureSample = 2
	ag2 := dgx.New(dgx.NewStaticProvider("fake", resp), p2)
	cands2, rep2, err := ag2.Propose(context.Background(), equivContext())
	if err != nil {
		t.Fatal(err)
	}
	if len(cands2) != 1 {
		t.Fatalf("floor=2: accepted %d, want 1 (sample 2 ≥ 2): %+v", len(cands2), rep2.Rejected)
	}

	// Floor = 3: capture sample (2) is below the floor → rejected with the declared-floor reason.
	p3 := dgx.DefaultParams
	p3.MinCaptureSample = 3
	ag3 := dgx.New(dgx.NewStaticProvider("fake", resp), p3)
	cands3, rep3, err := ag3.Propose(context.Background(), equivContext())
	if err != nil {
		t.Fatal(err)
	}
	if len(cands3) != 0 {
		t.Fatalf("floor=3: accepted %d, want 0 (sample 2 < 3)", len(cands3))
	}
	found := false
	for _, r := range rep3.Rejected {
		if strings.Contains(r.Reason, "capture-sample below floor") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a 'capture-sample below floor' rejection, got %+v", rep3.Rejected)
	}
}
