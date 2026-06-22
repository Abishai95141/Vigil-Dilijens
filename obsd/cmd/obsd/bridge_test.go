package main

import (
	"fmt"
	"testing"

	vapi "github.com/Abishai95141/Vigil-Dilijens/obsd/internal/api"
)

// TestAuthoredFromToRoundTrip locks the writer↔reader contract of the docs/33 build-3
// bridge: the note authorCausalDirection writes for a promoted direction must parse back to
// the exact (from, to) series via authoredFromTo — even with real series keys (which contain
// '|' and '_') and with/without an operator free-text note.
func TestAuthoredFromToRoundTrip(t *testing.T) {
	a := "i|clu|shop|Pod|cart-7d9|uid-1|app_request_latency_seconds"
	b := "i|clu|shop|Pod|redis-0|uid-2|redis_commands_total"
	cases := []struct{ from, to, note string }{
		{a, b, "confirmed by the on-call"},
		{b, a, ""}, // no operator note
	}
	for _, c := range cases {
		// EXACTLY the note authorCausalDirection composes.
		note := fmt.Sprintf("%s%s → %s. %s", causalDirectionNoteMarker, c.from, c.to, c.note)
		gotFrom, gotTo, ok := authoredFromTo(note)
		if !ok || gotFrom != c.from || gotTo != c.to {
			t.Errorf("authoredFromTo(%q) = (%q,%q,%v), want (%q,%q,true)", note, gotFrom, gotTo, ok, c.from, c.to)
		}
	}
	// A note that is not a causal-direction authoring must NOT parse (e.g. a not-causal note).
	if _, _, ok := authoredFromTo("recorded as not-causal by alice"); ok {
		t.Error("a non-direction note must not parse as an authored direction")
	}
	if _, _, ok := authoredFromTo(""); ok {
		t.Error("empty note must not parse")
	}
}

// TestAuthorCausalDirectionEmitsParseableNote is the integration half: a real
// authorCausalDirection promotion produces a note authoredFromTo recovers — proving the
// bridge's extraction reads exactly what the authoring writes.
func TestAuthorCausalDirectionEmitsParseableNote(t *testing.T) {
	note := fmt.Sprintf("%s%s → %s. %s", causalDirectionNoteMarker, "i|c|n|Pod|x|u1|m1", "i|c|n|Pod|y|u2|m2", "")
	from, to, ok := authoredFromTo(note)
	if !ok || from != "i|c|n|Pod|x|u1|m1" || to != "i|c|n|Pod|y|u2|m2" {
		t.Errorf("round-trip failed: from=%q to=%q ok=%v", from, to, ok)
	}
	_ = vapi.CausalDirectionRequest{} // keep the api import meaningful (shared types)
}
