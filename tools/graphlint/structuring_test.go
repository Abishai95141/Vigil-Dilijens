package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// phen builds a phenomenon node with the given inline member roles.
func phenNode(id string, roles ...string) kgNode {
	n := kgNode{ID: id, Type: "CorrelationGroup"}
	for i, r := range roles {
		n.Signals = append(n.Signals, []string{"pattern" + string(rune('A'+i)), r, "T0", "note"})
	}
	return n
}

func reqEdge(sig, phen, role string) kgEdge {
	return kgEdge{Type: "participates_in", Src: sig, Dst: phen, Role: role}
}

func hasID(ds []StructuringDeficit, id string) bool {
	for _, d := range ds {
		if d.ID == id {
			return true
		}
	}
	return false
}

// TestOverrideValidation checks graphlint validates governed corrections: a valid override
// passes, and one targeting non-existent content or omitting a rationale is a hard error
// (mirroring the runtime applyOverlay so the offline lint matches ingestion).
func TestOverrideValidation(t *testing.T) {
	sch, err := compileSchema(schemaPath)
	if err != nil {
		t.Fatalf("compileSchema: %v", err)
	}
	cases := []struct {
		name, body string
		wantErr    string // "" => must pass
	}{
		{"valid", `
overlay: t
author: a
overrides:
  phenomenon_relations:
    - {src: PHEN_PROBE_CASCADE_META, dst: PHEN_KUBE_PROXY_SYNC_SLOW, set_role: corroborating, rationale: probes bypass kube-proxy}
  equivalence_patterns:
    - {id: EQG_OOM_EVENTS, remove: [oom_kill], rationale: scope mis-join}
`, ""},
		{"relation absent", `
overlay: t
author: a
overrides:
  phenomenon_relations:
    - {src: PHEN_MEMORY_LEAK, dst: PHEN_DNS_FAILURE, set_role: corroborating, rationale: x}
`, "no such relation exists"},
		{"missing rationale", `
overlay: t
author: a
overrides:
  phenomenon_relations:
    - {src: PHEN_PROBE_CASCADE_META, dst: PHEN_KUBE_PROXY_SYNC_SLOW, set_role: corroborating, rationale: ""}
`, "needs a rationale"},
		{"pattern not present", `
overlay: t
author: a
overrides:
  equivalence_patterns:
    - {id: EQG_OOM_EVENTS, remove: [bogus], rationale: x}
`, "is not present to remove"},
		{"empties the group", `
overlay: t
author: a
overrides:
  equivalence_patterns:
    - {id: EQG_OOM_EVENTS, remove: ['^container_oom_events_total$', oom_kill], rationale: x}
`, "would empty the group"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "ov.yaml"), []byte(c.body), 0o644); err != nil {
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
			if c.wantErr == "" {
				if len(res.overlayErrors) != 0 {
					t.Errorf("valid override should pass, got: %s", joined)
				}
			} else if !strings.Contains(joined, c.wantErr) {
				t.Errorf("want error %q, got: %s", c.wantErr, joined)
			}
		})
	}
}

