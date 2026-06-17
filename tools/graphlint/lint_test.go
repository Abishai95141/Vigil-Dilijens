package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	schemaPath     = "../../ontology/schema/kg.schema.json"
	realKGPath     = "../../ontology/graph/k8s_signal_kg.json"
	realOverlayDir = "../../ontology/graph/overlays"
)

func TestValidFixturePassesNoGap(t *testing.T) {
	sch, err := compileSchema(schemaPath)
	if err != nil {
		t.Fatalf("compileSchema: %v", err)
	}
	res, err := lintFile(sch, "testdata/valid_kg.json", nil)
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
	res, err := lintFile(sch, "testdata/invalid_ref.json", nil)
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
	res, err := lintFile(sch, "testdata/invalid_schema.json", nil)
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
	res, err := lintFile(sch, "testdata/invalid_edge_field.json", nil)
	if err != nil {
		t.Fatalf("lintFile: %v", err)
	}
	if len(res.schemaErrors) == 0 {
		t.Error("a typo'd edge field must fail schema validation (additionalProperties:false)")
	}
}

// WITHOUT overlays the base KG's authoring gap must stay visible: all 38 phenomena
// lack a span in the base release (doc 02 §3.6 — never defaulted, only authored).
// After the agent-name curation, the base graph carries no referential warnings.
func TestRealKGBaseGapStillReportedWithoutOverlays(t *testing.T) {
	sch, err := compileSchema(schemaPath)
	if err != nil {
		t.Fatalf("compileSchema: %v", err)
	}
	res, err := lintFile(sch, realKGPath, nil)
	if err != nil {
		t.Fatalf("lintFile real KG: %v", err)
	}
	if !res.ok() {
		t.Errorf("real KG core structure should be clean:\n schema=%v\n ref=%v", res.schemaErrors, res.refErrors)
	}
	if res.refWarnTotal != 0 {
		t.Errorf("owned_by_agent warnings = %d, want 0 after the agent-name curation; e.g. %v", res.refWarnTotal, res.refWarnings)
	}
	g := res.gap
	if g.PhenomenaTotal != 38 || g.PhenomenaMissingSpan != 38 || g.PhenomenaWithSpan != 0 {
		t.Errorf("base gap = total %d / missing %d / with %d, want 38/38/0", g.PhenomenaTotal, g.PhenomenaMissingSpan, g.PhenomenaWithSpan)
	}
	if !g.hasGaps() {
		t.Error("base KG without overlays must report the span gap")
	}
	if g.SignalsTotal != 589 || g.MetricSignals != 457 {
		t.Errorf("signals = %d (metric %d), want 589 (457)", g.SignalsTotal, g.MetricSignals)
	}
	if len(g.TemporalVocabulary) < 5 {
		t.Errorf("expected the rich temporal vocabulary, got %v", g.TemporalVocabulary)
	}
}

// WITH the authored overlays the gap is closed: 38/38 spans declared, structured
// threshold rules attached, zero remaining gaps — the -strict gate passes honestly
// because the content was authored, not defaulted.
func TestRealKGWithOverlaysStrictClean(t *testing.T) {
	sch, err := compileSchema(schemaPath)
	if err != nil {
		t.Fatalf("compileSchema: %v", err)
	}
	ovls, err := loadOverlays(realOverlayDir)
	if err != nil {
		t.Fatalf("loadOverlays: %v", err)
	}
	// The production overlay glob (experimental/ excluded): cross-service-v0, spans-v1,
	// threshold-rules-v1..v6, detect-conditions-v1..v7 = 15 (G2/G2b/DISK/PVC added the KSM
	// object-state lane overlays).
	if len(ovls) != 15 {
		t.Fatalf("overlays = %d, want 15", len(ovls))
	}
	res, err := lintFile(sch, realKGPath, ovls)
	if err != nil {
		t.Fatalf("lintFile: %v", err)
	}
	if !res.ok() {
		t.Errorf("merged lint should be clean: schema=%v ref=%v overlay=%v", res.schemaErrors, res.refErrors, res.overlayErrors)
	}
	g := res.gap
	// 40 after v0.4.0 (doc 15 Phase C): the cross-service overlay adds 2 spanned phenomena.
	if g.PhenomenaMissingSpan != 0 || g.PhenomenaWithSpan != 40 {
		t.Errorf("merged spans = with %d / missing %d, want 40/0", g.PhenomenaWithSpan, g.PhenomenaMissingSpan)
	}
	// 8 v1 + 2 v2 + 2 v3 + 1 v4 (THR_POD_EVICTED) + 1 v5 (THR_NODE_DISK_PRESSURE) + 1 v6
	// (THR_PVC_PENDING).
	if g.ThresholdRulesStructured != 15 {
		t.Errorf("structured rules = %d, want 15", g.ThresholdRulesStructured)
	}
	if g.hasGaps() {
		t.Error("no gaps should remain with overlays applied (-strict must pass)")
	}
	if res.refWarnTotal != 0 {
		t.Errorf("warnings remain: %d", res.refWarnTotal)
	}
}

// Defective overlays are hard errors: dangling phenomenon, bad vocabulary, missing
// rationale, incoherent rules — a defective authored delta must not merge.
func TestOverlayDefectsAreHardErrors(t *testing.T) {
	sch, _ := compileSchema(schemaPath)
	dir := t.TempDir()
	bad := `
overlay: bad
author: a
spans:
  PHEN_DOES_NOT_EXIST: {span: galaxy-wide, rationale: ""}
  PHEN_OOM_KILL_CGROUP: {span: first-order, rationale: r}
rules:
  - {id: R1, signal: SIG_NOPE, metric: "", kind: config-relative, direction: sideways, entity_scope: Galaxy, window: 5x}
`
	if err := os.WriteFile(filepath.Join(dir, "bad.yaml"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	ovls, err := loadOverlays(dir)
	if err != nil {
		t.Fatalf("loadOverlays: %v", err)
	}
	res, err := lintFile(sch, realKGPath, ovls)
	if err != nil {
		t.Fatalf("lintFile: %v", err)
	}
	if res.ok() {
		t.Error("defective overlay must fail the lint")
	}
	wantFragments := []string{
		"unknown phenomenon", "invalid span", "requires traversal edge types", "rationale",
		"unknown signal", "missing metric", "config_path", "direction", "entity_scope", "bad window",
	}
	joined := strings.Join(res.overlayErrors, "\n")
	for _, w := range wantFragments {
		if !strings.Contains(joined, w) {
			t.Errorf("overlay errors missing %q in:\n%s", w, joined)
		}
	}
	// Gap analysis must NOT consume a defective merge: spans stay un-merged.
	if res.gap.PhenomenaWithSpan != 0 {
		t.Errorf("defective overlay was partially merged: with-span = %d", res.gap.PhenomenaWithSpan)
	}
}

// The overlays directory is excluded from graph-file collection (overlays are
// authored deltas, not graph releases — they must not be schema-checked as graphs).
func TestCollectSkipsOverlaysDir(t *testing.T) {
	files, err := collect([]string{"../../ontology/graph"})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	for _, f := range files {
		if strings.Contains(f, string(filepath.Separator)+"overlays"+string(filepath.Separator)) {
			t.Errorf("collect picked up an overlay file as a graph: %s", f)
		}
	}
	if len(files) != 1 {
		t.Errorf("collected %d graph files under ontology/graph, want 1 (the KG release): %v", len(files), files)
	}
}
