package graph

import (
	"strings"
	"testing"
)

// baseGraph loads the vendored base KG with NO overlays — a clean slate to test the
// equivalence-group absorb path (doc 21 §5.3) in isolation.
func baseGraph(t *testing.T) *Graph {
	t.Helper()
	g, err := LoadWithOverlays(kgPath, "")
	if err != nil {
		t.Fatalf("load base KG: %v", err)
	}
	return g
}

// An overlay may EXTEND an existing equivalence group with a new dialect pattern — the
// deterministic absorb a promoted stray→group mapping produces. The canonical is untouched.
func TestEquivGroupOverlayExtendsExisting(t *testing.T) {
	g := baseGraph(t)
	const id = "EQG_WORKING_SET"
	eg := g.EquivalenceGroups[id]
	if eg == nil {
		t.Fatalf("base KG missing %s (the test's anchor group)", id)
	}
	before := len(eg.Patterns)
	canon := eg.CanonicalOTel
	raw := []byte(`overlay: t-extend
author: tester
equivalence_groups:
  - id: EQG_WORKING_SET
    add_patterns: ["^myapp_working_set_bytes$"]
    rationale: "myapp exposes the working set under a custom name"
`)
	if err := g.applyOverlay("t-extend.yaml", raw); err != nil {
		t.Fatalf("applyOverlay: %v", err)
	}
	eg = g.EquivalenceGroups[id]
	if len(eg.Patterns) != before+1 {
		t.Fatalf("patterns = %d, want %d", len(eg.Patterns), before+1)
	}
	if eg.CanonicalOTel != canon {
		t.Errorf("canonical changed: %q -> %q (overlays may not redefine)", canon, eg.CanonicalOTel)
	}
	found := false
	for _, p := range eg.Patterns {
		if p == "^myapp_working_set_bytes$" {
			found = true
		}
	}
	if !found {
		t.Error("the new pattern was not absorbed")
	}
}

// An overlay may DEFINE a new equivalence group (label + canonical_otel + patterns) — the
// new-group promotion path. The group becomes resolvable like any base group.
func TestEquivGroupOverlayDefinesNew(t *testing.T) {
	g := baseGraph(t)
	const id = "EQG_REDIS_CONNECTED_CLIENTS"
	if g.EquivalenceGroups[id] != nil {
		t.Fatalf("%s unexpectedly already exists in the base KG", id)
	}
	raw := []byte(`overlay: t-new
author: tester
equivalence_groups:
  - id: EQG_REDIS_CONNECTED_CLIENTS
    label: "Redis connected clients"
    canonical_otel: "db.redis.connected_clients"
    patterns: ["^redis_connected_clients$"]
    rationale: "redis_exporter clients gauge"
`)
	if err := g.applyOverlay("t-new.yaml", raw); err != nil {
		t.Fatalf("applyOverlay: %v", err)
	}
	eg := g.EquivalenceGroups[id]
	if eg == nil {
		t.Fatal("new group not created")
	}
	if eg.Label != "Redis connected clients" || eg.CanonicalOTel != "db.redis.connected_clients" {
		t.Errorf("identity wrong: label=%q canonical=%q", eg.Label, eg.CanonicalOTel)
	}
	if len(eg.Patterns) != 1 || eg.Patterns[0] != "^redis_connected_clients$" {
		t.Errorf("patterns = %v", eg.Patterns)
	}
}

// Defective equivalence-group deltas are HARD errors — a bad delta must never merge.
func TestEquivGroupOverlayDefectsRejected(t *testing.T) {
	cases := []struct {
		name string
		body string
		frag string
	}{
		{"missing rationale", "equivalence_groups:\n  - id: EQG_WORKING_SET\n    add_patterns: [\"^x$\"]\n", "rationale"},
		{"redefine canonical", "equivalence_groups:\n  - id: EQG_WORKING_SET\n    canonical_otel: \"something.else\"\n    add_patterns: [\"^x$\"]\n    rationale: r\n", "may not be redefined"},
		{"patterns on existing", "equivalence_groups:\n  - id: EQG_WORKING_SET\n    patterns: [\"^x$\"]\n    rationale: r\n", "use add_patterns"},
		{"add_patterns on new", "equivalence_groups:\n  - id: EQG_BRAND_NEW\n    add_patterns: [\"^x$\"]\n    rationale: r\n", "use patterns to define it"},
		{"new missing identity", "equivalence_groups:\n  - id: EQG_BRAND_NEW\n    patterns: [\"^x$\"]\n    rationale: r\n", "label + canonical_otel"},
		{"new no pattern", "equivalence_groups:\n  - id: EQG_BRAND_NEW\n    label: l\n    canonical_otel: c\n    rationale: r\n", "at least one pattern"},
		{"bad regex", "equivalence_groups:\n  - id: EQG_WORKING_SET\n    add_patterns: [\"^(unclosed\"]\n    rationale: r\n", "does not compile"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := baseGraph(t)
			raw := []byte("overlay: bad\nauthor: tester\n" + tc.body)
			err := g.applyOverlay("bad.yaml", raw)
			if err == nil {
				t.Fatalf("expected a hard error containing %q, got nil", tc.frag)
			}
			if !strings.Contains(err.Error(), tc.frag) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.frag)
			}
		})
	}
}
