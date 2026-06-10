package main

import "testing"

const (
	schemaPath = "../../ontology/schema/kg.schema.json"
	realKGPath = "../../ontology/graph/k8s_signal_kg.json"
)

func TestValidFixturePassesNoGap(t *testing.T) {
	sch, err := compileSchema(schemaPath)
	if err != nil {
		t.Fatalf("compileSchema: %v", err)
	}
	res, err := lintFile(sch, "testdata/valid_kg.json")
	if err != nil {
		t.Fatalf("lintFile: %v", err)
	}
	if !res.ok() {
		t.Errorf("valid fixture should pass: schema=%v ref=%v", res.schemaErrors, res.refErrors)
	}
	if res.gap.PhenomenaMissingSpan != 0 {
		t.Errorf("valid fixture phenomenon has a span; missing-span = %d, want 0", res.gap.PhenomenaMissingSpan)
	}
}

func TestReferentialViolationFails(t *testing.T) {
	sch, _ := compileSchema(schemaPath)
	res, err := lintFile(sch, "testdata/invalid_ref.json")
	if err != nil {
		t.Fatalf("lintFile: %v", err)
	}
	if res.ok() {
		t.Error("a dangling participates_in endpoint must fail referential integrity")
	}
	if len(res.refErrors) == 0 {
		t.Error("expected a (hard) referential-integrity error on a core edge type")
	}
}

func TestSchemaViolationFails(t *testing.T) {
	sch, _ := compileSchema(schemaPath)
	res, err := lintFile(sch, "testdata/invalid_schema.json")
	if err != nil {
		t.Fatalf("lintFile: %v", err)
	}
	if len(res.schemaErrors) == 0 {
		t.Error("a Signal missing required fields must fail schema validation")
	}
}

// A misspelled edge field (temporl_order) must be REJECTED by the closed edge schema
// rather than silently dropped — otherwise an authored temporal_order/why would be
// lost with no error.
func TestSchemaRejectsTypodEdgeField(t *testing.T) {
	sch, _ := compileSchema(schemaPath)
	res, err := lintFile(sch, "testdata/invalid_edge_field.json")
	if err != nil {
		t.Fatalf("lintFile: %v", err)
	}
	if len(res.schemaErrors) == 0 {
		t.Error("a typo'd edge field must fail schema validation (additionalProperties:false)")
	}
}

// The real 842-node KG must be structurally valid and referentially intact — and
// the gap analysis must surface that ALL 38 phenomena lack a declared span (the
// doc 14 A14 authoring queue) and report the reference scale.
func TestRealKGValidAndGapReported(t *testing.T) {
	sch, err := compileSchema(schemaPath)
	if err != nil {
		t.Fatalf("compileSchema: %v", err)
	}
	res, err := lintFile(sch, realKGPath)
	if err != nil {
		t.Fatalf("lintFile real KG: %v", err)
	}
	// The detection-critical structure must be clean: schema valid + no hard ref
	// errors. (The owned_by_agent agent-name typos surface as warnings, below.)
	if !res.ok() {
		t.Errorf("real KG core structure should be clean:\n schema=%v\n ref=%v", res.schemaErrors, res.refErrors)
	}
	// Any referential warning must be confined to owned_by_agent (organizational).
	for _, w := range res.refWarnings {
		if w.EdgeType != "owned_by_agent" {
			t.Errorf("unexpected core ref warning: %s", w)
		}
	}

	g := res.gap
	if g.PhenomenaTotal != 38 {
		t.Errorf("phenomena total = %d, want 38", g.PhenomenaTotal)
	}
	if g.PhenomenaMissingSpan != 38 || g.PhenomenaWithSpan != 0 {
		t.Errorf("expected all 38 phenomena missing a span (the gap), got missing=%d with=%d", g.PhenomenaMissingSpan, g.PhenomenaWithSpan)
	}
	if g.SignalsTotal != 589 || g.MetricSignals != 457 {
		t.Errorf("signals = %d (metric %d), want 589 (457)", g.SignalsTotal, g.MetricSignals)
	}
	if !g.hasGaps() {
		t.Error("real KG must report authoring gaps (missing spans)")
	}
	if len(g.TemporalVocabulary) < 5 {
		t.Errorf("expected the rich temporal vocabulary, got %v", g.TemporalVocabulary)
	}
}
