package flow

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
)

// TestCaptureReplayDeterminism is the PHASE B proof: flow edges survive the exact
// production capture→replay round-trip (identity.EdgeStore.Snapshot → []EdgeSnap on
// disk → identity.NewEdgeStoreFromSnapshot) byte-identically, and the digest-bearing
// structural cascade computed from the replayed store is identical to the live one.
// This is the determinism guarantee flow edges must meet to enter the deterministic
// tick (doc 07 §3.7: same readings + same graph version + same topology snapshot ⇒
// same matches). Network-free, deterministic, -race.
func TestCaptureReplayDeterminism(t *testing.T) {
	at := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	g := loadSampleGraph(t, at)
	budgets := map[identity.EdgeType]time.Duration{EdgeTypeFlow: flowBudget}

	// LIVE: snapshot the flow edges exactly as the per-tick capture would.
	snapLive := g.Store().Snapshot()
	bLive, err := json.Marshal(snapLive)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapLive) == 0 {
		t.Fatal("no flow edges snapshotted")
	}
	// Every snapshotted edge is type "flow" (the only type this store holds).
	for _, s := range snapLive {
		if s.Type != string(EdgeTypeFlow) {
			t.Fatalf("unexpected edge type in snapshot: %q", s.Type)
		}
	}

	// DISK ROUND-TRIP: bytes → []EdgeSnap → rebuilt store under the PINNED budget,
	// exactly as replay/engine.go does from a bundle.
	var onDisk []identity.EdgeSnap
	if err := json.Unmarshal(bLive, &onDisk); err != nil {
		t.Fatal(err)
	}
	replayed, err := identity.NewEdgeStoreFromSnapshot(onDisk, budgets)
	if err != nil {
		t.Fatal(err)
	}
	bReplay, err := json.Marshal(replayed.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bLive, bReplay) {
		t.Fatalf("topology snapshot NOT byte-identical across capture→replay:\nlive=%s\nreplay=%s", bLive, bReplay)
	}

	// STRUCTURAL CASCADE: identical live vs replayed (the digest-bearing core).
	res := NewResolver("test-cluster", boutiquePods())
	catalog, _ := res.Role("10.244.1.3")
	w := identity.TimeWindow{Start: at, End: at}
	scLive := StructuralCascade(g.Store(), []string{catalog.Key()}, w)
	scReplay := StructuralCascade(replayed, []string{catalog.Key()}, w)
	jLive, _ := json.Marshal(scLive)
	jReplay, _ := json.Marshal(scReplay)
	if !bytes.Equal(jLive, jReplay) {
		t.Fatalf("structural cascade differs across replay:\nlive=%s\nreplay=%s", jLive, jReplay)
	}

	// Sanity: the cascade is the real one (root = catalog, 3 impacted callers).
	if scLive.Root != catalog.Key() {
		t.Errorf("root=%q, want productcatalog role key", scLive.Root)
	}
	if len(scLive.Links) != 3 {
		t.Errorf("structural links=%d, want 3", len(scLive.Links))
	}
	for _, l := range scLive.Links {
		if l.Traversal != "valid" {
			t.Errorf("edge %s->%s traversal=%s, want valid", l.Impacted, l.Degraded, l.Traversal)
		}
	}
}
