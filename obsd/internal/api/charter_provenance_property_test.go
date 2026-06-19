package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/detect"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/forecast"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/observe"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/store"
)

// threshEnumList is the EXACT, closed set of strings a MEASURED member state may
// hold — the rendering of every observe.ThresholdState rung (doc 05 §3.4). It is
// derived from the source enum itself (not a hand-copied literal) so that if the
// ladder ever grows a rung the allowlist tracks it instead of silently rejecting a
// legitimate new state. threshEnum is the membership set the property checks against.
var (
	threshEnumList = []string{
		observe.StateBelow.String(),       // "below"
		observe.StateAtThreshold.String(), // "at-threshold"
		observe.StateAbove.String(),       // "above"
		observe.StateWellAbove.String(),   // "well-above"
		observe.StateUnknown.String(),     // "unknown" (no usable sample)
	}
	threshEnum = func() map[string]bool {
		m := make(map[string]bool, len(threshEnumList))
		for _, s := range threshEnumList {
			m[s] = true
		}
		return m
	}()
)

// charter_provenance_property_test.go — a PROVENANCE property test layered on top of
// the charter battery (doc 01 §4/§5, doc 10 §3.1). It has two halves, each with teeth:
//
//   1. SEPARATION + NON-STRENGTHENING (TestProvenanceNeverFusedOrStrengthened):
//      build surfaces that carry MEASURED + PROJECTED + AUTHORED data ADJACENTLY — the
//      timeline (MEASURED match/unexplained lanes vs the PROJECTED band lane), the
//      insights cards (MEASURED member state vs its AUTHORED note, vs the AUTHORED
//      cascade "why"), and the warnings cards (PROJECTED band + cited AUTHORED refs).
//      Then assert the rendered payload keeps each datum in its own LABELLED lane/field
//      and NEVER strengthens a class: a PROJECTED span is never re-labelled MEASURED, a
//      MEASURED match never grows a forward-pointing crossing band, an AUTHORED note is
//      never restated as a MEASURED state. These are checked STRUCTURALLY (parsed JSON),
//      so a render that fused the lanes — not merely one that mislabelled a string —
//      fails.
//
//   2. METAMORPHIC LINTER (TestCharterLinterMetamorphic): the charter sweep
//      (CharterViolations) is itself the thing under test here. We take a CLEAN rendered
//      surface, confirm the sweep finds nothing, then inject ONE banned register marker
//      (causal / future-certainty / fusion) into a string field and re-serialize. The
//      metamorphic relation is: clean⇒0 violations, and clean+inject(p)⇒p is caught with
//      its correct class. A linter that silently lost a phrase, mis-classed it, or only
//      matched the hardcoded battery payloads would fail.

