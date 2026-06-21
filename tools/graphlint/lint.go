// Command graphlint validates the ontology knowledge graph (doc 02 M3) and reports
// the authoring gap (doc 14 A14). It performs three checks, in order of severity:
//
//  1. Schema validation — the KG envelope/structure against ontology/schema/kg.schema.json.
//  2. Referential integrity — every edge endpoint resolves to a node and its
//     src_type/dst_type matches the referenced node's type.
//  3. Authoring gap — which phenomena lack a declared topological span (doc 02 §3.6:
//     an undeclared span is INVALID, not entity-local-by-default) and the standing
//     threshold-rule gap, plus informational vocabulary stats.
//
// (1) and (2) are ERRORS (a malformed/dangling graph fails). (3) is a GAP: reported
// always, and a hard failure only under -strict (the gate that must pass before
// topological detection ships). Pure Go, no CGO. This is the offline governance lint;
// the runtime loader lives in obsd/internal/graph.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

// kgNode / kgEdge / kgDoc are the lightweight projections graphlint needs for the
// semantic lints (the rich typed loader is obsd/internal/graph).
type kgNode struct {
	ID       string `json:"id" yaml:"id"`
	Type     string `json:"type" yaml:"type"`
	Span     string `json:"span" yaml:"span"`
	Modality string `json:"modality" yaml:"modality"`
	DataType string `json:"data_type" yaml:"data_type"`
	Severity string `json:"severity" yaml:"severity"` // AUTHORED phenomenon prioritisation (doc 21 Phase 5)
	// Signals is a phenomenon's INLINE member tuples [pattern, role, temporal, note]
	// (the documentary membership the matcher never reads) — needed by the
	// membership-structuring lint to count inline-required members.
	Signals [][]string `json:"signals" yaml:"signals"`
	// Patterns is an EquivalenceGroup's dialect-bridge regexes — needed to validate an
	// equivalence-pattern override removes a pattern that is actually present.
	Patterns []string `json:"patterns" yaml:"patterns"`
}

type kgEdge struct {
	Type          string `json:"type" yaml:"type"`
	Src           string `json:"src" yaml:"src"`
	SrcType       string `json:"src_type" yaml:"src_type"`
	Dst           string `json:"dst" yaml:"dst"`
	DstType       string `json:"dst_type" yaml:"dst_type"`
	TemporalOrder string `json:"temporal_order" yaml:"temporal_order"`
	Threshold     string `json:"threshold" yaml:"threshold"`
	// Role distinguishes a required structured member (participates_in) from a
	// corroborating one — needed by the membership-structuring lint.
	Role string `json:"role" yaml:"role"`
}

type kgDoc struct {
	Nodes []kgNode `json:"nodes" yaml:"nodes"`
	Edges []kgEdge `json:"edges" yaml:"edges"`
}

// validSeverity mirrors graph.IsValidSeverity (graphlint cannot import obsd/internal/graph —
// the internal-package rule). The closed AUTHORED severity vocabulary (doc 21 Phase 5).
func validSeverity(s string) bool {
	switch s {
	case "", "critical", "high", "medium", "low":
		return true
	}
	return false
}

// RefError is a referential-integrity violation (a dangling or mistyped endpoint).
type RefError struct {
	EdgeIndex int
	EdgeType  string
	Detail    string
}

func (e RefError) String() string {
	return fmt.Sprintf("edge %d (%s): %s", e.EdgeIndex, e.EdgeType, e.Detail)
}

// fileResult is the lint outcome for one graph file.
type fileResult struct {
	path          string
	schemaErrors  []string
	refErrors     []RefError // dangling refs on detection-critical edge types (capped display list)
	refWarnings   []RefError // dangling refs on owned_by_agent (organizational; capped display list)
	refErrTotal   int        // total core ref errors (uncapped)
	refWarnTotal  int        // total owned_by_agent ref warnings (uncapped)
	overlayErrors []string   // defective authored overlays (hard errors)
	structErrors  []string   // membership-structuring HARD fails (inline-required, zero structured-required, unacknowledged)
	overlays      []overlayDoc
	gap           GapReport
	structuring   StructuringReport
}

// ok reports whether the file passed the HARD checks (schema + core referential
// integrity + overlay validity + membership structuring). owned_by_agent ref warnings
// do not fail the graph: it is an organizational annotation, not detection knowledge,
// so a mistyped agent name is a curation item, not a corruption of what detection reads.
// A membership-structuring hard fail IS a corruption of what detection reads (a
// phenomenon the curator declared metric-detectable that the matcher is structurally
// blind to), so it gates like a dangling participates_in endpoint.
func (r fileResult) ok() bool {
	return len(r.schemaErrors) == 0 && len(r.refErrors) == 0 &&
		len(r.overlayErrors) == 0 && len(r.structErrors) == 0
}

func compileSchema(schemaPath string) (*jsonschema.Schema, error) {
	raw, err := os.ReadFile(schemaPath)
	if err != nil {
		return nil, fmt.Errorf("read schema: %w", err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("parse schema JSON: %w", err)
	}
	c := jsonschema.NewCompiler()
	const id = "kg.schema.json"
	if err := c.AddResource(id, doc); err != nil {
		return nil, fmt.Errorf("add schema resource: %w", err)
	}
	return c.Compile(id)
}

// decode reads a graph file (JSON or YAML) into both a schema-validation value and
// the typed kgDoc.
func decode(path string) (any, kgDoc, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, kgDoc{}, fmt.Errorf("read: %w", err)
	}
	var doc kgDoc
	var inst any
	if strings.EqualFold(filepath.Ext(path), ".json") {
		if err := json.Unmarshal(raw, &inst); err != nil {
			return nil, kgDoc{}, fmt.Errorf("parse json: %w", err)
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			return nil, kgDoc{}, fmt.Errorf("parse json doc: %w", err)
		}
	} else {
		if err := yaml.Unmarshal(raw, &inst); err != nil {
			return nil, kgDoc{}, fmt.Errorf("parse yaml: %w", err)
		}
		inst = normalize(inst)
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return nil, kgDoc{}, fmt.Errorf("parse yaml doc: %w", err)
		}
	}
	return inst, doc, nil
}

