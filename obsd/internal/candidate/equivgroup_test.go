package candidate

import (
	"strings"
	"testing"
	"time"
)

func TestValidateEquivGroupProposal(t *testing.T) {
	good := EquivGroupProposal{Metric: "redis_connected_clients", Pattern: "^redis_connected_clients$",
		NewGroup: &NewEquivGroup{ID: "EQG_REDIS", Label: "Redis clients", CanonicalOTel: "db.redis.connected_clients"}}
	if err := ValidateEquivGroupProposal(good); err != nil {
		t.Fatalf("good proposal rejected: %v", err)
	}
	existing := EquivGroupProposal{Metric: "redis_connected_clients", Pattern: "^redis_connected_clients$", GroupID: "EQG_REDIS"}
	if err := ValidateEquivGroupProposal(existing); err != nil {
		t.Fatalf("good existing-group proposal rejected: %v", err)
	}

	bad := []struct {
		name string
		p    EquivGroupProposal
		frag string
	}{
		{"empty metric", EquivGroupProposal{Pattern: "^x$", GroupID: "EQG_X"}, "empty metric"},
		{"empty pattern", EquivGroupProposal{Metric: "m", GroupID: "EQG_X"}, "empty pattern"},
		{"bad regex", EquivGroupProposal{Metric: "m", Pattern: "^(unclosed", GroupID: "EQG_X"}, "does not compile"},
		{"pattern misses metric", EquivGroupProposal{Metric: "redis_x", Pattern: "^mysql_y$", GroupID: "EQG_X"}, "does not match its own metric"},
		{"both targets", EquivGroupProposal{Metric: "m", Pattern: "^m$", GroupID: "EQG_X", NewGroup: &NewEquivGroup{ID: "EQG_Y", Label: "l", CanonicalOTel: "c"}}, "exactly one"},
		{"no target", EquivGroupProposal{Metric: "m", Pattern: "^m$"}, "exactly one"},
		{"new missing canonical", EquivGroupProposal{Metric: "m", Pattern: "^m$", NewGroup: &NewEquivGroup{ID: "EQG_Y", Label: "l"}}, "id + label + canonical_otel"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateEquivGroupProposal(tc.p)
			if err == nil || !strings.Contains(err.Error(), tc.frag) {
				t.Errorf("want error containing %q, got %v", tc.frag, err)
			}
		})
	}
}

// The deterministic SUPPORT: which strays a pattern would also capture — a count of facts,
// stable + sorted, never a learned score.
func TestEquivGroupSupport(t *testing.T) {
	strays := []string{"redis_connected_clients", "redis_blocked_clients", "mysql_threads_connected", "redis_connected_clients"}
	got, err := EquivGroupSupport("^redis_.*", strays)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"redis_blocked_clients", "redis_connected_clients"} // sorted, deduped
	if len(got) != len(want) {
		t.Fatalf("support = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("support[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if _, err := EquivGroupSupport("^(bad", strays); err == nil {
		t.Error("a non-compiling pattern must error, not return an empty set")
	}
}

func TestEquivGroupPayloadRoundTrip(t *testing.T) {
	in := EquivGroupProposal{
		Metric: "redis_connected_clients", Pattern: "^redis_connected_clients$",
		NewGroup:      &NewEquivGroup{ID: "EQG_REDIS", Label: "Redis clients", CanonicalOTel: "db.redis.connected_clients"},
		CaptureSample: []string{"redis_connected_clients"},
	}
	out, err := ParseEquivGroupPayload(EquivGroupPayload(in))
	if err != nil {
		t.Fatal(err)
	}
	if out.Metric != in.Metric || out.Pattern != in.Pattern {
		t.Errorf("metric/pattern lost: %+v", out)
	}
	if out.NewGroup == nil || out.NewGroup.ID != "EQG_REDIS" || out.NewGroup.CanonicalOTel != "db.redis.connected_clients" {
		t.Errorf("new_group lost: %+v", out.NewGroup)
	}
	if len(out.CaptureSample) != 1 || out.CaptureSample[0] != "redis_connected_clients" {
		t.Errorf("capture_sample lost: %v", out.CaptureSample)
	}
}

// A promoted equiv_group candidate renders a REAL equivalence_groups overlay block (the
// absorb artifact), routed through the generic PromotedOverlayYAML entry point. It refuses
// a candidate with no authored note (the rationale a falsifiable delta requires).
func TestPromotedEquivGroupOverlay(t *testing.T) {
	mk := func(note string) Candidate {
		return Candidate{
			ID: "cand:abc123def456", Kind: KindEquivGroup, Status: StatusPromoted,
			DecidedBy: "alice", Note: note, DecidedAt: time.Date(2026, 6, 19, 0, 0, 0, 0, time.UTC),
			Subject: EquivGroupSubject("redis_connected_clients"),
			Payload: EquivGroupPayload(EquivGroupProposal{
				Metric: "redis_connected_clients", Pattern: "^redis_connected_clients$",
				NewGroup: &NewEquivGroup{ID: "EQG_REDIS", Label: "Redis clients", CanonicalOTel: "db.redis.connected_clients"},
			}),
		}
	}
	// Empty note is refused.
	if _, err := PromotedOverlayYAML(mk(""), "sha256:test"); err == nil || !strings.Contains(err.Error(), "authored note") {
		t.Fatalf("empty note must be refused, got %v", err)
	}
	out, err := PromotedOverlayYAML(mk("redis exporter clients gauge"), "sha256:test")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"equivalence_groups:", "EQG_REDIS", "db.redis.connected_clients", "^redis_connected_clients$", "author: alice", "rationale: redis exporter clients gauge"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered overlay missing %q:\n%s", want, out)
		}
	}
	// A non-equiv_group candidate must NOT hit this renderer.
	other := Candidate{ID: "cand:x", Kind: KindEdge, Status: StatusPromoted, DecidedBy: "bob", Subject: "a~>b", Relation: "associated-with"}
	if _, err := PromotedEquivGroupOverlayYAML(other, "v"); err == nil || !strings.Contains(err.Error(), "not") {
		t.Errorf("wrong-kind candidate must be refused, got %v", err)
	}
}