// TestDetectionLaneVocabParity locks graphlint's detection-lane vocabulary to the canonical
// set it MUST mirror from graph.knownDetectionLanes (graphlint cannot import the internal
// package, so the set is duplicated). Adding/removing a lane requires updating BOTH
// graphlint's ovDetectionLaneVocab AND graph.knownDetectionLanes AND this list — the
// failure here is the reminder to keep all three in lockstep.
func TestDetectionLaneVocabParity(t *testing.T) {
	canonical := []string{"events-only", "log-only", "needs-scrape-lane", "needs-entity-binding", "wireable-backlog"}
	if len(ovDetectionLaneVocab) != len(canonical) {
		t.Fatalf("graphlint lane vocab size = %d, want %d (mirror graph.knownDetectionLanes)", len(ovDetectionLaneVocab), len(canonical))
	}
	for _, l := range canonical {
		if !ovDetectionLaneVocab[l] {
			t.Errorf("graphlint lane vocab missing %q (must mirror graph.knownDetectionLanes)", l)
		}
	}
	for l := range ovDetectionLaneVocab {
		found := false
		for _, c := range canonical {
			if c == l {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("graphlint lane vocab has unexpected %q (update graph.knownDetectionLanes + the canonical list)", l)
		}
	}
}

// (a) A phenomenon with required inline members and ZERO structured required members,
// not acknowledged, HARD-fails — it is undetectable by the metric matcher.
func TestStructuringHardFailOnZeroStructured(t *testing.T) {
	doc := kgDoc{Nodes: []kgNode{phenNode("PHEN_A", "required", "required")}}
	rep := analyzeStructuringGap(doc, nil)
	if len(rep.HardFails) != 1 || rep.HardFails[0].ID != "PHEN_A" {
		t.Fatalf("want 1 hard fail PHEN_A, got %v", deficitIDs(rep.HardFails))
	}
	if len(rep.hardErrors()) != 1 {
		t.Fatalf("want 1 hard error, got %d", len(rep.hardErrors()))
	}
}

// (b) The same phenomenon with a detection_status escape hatch PASSES (no hard fail) and
// is listed as acknowledged off the metric matcher.
func TestStructuringEscapeHatchAcknowledges(t *testing.T) {
	doc := kgDoc{Nodes: []kgNode{phenNode("PHEN_A", "required", "required")}}
	ovls := []overlayDoc{{DetectionStatuses: map[string]ovDetectionStatus{
		"PHEN_A": {Lane: "needs-scrape-lane", Rationale: "no scrape lane exists"},
	}}}
	rep := analyzeStructuringGap(doc, ovls)
	if len(rep.HardFails) != 0 {
		t.Fatalf("escape hatch must clear the hard fail, got %v", deficitIDs(rep.HardFails))
	}
	if len(rep.Acknowledged) != 1 || rep.Acknowledged[0].ID != "PHEN_A" || rep.Acknowledged[0].Lane != "needs-scrape-lane" {
		t.Fatalf("want PHEN_A acknowledged on needs-scrape-lane, got %+v", rep.Acknowledged)
	}
}

// (c) The init-container positive control + merge-union lock: a phenomenon whose required
// members are supplied ENTIRELY by an overlay `members:` block (never projected into
// doc.Edges) HARD-fails WITHOUT overlays but PASSES WITH them. This guards the single
// biggest correctness risk — reading only doc.Edges would spuriously fail it.
func TestStructuringMergeUnionFromOverlayMembers(t *testing.T) {
	doc := kgDoc{Nodes: []kgNode{phenNode("PHEN_INIT", "required")}}

	repNoOvl := analyzeStructuringGap(doc, nil)
	if !hasID(repNoOvl.HardFails, "PHEN_INIT") {
		t.Fatalf("PHEN_INIT must hard-fail without its overlay members")
	}

	// An overlay supplies the required member (empty role defaults to "required", matching
	// the runtime applyOverlay default).
	ovls := []overlayDoc{{Members: map[string][]ovMember{
		"PHEN_INIT": {{Signal: "SIG_X", Role: ""}},
	}}}
	repOvl := analyzeStructuringGap(doc, ovls)
	if len(repOvl.HardFails) != 0 {
		t.Fatalf("overlay-supplied required member must clear the hard fail, got %v", deficitIDs(repOvl.HardFails))
	}
}

// (d) Under-structured (inline 3, structured-required 1) is a SOFT deficit, never a hard
// fail — detection is partial but present.
func TestStructuringUnderStructuredIsSoftNotHard(t *testing.T) {
	doc := kgDoc{
		Nodes: []kgNode{phenNode("PHEN_U", "required", "required", "required")},
		Edges: []kgEdge{reqEdge("SIG_Y", "PHEN_U", "required")},
	}
	rep := analyzeStructuringGap(doc, nil)
	if len(rep.HardFails) != 0 {
		t.Fatalf("under-structured must NOT hard-fail, got %v", deficitIDs(rep.HardFails))
	}
	if !hasID(rep.Deficits, "PHEN_U") {
		t.Fatalf("under-structured must be a soft deficit, got %v", deficitIDs(rep.Deficits))
	}
	for _, d := range rep.Deficits {
		if d.ID == "PHEN_U" && (d.InlineRequired != 3 || d.StructuredRequired != 1) {
			t.Fatalf("PHEN_U deficit = inline %d / struct %d, want 3/1", d.InlineRequired, d.StructuredRequired)
		}
	}
}

// (e) Over-structured (inline 1, structured-required 3) is NOT a deficit and never fails —
// a richer structured set than the inline headline is a benign authoring choice.
func TestStructuringOverStructuredIsClean(t *testing.T) {
	doc := kgDoc{
		Nodes: []kgNode{phenNode("PHEN_O", "required")},
		Edges: []kgEdge{
			reqEdge("SIG_1", "PHEN_O", "required"),
			reqEdge("SIG_2", "PHEN_O", "required"),
			reqEdge("SIG_3", "PHEN_O", "required"),
		},
	}
	rep := analyzeStructuringGap(doc, nil)
	if len(rep.HardFails) != 0 || hasID(rep.Deficits, "PHEN_O") {
		t.Fatalf("over-structured must be clean, got hardfails=%v deficits=%v", deficitIDs(rep.HardFails), deficitIDs(rep.Deficits))
	}
}

// Risk #3 lock: a phenomenon with structured members that are all CORROBORATING (zero
// required) still HARD-fails — the rule is "zero structured REQUIRED", never "zero
// structured total". Relaxing it would let a corroborating-only phenomenon through
// undetectably (the matcher needs a required member to accrue RequiredTotal>0).
func TestStructuringCorroboratingOnlyStillHardFails(t *testing.T) {
	doc := kgDoc{
		Nodes: []kgNode{phenNode("PHEN_C", "required")},
		Edges: []kgEdge{reqEdge("SIG_Z", "PHEN_C", "corroborating")},
	}
	rep := analyzeStructuringGap(doc, nil)
	if !hasID(rep.HardFails, "PHEN_C") {
		t.Fatalf("corroborating-only structured members must NOT satisfy the gate, got %v", deficitIDs(rep.HardFails))
	}
}