// normalize converts map[any]any (defensive) to map[string]any for the validator.
func normalize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			t[k] = normalize(val)
		}
		return t
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[fmt.Sprintf("%v", k)] = normalize(val)
		}
		return out
	case []any:
		for i, val := range t {
			t[i] = normalize(val)
		}
		return t
	default:
		return v
	}
}

const maxErrors = 50

// referentialIntegrity checks every edge endpoint resolves to a node with a
// matching declared type (doc 02 §3.6 authoring invariant). Violations on
// detection-critical edge types are errors; on owned_by_agent (organizational
// ownership annotation) they are warnings (a curation item, not a corruption).
func referentialIntegrity(doc kgDoc) (errs, warns []RefError, errTotal, warnTotal int) {
	nodeType := make(map[string]string, len(doc.Nodes))
	for _, n := range doc.Nodes {
		nodeType[n.ID] = n.Type
	}
	add := func(e kgEdge, i int, detail string) {
		re := RefError{EdgeIndex: i, EdgeType: e.Type, Detail: detail}
		if e.Type == "owned_by_agent" {
			warnTotal++ // count ALL; cap only the displayed slice
			if len(warns) < maxErrors {
				warns = append(warns, re)
			}
			return
		}
		errTotal++
		if len(errs) < maxErrors {
			errs = append(errs, re)
		}
	}
	for i, e := range doc.Edges {
		st, srcOK := nodeType[e.Src]
		dt, dstOK := nodeType[e.Dst]
		switch {
		case !srcOK:
			add(e, i, fmt.Sprintf("src %q does not resolve to a node", e.Src))
		case e.SrcType != "" && st != e.SrcType:
			add(e, i, fmt.Sprintf("src_type %q != node %q type %q", e.SrcType, e.Src, st))
		}
		switch {
		case !dstOK:
			add(e, i, fmt.Sprintf("dst %q does not resolve to a node", e.Dst))
		case e.DstType != "" && dt != e.DstType:
			add(e, i, fmt.Sprintf("dst_type %q != node %q type %q", e.DstType, e.Dst, dt))
		}
	}
	return errs, warns, errTotal, warnTotal
}

// lintFile runs all checks on one graph file. Overlays (authored spans + threshold
// rules) are validated against the document and merged before the gap analysis, so
// the report states what remains owed AFTER the authored deltas apply; overlay
// defects are hard errors (a defective authored delta must not merge).
func lintFile(sch *jsonschema.Schema, path string, ovls []overlayDoc) (fileResult, error) {
	inst, doc, err := decode(path)
	if err != nil {
		return fileResult{}, err
	}
	res := fileResult{path: path}
	if err := sch.Validate(inst); err != nil {
		res.schemaErrors = append(res.schemaErrors, err.Error())
	}
	// doc 21 Phase 5: a phenomenon's AUTHORED severity must be a declared level or empty. An
	// unknown value is a curator typo — a hard error (a defective authored prioritisation).
	for _, n := range doc.Nodes {
		if n.Type == "CorrelationGroup" && !validSeverity(n.Severity) {
			res.schemaErrors = append(res.schemaErrors,
				fmt.Sprintf("phenomenon %q: severity %q is not critical|high|medium|low (or empty) (doc 21 Phase 5)", n.ID, n.Severity))
		}
	}
	res.refErrors, res.refWarnings, res.refErrTotal, res.refWarnTotal = referentialIntegrity(doc)
	res.overlayErrors = validateOverlays(doc, ovls)
	rules := 0
	if len(res.overlayErrors) == 0 {
		rules = mergeOverlays(&doc, ovls)
	}
	res.gap = analyzeGap(doc)
	res.gap.ThresholdRulesStructured = rules
	// Membership-structuring analysis (the inline-vs-structured member gate). Needs BOTH
	// the merged doc (base nodes + edges) AND ovls (overlay `members:`/`detection_status:`
	// blocks that mergeOverlays never projects into doc.Edges), so it runs here where both
	// are in scope. Skipped if overlays are defective (they were not merged).
	if len(res.overlayErrors) == 0 {
		res.structuring = analyzeStructuringGap(doc, ovls)
		res.structErrors = res.structuring.hardErrors()
	}
	res.overlays = ovls
	return res, nil
}

// collect gathers *.json / *.yaml / *.yml graph files under the given roots.
func collect(roots []string) ([]string, error) {
	var files []string
	for _, root := range roots {
		info, err := os.Stat(root)
		if err != nil {
			return nil, fmt.Errorf("stat %q: %w", root, err)
		}
		if !info.IsDir() {
			files = append(files, root)
			continue
		}
		err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				// Overlays are authored deltas, not graph releases: they are
				// validated and merged via -overlays, never schema-checked as graphs.
				if d.Name() == "overlays" {
					return fs.SkipDir
				}
				return nil
			}
			switch strings.ToLower(filepath.Ext(p)) {
			case ".json", ".yaml", ".yml":
				files = append(files, p)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(files)
	return files, nil
}