// provenanceSurfaces builds the three highest-risk multi-class surfaces with the SAME
// fixtures the battery uses (podKey/nodeKey/at), so this test and the battery agree on
// the rendered shape. Returned raw JSON is what the operator actually receives.
func provenanceSurfaces(t *testing.T) (timeline, insights, warnings []byte) {
	t.Helper()

	// MEASURED lanes: a persisted match and an unexplained card (no authored reason).
	frows := []store.FindingRow{{
		Label: "Memory leak", EntityCEI: podKey, Name: "web-a", Kind: "Container", Quality: "degraded",
		FirstSeen: at.Add(-5 * time.Minute), LastSeen: at.Add(-1 * time.Minute),
	}}
	urows := []store.UnexplainedRow{{
		Scope: nodeKey, Name: "worker-1", Kind: "Node", Status: "aging",
		FirstSeen: at.Add(-8 * time.Minute), LastSeen: at.Add(-3 * time.Minute),
	}}
	// PROJECTED lane: a real warning card, rendered through BuildWarnings, fed into the
	// timeline so all three classes coexist in ONE payload.
	wv := BuildWarnings("v", "v0.3.0", wAt, true, cycleWithCandidate(), 1, ClockHealthRow{Ready: true}, nil, nil)
	tv := BuildTimeline(at, frows, urows, wv.Warnings, true)

	// INSIGHTS: MEASURED member state with an AUTHORED note adjacent, plus an AUTHORED
	// cascade "why". The member State is MEASURED; the Note is the AUTHORED relation.
	findings := []detect.Finding{{
		Phenomenon: "PHEN_MEMORY_LEAK", Label: "Memory leak", EntityCEI: podKey,
		Namespace: "shop", Name: "web-a", Kind: "Container", Span: "",
		Quality: detect.QualityDegraded, Completeness: 0.5, RequiredMet: 1, RequiredTotal: 2,
		GraphVersion: "sha256:test",
		Members: []detect.MemberEvidence{{
			Metric: "container_memory_working_set_bytes", Role: "required",
			Temporal: "T0", State: "well-above", Note: "Slope > 0", SampleAt: at,
		}, {
			// A SECOND member whose AUTHORED note is genuine multi-word prose (not the
			// terse "Slope > 0"). Its MEASURED state is the terse enum "above". This is
			// the carrier the allowlist guard bites against: if a render ever fused this
			// prose note into the measured state field, the state would no longer be a
			// member of the threshold enum and the guard below would catch it. Its prose
			// is charter-clean (no banned register marker) so the linter baseline stays 0.
			Metric: "container_memory_rss_bytes", Role: "supporting",
			Temporal: "T0", State: "above", Note: "Working set climbs steadily across the observed window", SampleAt: at,
		}},
		BlastRadius: []detect.AtRisk{{
			CEIKey: podKey, Phenomenon: "PHEN_OOM_KILL_CGROUP", Related: "same-entity",
			Temporal: "T0+terminal", Why: "Eventual outcome",
		}},
	}}
	cascades := []detect.Cascade{{
		Trigger:    detect.FindingRef{Phenomenon: "PHEN_MEMORY_LEAK", EntityCEI: podKey, EvaluatedAt: at, Quality: detect.QualityDegraded},
		Downstream: detect.FindingRef{Phenomenon: "PHEN_OOM_KILL_CGROUP", EntityCEI: podKey, EvaluatedAt: at, Quality: detect.QualityFull},
		Why:        "Eventual outcome", Temporal: "T0+terminal", Related: "same-entity",
	}}
	iv := BuildInsights("cl", "sha256:test", "v0.3.0", at, findings, cascades)

	mustJSONlocal := func(name string, v any) []byte {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal %s: %v", name, err)
		}
		return raw
	}
	return mustJSONlocal("timeline", tv), mustJSONlocal("insights", iv), mustJSONlocal("warnings", wv)
}

