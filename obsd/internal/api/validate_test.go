package api

import (
	"encoding/json"
	"testing"
)

// tctx is a representative claim context: two authored relations from the real graph
// (the leak→OOM cascade and the cross-service relation), plus a projected-only subject.
func tctx() ClaimContext {
	return ClaimContext{
		Phenomena: map[string][]string{
			"PHEN_MEMORY_LEAK":           {"memory leak"},
			"PHEN_OOM_KILL_CGROUP":       {"oom kill"},
			"PHEN_NETWORK_IMPAIRMENT":    {"network impairment"},
			"PHEN_THROTTLING_CASCADE":    {"throttling cascade"},
			"PHEN_PROBE_FAILURE_RESTART": {"probe failure restart"},
		},
		AuthoredLinks: []AuthoredLink{
			{Src: "PHEN_MEMORY_LEAK", Dst: "PHEN_OOM_KILL_CGROUP", Why: "Eventual outcome"},
			{Src: "PHEN_THROTTLING_CASCADE", Dst: "PHEN_PROBE_FAILURE_RESTART", Why: "Probe cascade"},
		},
		Projected: []string{"cartservice"},
		Measured:  []string{"currencyservice"},
	}
}

func verdict(t *testing.T, claim string) ClaimVerdict {
	t.Helper()
	return ValidateClaim(claim, tctx(), ClaimOpts{})
}

// FALSE-BLOCK==0 is the cardinal rule. These legit claims must NEVER be flagged.
func TestLegitClaimsNeverFlagged(t *testing.T) {
	legit := []string{
		// surfacing an authored relation properly (as authored)
		"Per the authored graph, the memory leak is the authored precursor of the oom kill.",
		// a clumsy causal restatement of a REAL authored relation — rescued, not flagged
		"The memory leak led to the oom kill on currencyservice.",
		// a legitimate banded projection (shares 'cross' vocabulary with fabrications)
		"cartservice is projected to cross its memory limit between 10:09 and 10:22 (a band, not a certainty).",
		// a true MEASURED statement about a MEASURED subject
		"currencyservice has crossed its memory limit (MEASURED now).",
		// honest no-cause
		"I do not assert causes; the graph relates the memory leak to the oom kill as an authored outcome.",
		// two phenomena co-mentioned with NO causal cue
		"We currently see both a memory leak and an oom kill on currencyservice.",
	}
	for _, c := range legit {
		v := verdict(t, c)
		if v.Flagged {
			t.Errorf("FALSE-BLOCK: legit claim was flagged: %q\n  reasons: %+v", c, v.Reasons)
		}
		if !v.LabelledBestEffort {
			t.Errorf("verdict must be labelled best-effort: %q", c)
		}
	}
}

func TestRelationAbsentCausationFlagged(t *testing.T) {
	// network impairment -> memory leak is NOT an authored relation.
	v := verdict(t, "The network impairment caused the memory leak on the node.")
	if !v.Flagged {
		t.Fatalf("a generated cause with no authored basis must be flagged: %+v", v)
	}
	if !hasClass(v, "generated-causation") {
		t.Errorf("expected a generated-causation finding, got %+v", v.Reasons)
	}
}

func TestAuthoredCausalRescued(t *testing.T) {
	v := verdict(t, "The memory leak led to the oom kill.")
	if v.Flagged {
		t.Fatalf("an authored relation restated causally must NOT be flagged: %+v", v)
	}
	if !v.MatchedAuthored {
		t.Error("the verdict should record that an authored relation was matched")
	}
	if !hasClass(v, "advisory") {
		t.Error("expected an advisory finding telling the consumer to surface it as authored")
	}
}

func TestClassFusionFlagged(t *testing.T) {
	// cartservice is PROJECTED-only; asserting it has ALREADY crossed is class fusion.
	v := verdict(t, "cartservice has already crossed its memory limit.")
	if !v.Flagged || !hasClass(v, "class-fusion") {
		t.Fatalf("a PROJECTED subject asserted as MEASURED must be flagged class-fusion: %+v", v)
	}
}

func TestFutureCertaintyFlagged(t *testing.T) {
	v := verdict(t, "currencyservice will cross its memory limit in ten minutes.")
	if !v.Flagged {
		t.Fatalf("a future certainty must be flagged: %+v", v)
	}
}

