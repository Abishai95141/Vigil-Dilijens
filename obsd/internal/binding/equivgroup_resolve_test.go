package binding

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/candidate"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
)

// The full Phase-1 round trip (doc 21 §5): a metric that is a STRAY against the base graph
// becomes RESOLVABLE after a promoted equiv_group candidate's overlay is loaded. This proves
// the absorb path end to end — promotion → authored overlay → deterministic resolver — which
// is the one promotion that moves MEASURED coverage.
func TestPromotedEquivGroupOverlayResolvesStray(t *testing.T) {
	const metric = "redis_connected_clients"
	const newGroupID = "EQG_REDIS_CONNECTED_CLIENTS"

	base, err := graph.LoadWithOverlays(kgPath, "")
	if err != nil {
		t.Fatalf("load base KG: %v", err)
	}
	r0, err := NewEquivalenceResolver(base)
	if err != nil {
		t.Fatalf("resolver(base): %v", err)
	}
	if got := r0.Resolve(metric); len(got) != 0 {
		t.Fatalf("%q is already mapped in the base KG (%v) — pick a genuinely-stray metric", metric, got)
	}

	// A promoted candidate proposing a NEW group for the stray.
	c := candidate.Candidate{
		ID: "cand:redisclients01", Kind: candidate.KindEquivGroup, Status: candidate.StatusPromoted,
		DecidedBy: "alice", Note: "redis_exporter connected-clients gauge",
		Subject: candidate.EquivGroupSubject(metric),
		Payload: candidate.EquivGroupPayload(candidate.EquivGroupProposal{
			Metric:  metric,
			Pattern: "^redis_connected_clients$",
			NewGroup: &candidate.NewEquivGroup{
				ID: newGroupID, Label: "Redis connected clients", CanonicalOTel: "db.redis.connected_clients",
			},
		}),
	}
	ovl, err := candidate.PromotedOverlayYAML(c, base.Version)
	if err != nil {
		t.Fatalf("render overlay: %v", err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "equiv-group-redis.yaml"), []byte(ovl), 0o644); err != nil {
		t.Fatal(err)
	}
	g1, err := graph.LoadWithOverlays(kgPath, dir)
	if err != nil {
		t.Fatalf("load base + promoted overlay: %v", err)
	}
	r1, err := NewEquivalenceResolver(g1)
	if err != nil {
		t.Fatalf("resolver(merged): %v", err)
	}
	ms := r1.Resolve(metric)
	if len(ms) != 1 {
		t.Fatalf("after promotion %q resolves to %d groups, want 1: %v", metric, len(ms), ms)
	}
	if ms[0].GroupID != newGroupID {
		t.Errorf("resolved to %q, want %q", ms[0].GroupID, newGroupID)
	}
	if ms[0].Canonical != "db.redis.connected_clients" {
		t.Errorf("canonical = %q, want db.redis.connected_clients", ms[0].Canonical)
	}
}