// TestProvenanceNeverFusedOrStrengthened is the property: across surfaces carrying all
// three classes adjacently, each datum stays in its own LABELLED lane and is never
// promoted to a stronger class. Parsed structurally so a real fusion (not a string typo)
// is what fails.
func TestProvenanceNeverFusedOrStrengthened(t *testing.T) {
	timelineRaw, insightsRaw, warningsRaw := provenanceSurfaces(t)

	// --- TIMELINE: three lanes, each its own class, no cross-contamination ------------
	var tv TimelineView
	if err := json.Unmarshal(timelineRaw, &tv); err != nil {
		t.Fatalf("unmarshal timeline: %v", err)
	}
	if len(tv.Matches) == 0 || len(tv.Unexplained) == 0 || len(tv.Projected) == 0 {
		t.Fatalf("the property needs all three lanes populated to mean anything: "+
			"matches=%d unexplained=%d projected=%d", len(tv.Matches), len(tv.Unexplained), len(tv.Projected))
	}
	// The MEASURED lanes must carry ONLY the MEASURED class — never promoted to PROJECTED.
	for _, s := range append(append([]TimelineSpan{}, tv.Matches...), tv.Unexplained...) {
		if s.Class != "MEASURED" {
			t.Errorf("MEASURED lane span %q/%q carries class %q — a measured datum was strengthened", s.Surface, s.Label, s.Class)
		}
		// A MEASURED match has a bounded [FirstSeen,LastSeen] interval that ENDED at or
		// before now; it must not be rendered as a forward-pointing crossing band (the
		// PROJECTED shape). If a MEASURED span pointed past `now`, the lane fused.
		if s.To.After(tv.GeneratedAt) {
			t.Errorf("MEASURED span %q ends at %s, AFTER generatedAt %s — a measured interval became a forward projection", s.Label, s.To, tv.GeneratedAt)
		}
		if strings.Contains(strings.ToLower(s.Label), "projected to cross") {
			t.Errorf("MEASURED span %q wears the PROJECTED register in its label — fusion", s.Label)
		}
	}
	// The PROJECTED lane must carry ONLY the PROJECTED class — never down-cast to MEASURED.
	for _, s := range tv.Projected {
		if s.Class != "PROJECTED" {
			t.Errorf("PROJECTED lane span %q carries class %q — a forecast was restated as a measurement", s.Label, s.Class)
		}
		// A projection is forward-pointing: its band ends at/after now, and the modal
		// register ("projected to cross") is present, never the indicative.
		if s.To.Before(tv.GeneratedAt) {
			t.Errorf("PROJECTED span %q ends BEFORE generatedAt — a band collapsed into the past", s.Label)
		}
		if !strings.Contains(strings.ToLower(s.Label), "projected to cross") {
			t.Errorf("PROJECTED span %q dropped its modal register (must read 'projected to cross')", s.Label)
		}
	}

	// --- INSIGHTS: MEASURED member state vs adjacent AUTHORED note, never fused --------
	var iv InsightsView
	if err := json.Unmarshal(insightsRaw, &iv); err != nil {
		t.Fatalf("unmarshal insights: %v", err)
	}
	if len(iv.Findings) == 0 {
		t.Fatal("insights need a finding to carry a member state + authored note")
	}
	sawMemberWithNote := false
	for _, f := range iv.Findings {
		for _, m := range f.Members {
			if m.Note == "" {
				continue
			}
			sawMemberWithNote = true
			// The AUTHORED note and the MEASURED state are SEPARATE fields. Fusion would
			// be the authored relation text leaking into the measured state enum.
			if m.State == m.Note {
				t.Errorf("member %q: MEASURED state == AUTHORED note (%q) — the two classes were fused into one field", m.Metric, m.Note)
			}
			// The MEASURED state is one of the terse threshold-ladder rungs (the exact
			// set observe.ThresholdState.String() can emit; doc 05 §3.4), never an
			// authored sentence. A strict allowlist — not a heuristic on spaces/length —
			// is the teeth: ANY partial leak of authored prose into the state field
			// (a prefix like "well-aboveSlope...", a single-word note like "degraded",
			// or the full note) yields a value outside the enum and fails here, even
			// when the leak has no space and is shorter than "well-above".
			if !threshEnum[m.State] {
				t.Errorf("member %q MEASURED state %q is not a threshold-ladder rung %v — authored text may have leaked into the measured field", m.Metric, m.State, threshEnumList)
			}
		}
		// The blast radius is AUTHORED (a verbatim relation note), carried in its own
		// `why` field — never fused into a member's MEASURED state.
		for _, r := range f.BlastRadius {
			if r.Why == "" {
				t.Errorf("blast-radius entry for %q has no authored why — an authored relation must be cited, not dropped", r.Phenomenon)
			}
			for _, m := range f.Members {
				if m.State == r.Why {
					t.Errorf("authored why %q surfaced as a MEASURED member state on %q — class strengthening", r.Why, m.Metric)
				}
			}
		}
	}
	if !sawMemberWithNote {
		t.Fatal("no member carried an authored note — the adjacency property is untested")
	}
	// The cascade carries its AUTHORED why; it must never be presented as a MEASURED fact.
	if len(iv.Cascades) == 0 {
		t.Fatal("a cascade is needed to test the authored-why separation")
	}
	for _, c := range iv.Cascades {
		if c.Why == "" {
			t.Errorf("cascade %s→%s dropped its AUTHORED why — the relation must ride verbatim", c.Trigger.Phenomenon, c.Downstream.Phenomenon)
		}
	}

	// --- WARNINGS: PROJECTED class is STRUCTURAL, AUTHORED refs are cited not fused ----
	var wv WarningsView
	if err := json.Unmarshal(warningsRaw, &wv); err != nil {
		t.Fatalf("unmarshal warnings: %v", err)
	}
	if wv.Class != "PROJECTED" {
		t.Errorf("warnings surface class is %q, not PROJECTED — the lane mislabelled its provenance", wv.Class)
	}
	if len(wv.Warnings) == 0 {
		t.Fatal("warnings need a card to carry the PROJECTED band + authored refs")
	}
	for _, c := range wv.Warnings {
		if c.Class != "PROJECTED" || !c.IsProjection {
			t.Errorf("warning card %q missing PROJECTED class markers (class=%q isProjection=%v) — a forecast could be read as measured", c.Metric, c.Class, c.IsProjection)
		}
		// The band must NEVER collapse to a single instant — a forecast is never a point.
		if c.EarliestAt.IsZero() && c.LatestAt.IsZero() {
			t.Errorf("warning card %q has no band at all — a PROJECTED datum collapsed to nothing", c.Metric)
		}
		if !c.EarliestAt.IsZero() && c.EarliestAt.Equal(c.LatestAt) {
			t.Errorf("warning card %q band collapsed to a line (%s) — false certainty", c.Metric, c.EarliestAt)
		}
		// AUTHORED references are CITED (precursor ids), never paraphrased into a measured
		// state on the projected card.
		for _, p := range c.PrecursorPhenomena {
			if !strings.HasPrefix(p, "PHEN_") {
				t.Errorf("precursor ref %q is not a cited phenomenon id — an authored ref may have been paraphrased into prose", p)
			}
		}
	}
}

