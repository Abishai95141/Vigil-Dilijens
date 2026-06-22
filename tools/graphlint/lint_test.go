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
	// Core structural integrity (schema + referential) is clean on the bare base — the
	// overlays add authored deltas, they never fix a corrupt base.
	if len(res.schemaErrors) != 0 || len(res.refErrors) != 0 {
		t.Errorf("real KG core structure should be clean:\n schema=%v\n ref=%v", res.schemaErrors, res.refErrors)
	}
	if res.refWarnTotal != 0 {
		t.Errorf("owned_by_agent warnings = %d, want 0 after the agent-name curation; e.g. %v", res.refWarnTotal, res.refWarnings)
	}
	// The membership-structuring gap STAYS VISIBLE without overlays: the base KG alone is
	// not detection-complete (7 phenomena author required inline members but have zero
	// structured required members, and the detection-status escape hatch lives in an
	// overlay that is not loaded here). So the bare base legitimately FAILS the gate — the
	// production overlays are what acknowledge/resolve it (see TestRealKGWithOverlaysStrictClean).
	if len(res.structuring.HardFails) != 7 {
		t.Errorf("base structuring hard fails = %d, want 7 (the metric-matcher-dark phenomena, incl. INIT_CONTAINER which the overlay resolves): %v",
			len(res.structuring.HardFails), deficitIDs(res.structuring.HardFails))
	}
	if len(res.structErrors) == 0 || res.ok() {
		t.Error("bare base must FAIL the membership-structuring gate (the gap is reported, not silently passed)")
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
	// threshold-rules-v1..v6, detect-conditions-v1..v7 = 15; + disk-filling-v1 (v0.9.0,
	// the hanging-signal wire) = 16; + phenomenon-severity-v1 (v0.10.0, Phase 5) = 17;
	// + init-container-failure-v1 (v0.11.0, the KSM init-restart member) = 18;
	// + detection-status-v1 (v0.12.0, the membership-structuring escape hatch) = 19;
	// + corrections-v1 (v0.13.0, the governed base-content overrides) = 20;
	// + psi-pressure-v1 (v0.14.0, the PSI saturation lane) = 21;
	// + pvc-filling-v1 (v0.15.0, the PVC-fill lane) = 22;
	// + controlplane-metrics-v1 (v0.16.0, the apiserver + CoreDNS lane) = 23.
	if len(ovls) != 24 {
		t.Fatalf("overlays = %d, want 24", len(ovls))
	}
	res, err := lintFile(sch, realKGPath, ovls)
	if err != nil {
		t.Fatalf("lintFile: %v", err)
	}
	if !res.ok() {
		t.Errorf("merged lint should be clean: schema=%v ref=%v overlay=%v struct=%v", res.schemaErrors, res.refErrors, res.overlayErrors, res.structErrors)
	}
	// The membership-structuring gate is GREEN with the production overlays: the 6
	// metric-matcher-dark phenomena are each acknowledged by detection-status-v1, so zero
	// hard fails remain (PHEN_INIT_CONTAINER_FAILURE is detectable via its overlay members).
	if len(res.structErrors) != 0 {
		t.Errorf("membership-structuring hard fails remain: %v", res.structErrors)
	}
	if len(res.structuring.Acknowledged) != 4 {
		t.Errorf("acknowledged off-matcher phenomena = %d, want 4 (detection-status-v1 set minus PDB_VIOLATION + IMAGE_GC_EVENTS, both now wired)", len(res.structuring.Acknowledged))
	}
	g := res.gap
	// 41 after v0.9.0: + PHEN_DISK_FILLING (entity-local span). (40 after v0.4.0: the
	// cross-service overlay added 2 spanned phenomena.) + 3 PSI saturation phenomena
	// (v0.14.0: CPU/MEMORY/IO) = 44; + PHEN_PVC_FILLING (v0.15.0) = 45.
	if g.PhenomenaMissingSpan != 0 || g.PhenomenaWithSpan != 45 {
		t.Errorf("merged spans = with %d / missing %d, want 45/0", g.PhenomenaWithSpan, g.PhenomenaMissingSpan)
	}
	// 8 v1 + 2 v2 + 2 v3 + 1 v4 (THR_POD_EVICTED) + 1 v5 (THR_NODE_DISK_PRESSURE) + 1 v6
	// (THR_PVC_PENDING) + 1 disk-filling (THR_CONTAINER_FS_USAGE_VS_EPHEMERAL_LIMIT) = 16;
	// + 1 init-container (THR_INIT_CONTAINER_RESTARTS_RATE) = 17; + 3 PSI rate guards
	// (v0.14.0) = 20; + 1 PVC-fill bar (v0.15.0, THR_PVC_USED_VS_REQUESTED_STORAGE) = 21;
	// + 4 control-plane bars (v0.16.0: SERVFAIL/cache-miss ratios + APF/webhook rate guards) = 25.
	if g.ThresholdRulesStructured != 27 {
		t.Errorf("structured rules = %d, want 27", g.ThresholdRulesStructured)
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

// Two overlays declaring a CONFLICTING detection_status for the same phenomenon (a
// different lane) is a HARD error: graphlint's offline lint must match the runtime
// loader, which rejects the same input (graph/overlay.go's applyOverlay errors on a
// detection_status conflict). This guards the offline/runtime validation parity.
func TestOverlayDetectionStatusConflictAcrossOverlays(t *testing.T) {
	sch, _ := compileSchema(schemaPath)
	dir := t.TempDir()
	first := `
overlay: ds-first
author: a
detection_status:
  PHEN_OOM_KILL_CGROUP: {lane: needs-scrape-lane, rationale: no scrape lane exists yet}
`
	second := `
overlay: ds-second
author: a
detection_status:
  PHEN_OOM_KILL_CGROUP: {lane: wireable-backlog, rationale: on the wireable backlog}
`
	if err := os.WriteFile(filepath.Join(dir, "a-first.yaml"), []byte(first), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b-second.yaml"), []byte(second), 0o644); err != nil {
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
	joined := strings.Join(res.overlayErrors, "\n")
	if !strings.Contains(joined, "detection_status conflict across overlays") {
		t.Errorf("conflicting cross-overlay detection_status must be a hard overlay error, got:\n%s", joined)
	}
	if res.ok() {
		t.Error("conflicting detection_status overlays must fail the lint")
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