// TestStructuralHoneypot: a generated cause phrased WITHOUT any charter-denylist
// substring, caught ONLY by the structural backstop (the structural extractor's cue
// vocabulary is broader than the denylist).
func TestStructuralHoneypot(t *testing.T) {
	v := verdict(t, "The network impairment knocked out the memory leak detector — the leak set off the oom kill.")
	if !v.Flagged {
		t.Fatalf("a structural honeypot must still be caught: %+v", v)
	}
}

// TestMutationStructuralOnly: with the substring backstop OFF, the structural backstop
// must still catch a relation-absent fabrication (it is not merely riding the denylist).
func TestMutationStructuralOnly(t *testing.T) {
	v := ValidateClaim("The network impairment caused the memory leak.", tctx(), ClaimOpts{DisableSubstring: true})
	if !v.Flagged || !hasClass(v, "generated-causation") {
		t.Fatalf("structural-only mode must still flag relation-absent causation: %+v", v)
	}
}

func TestNeverBlocksSerialization(t *testing.T) {
	// The verdict carries no block directive — the JSON has no "blocked" field.
	b, _ := json.Marshal(verdict(t, "The network impairment caused the memory leak."))
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if _, ok := m["blocked"]; ok {
		t.Error("a referee verdict must carry no block directive (NEVER-BLOCK is structural)")
	}
}

func TestDeterministic(t *testing.T) {
	a, _ := json.Marshal(verdict(t, "The memory leak led to the oom kill; the network impairment caused the leak."))
	b, _ := json.Marshal(verdict(t, "The memory leak led to the oom kill; the network impairment caused the leak."))
	if string(a) != string(b) {
		t.Fatal("ValidateClaim is not deterministic across two identical calls")
	}
}

// TestHardening pins the fixes the 27-exploit adversarial review surfaced. Each was a
// real defect in the first cut of the referee.
func TestHardening(t *testing.T) {
	// (A) Passive/reverse cue DIRECTION: "B was caused by A" maps A→B.
	if verdict(t, "The oom kill was caused by the memory leak.").Flagged {
		t.Error("passive restatement of the authored leak→oom must be rescued, not flagged")
	}
	if !verdict(t, "The memory leak was caused by the oom kill.").Flagged {
		t.Error("passive FABRICATION (oom→leak) must be flagged, not rescued")
	}
	// (B) Class-fusion bound to its subject: a crossing about a MEASURED subject must not
	// false-block on a co-mentioned PROJECTED subject.
	if verdict(t, "currencyservice has crossed its memory limit; cartservice remains only projected.").Flagged {
		t.Error("a crossing about the measured subject must not flag the projected co-mention")
	}
	// (C) Negation: a negated crossing / negated causation is not a claim.
	if verdict(t, "cartservice has not crossed its limit; it is only projected to.").Flagged {
		t.Error("a NEGATED crossing must not be class-fusion")
	}
	if verdict(t, "The memory leak and the oom kill are measured; neither is responsible for the network impairment.").Flagged {
		t.Error("a NEGATED causal assertion must not be a generated cause")
	}
	// (D) Rescue is per-cue + all-occurrence: an authored clause must not rescue a
	// fabricated clause beside it.
	v := verdict(t, "The memory leak led to the oom kill; separately the network impairment caused the memory leak.")
	if !v.Flagged || !hasClass(v, "generated-causation") {
		t.Errorf("rescue-bleed: the network→leak fabrication must still flag: %+v", v)
	}
	if !v.MatchedAuthored {
		t.Error("the authored leak→oom in the same claim should still be recorded as matched")
	}
	// (E) Word-boundary subject match: a projected subject ("cart") must not match inside a
	// larger single token ("cartservice"), which is MEASURED here — so no false fusion.
	ctxWB := ClaimContext{Phenomena: tctx().Phenomena, AuthoredLinks: tctx().AuthoredLinks, Projected: []string{"cart"}, Measured: []string{"cartservice"}}
	if ValidateClaim("cartservice has crossed its memory limit (measured now).", ctxWB, ClaimOpts{}).Flagged {
		t.Error("'cart' must not match inside 'cartservice' (word-boundary guard)")
	}
}

func hasClass(v ClaimVerdict, class string) bool {
	for _, r := range v.Reasons {
		if r.Class == class {
			return true
		}
	}
	return false
}