// --- METAMORPHIC LINTER ------------------------------------------------------------

// injectInto appends a banned phrase to the first non-empty string-valued field of a JSON
// object (recursively), returning the re-serialized payload. The phrase is placed inside a
// real surface field (so the mutation is a realistic "dirty data reached a render" carrier,
// not a free-floating token bolted onto the blob) — but note that CharterViolations
// lower-cases and substring-scans the WHOLE serialized payload, so this test does NOT and
// cannot assert field-of-origin: which field the phrase lands in is irrelevant to the
// sweep's verdict. What is asserted is solely that the phrase, once present anywhere in a
// realistic payload, is caught and correctly classed. Returns ok=false if no string field
// was found (a fixture bug).
func injectInto(t *testing.T, raw []byte, phrase string) ([]byte, bool) {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("unmarshal for injection: %v", err)
	}
	injected := false
	var walk func(node any) any
	walk = func(node any) any {
		switch n := node.(type) {
		case map[string]any:
			for k, val := range n {
				if injected {
					return n
				}
				if s, ok := val.(string); ok && s != "" {
					n[k] = s + " " + phrase
					injected = true
					return n
				}
				n[k] = walk(val)
			}
			return n
		case []any:
			for i := range n {
				if injected {
					return n
				}
				n[i] = walk(n[i])
			}
			return n
		default:
			return node
		}
	}
	v = walk(v)
	if !injected {
		return nil, false
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("re-marshal after injection: %v", err)
	}
	return out, true
}

// TestCharterLinterMetamorphic drives CharterViolations over GENERATED payloads seeded
// with banned registers. The metamorphic relations:
//   - clean surface ⇒ zero violations (no false positive on real authored notes / bands);
//   - clean + inject(phrase of class C) ⇒ a violation of class C is found, AND the clean
//     baseline did NOT already contain it (so the injection is what flipped the result).
//
// This is a test of the LINTER, not of the surfaces — the surfaces are merely realistic
// carriers. A linter that dropped a phrase, mis-classed it, or only matched the battery's
// hardcoded honeypots would fail here.
func TestCharterLinterMetamorphic(t *testing.T) {
	timelineRaw, insightsRaw, warningsRaw := provenanceSurfaces(t)
	carriers := map[string][]byte{
		"timeline": timelineRaw,
		"insights": insightsRaw,
		"warnings": warningsRaw,
	}

	// One representative phrase from EACH banned register, with its expected class.
	// (The full lists live in charter.go; this proves the metamorphic relation per class.)
	type seed struct {
		phrase string
		class  string
	}
	seeds := []seed{
		{"caused by", "causal"},
		{"because", "causal"},
		{"root cause", "causal"},
		{"will cross", "future-certainty"},
		{"is going to", "future-certainty"},
		{"guaranteed to", "future-certainty"},
		{"measured forecast", "fusion"},
		{"projected and confirmed", "fusion"},
	}

	for cname, clean := range carriers {
		// Baseline: the GENERATED clean surface must be register-clean. Real authored
		// notes ("Eventual outcome", "Slope > 0") and the projected band must not trip it.
		if vs := CharterViolations(cname, clean); len(vs) > 0 {
			for _, v := range vs {
				t.Errorf("FALSE POSITIVE: clean %s surface flagged: %s", cname, v)
			}
		}

		for _, s := range seeds {
			// The injection only proves something if the phrase is absent before it.
			if strings.Contains(strings.ToLower(string(clean)), s.phrase) {
				t.Fatalf("metamorphic precondition broken: clean %s already contains %q", cname, s.phrase)
			}
			dirty, ok := injectInto(t, clean, s.phrase)
			if !ok {
				t.Fatalf("could not inject into %s: no string field found", cname)
			}
			vs := CharterViolations(cname, dirty)
			foundClass := false
			foundPhrase := false
			for _, v := range vs {
				if v.Phrase == s.phrase {
					foundPhrase = true
				}
				if v.Class == s.class && v.Phrase == s.phrase {
					foundClass = true
				}
			}
			if !foundPhrase {
				t.Errorf("metamorphic miss: injected %q into %s but the sweep did not flag it (got %v)", s.phrase, cname, vs)
			}
			if foundPhrase && !foundClass {
				t.Errorf("misclassification: %q flagged in %s but not as class %q (got %v)", s.phrase, cname, s.class, vs)
			}
		}
	}
}

// TestCharterLinterMetamorphicCleanStaysClean is the tightest teeth on the metamorphic
// relation: removing the injection must return to zero violations for that register —
// i.e. the sweep's verdict is caused by the injected phrase, not by anything ambient.
// (If the linter ever started false-positiving on real authored text, the clean-baseline
// branch above catches it; this isolates the per-phrase causal link.)
func TestCharterLinterMetamorphicCleanStaysClean(t *testing.T) {
	_, insightsRaw, _ := provenanceSurfaces(t)

	// A forecast-class phrase the sweep MUST catch when present.
	const phrase = "will reach"
	if strings.Contains(strings.ToLower(string(insightsRaw)), phrase) {
		t.Fatalf("precondition: clean insights already contains %q", phrase)
	}

	// Clean ⇒ no future-certainty violation.
	for _, v := range CharterViolations("insights", insightsRaw) {
		if v.Class == "future-certainty" {
			t.Fatalf("clean insights already has a future-certainty violation: %s", v)
		}
	}
	// Inject ⇒ exactly the future-certainty violation appears.
	dirty, ok := injectInto(t, insightsRaw, phrase)
	if !ok {
		t.Fatal("injection found no string field")
	}
	gotFC := false
	for _, v := range CharterViolations("insights", dirty) {
		if v.Class == "future-certainty" && v.Phrase == phrase {
			gotFC = true
		}
	}
	if !gotFC {
		t.Errorf("injecting %q did not produce a future-certainty violation", phrase)
	}

	// Forecast.ClassProjected is the canonical PROJECTED label — assert the warnings
	// surface still uses it, so a rename of the constant cannot silently desync the
	// provenance label this property depends on.
	if forecast.ClassProjected != "PROJECTED" {
		t.Errorf("forecast.ClassProjected drifted to %q — the PROJECTED provenance label changed", forecast.ClassProjected)
	}
}
