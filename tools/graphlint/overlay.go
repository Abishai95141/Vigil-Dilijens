package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Authored-overlay validation (doc 02 §3.6, doc 12). Overlays carry the authored
// deltas — phenomenon span declarations and structured threshold rules — that the
// runtime loader (obsd/internal/graph) merges into the base KG release. graphlint
// validates them with its own lightweight decode (deliberately independent of the
// runtime loader: offline governance lint vs runtime ingestion) and runs the gap
// analysis on the MERGED view, so the report states what a curator still owes.

type ovSpan struct {
	Span               string   `yaml:"span"`
	TraversalEdgeTypes []string `yaml:"traversal_edge_types"`
	Rationale          string   `yaml:"rationale"`
}

type ovRule struct {
	ID          string   `yaml:"id"`
	Signal      string   `yaml:"signal"`
	Metric      string   `yaml:"metric"`
	Kind        string   `yaml:"kind"`
	ConfigPath  string   `yaml:"config_path"`
	Eligibility string   `yaml:"eligibility_config_path"`
	Factor      float64  `yaml:"factor"`
	Default     *float64 `yaml:"default"`
	Direction   string   `yaml:"direction"`
	EntityScope string   `yaml:"entity_scope"`
	Window      string   `yaml:"window"`
	Rationale   string   `yaml:"rationale"`
}

// ovCheck mirrors the runtime loader's MemberCheck (doc 07 §3.1): the authored
// bridge from a phenomenon member to a fingerprint facet, with the 07 M2
// fields (on: anchor|neighbour).
type ovCheck struct {
	Signal   string `yaml:"signal"`
	Metric   string `yaml:"metric"`
	Facet    string `yaml:"facet"`
	Expect   string `yaml:"expect"`
	MinState string `yaml:"min_state"`
	On       string `yaml:"on"`
	Note     string `yaml:"note"`
}

// ovPhenomenon / ovRelation mirror the runtime loader's overlay-added CorrelationGroup
// nodes + phenomenon_relation edges (doc 15 Phase C): authored deltas may add phenomena
// + relations via overlay, so the base KG stays an immutable vendored mirror.
type ovPhenomenon struct {
	ID      string     `yaml:"id"`
	Label   string     `yaml:"label"`
	Signals [][]string `yaml:"signals"`
	Notes   string     `yaml:"notes"`
}

type ovRelation struct {
	Src           string `yaml:"src"`
	Dst           string `yaml:"dst"`
	Role          string `yaml:"role"`
	TemporalOrder string `yaml:"temporal_order"`
	Why           string `yaml:"why"`
}

// ovMember mirrors the runtime loader's overlayMember (doc 15 Phase C): the SIG_-bearing
// participates_in membership a check binds against. The loader appends the edge; graphlint
// reads the block so an overlay phenomenon's own member resolves for check validation.
type ovMember struct {
	Signal   string `yaml:"signal"`
	Role     string `yaml:"role"`
	Temporal string `yaml:"temporal"`
	Why      string `yaml:"why"`
}

// ovSignal mirrors the runtime loader's overlaySignal (graph/overlay.go): a new Signal
// node declared by an overlay — the base KG stays an immutable vendored mirror, so a new
// observable (an app /metrics gauge, or an infra series the base catalogue did not
// enumerate, e.g. a KSM init-container counter) lives in a versioned overlay. graphlint
// registers it so this overlay's own members/rules/checks resolve it, exactly as the
// runtime loader does — offline validation must match runtime ingestion.
type ovSignal struct {
	ID       string `yaml:"id"`
	Name     string `yaml:"name"`
	DataType string `yaml:"data_type"`
	Entity   string `yaml:"entity"`
	Modality string `yaml:"modality"`
}

// ovEquivGroup mirrors the runtime loader's overlayEquivGroup (doc 21 §5.3): the deterministic
// absorb path for a promoted stray→group mapping. graphlint validates it offline so a
// defective equivalence-group delta (non-compiling pattern, redefinition, incomplete new
// group) is a HARD error before it merges.
type ovEquivGroup struct {
	ID            string   `yaml:"id"`
	Label         string   `yaml:"label"`
	CanonicalOTel string   `yaml:"canonical_otel"`
	Patterns      []string `yaml:"patterns"`
	AddPatterns   []string `yaml:"add_patterns"`
	Rationale     string   `yaml:"rationale"`
	Notes         string   `yaml:"notes"`
}

type overlayDoc struct {
	path              string
	Overlay           string                       `yaml:"overlay"`
	Version           int                          `yaml:"version"`
	Author            string                       `yaml:"author"`
	Status            string                       `yaml:"status"`
	Signals           []ovSignal                   `yaml:"signals"`
	Phenomena         []ovPhenomenon               `yaml:"phenomena"`
	EquivGroups       []ovEquivGroup               `yaml:"equivalence_groups"`
	Members           map[string][]ovMember        `yaml:"members"`
	Relations         []ovRelation                 `yaml:"relations"`
	Spans             map[string]ovSpan            `yaml:"spans"`
	DetectionStatuses map[string]ovDetectionStatus `yaml:"detection_status"`
	Overrides         ovOverrides                  `yaml:"overrides"`
	Rules             []ovRule                     `yaml:"rules"`
	Checks            map[string][]ovCheck         `yaml:"checks"`
	Anchors           map[string]string            `yaml:"anchors"`
}

// ovOverrides mirrors the runtime loader's overlayOverrides: governed corrections of base
// content (a non-causal relation downgraded, a false-equivalence pattern removed). graphlint
// validates them offline so a defective correction (unknown target, missing rationale,
// removing a non-present pattern) is a HARD error before it merges.
type ovOverrides struct {
	PhenomenonRelations []ovRelationOverride     `yaml:"phenomenon_relations"`
	EquivalencePatterns []ovEquivPatternOverride `yaml:"equivalence_patterns"`
}

type ovRelationOverride struct {
	Src       string `yaml:"src"`
	Dst       string `yaml:"dst"`
	SetRole   string `yaml:"set_role"`
	Rationale string `yaml:"rationale"`
}

type ovEquivPatternOverride struct {
	ID        string   `yaml:"id"`
	Remove    []string `yaml:"remove"`
	Rationale string   `yaml:"rationale"`
}

var ovRelationRoleVocab = map[string]bool{"trigger": true, "downstream": true, "corroborating": true}

// ovDetectionStatus mirrors the runtime loader's overlayDetectionStatus: the authored
// membership-structuring escape hatch — the lane a phenomenon's detection lives on (or
// why it is off the metric matcher) plus a falsifiable rationale.
type ovDetectionStatus struct {
	Lane      string `yaml:"lane"`
	Rationale string `yaml:"rationale"`
}

// ovDetectionLaneVocab is the closed detection-lane vocabulary. KEEP IN SYNC with
// graph.knownDetectionLanes (obsd/internal/graph/graph.go) — graphlint cannot import the
// internal package, so the set is duplicated; TestDetectionLaneVocabParity locks this copy.
// graphlint validates it offline so a defective escape-hatch declaration is a HARD error
// before it merges.
var ovDetectionLaneVocab = map[string]bool{
	"events-only": true, "log-only": true, "needs-scrape-lane": true,
	"needs-entity-binding": true, "wireable-backlog": true,
}

var (
	ovSpanVocab = map[string]bool{"entity-local": true, "first-order": true, "second-order": true}
	// "flow" is the v2 observed-flow (conntrack) traversal edge type (doc 15 Phase C).
	ovEdgeVocab      = map[string]bool{"runs-on": true, "mounts": true, "selects": true, "node-lease": true, "flow": true}
	ovKindVocab      = map[string]bool{"config-relative": true, "absolute": true, "rate-of-change": true, "co-occurrence": true}
	ovDirectionVocab = map[string]bool{"above": true, "below": true}
	ovScopeVocab     = map[string]bool{"Container": true, "Pod": true, "Node": true, "PVC": true, "PDB": true}
	ovFacetVocab     = map[string]bool{"level": true, "slope": true, "ratio": true, "rate-guard": true}
	ovExpectVocab    = map[string]bool{"rising": true, "falling": true, "crossed": true, "at-or-above": true, "breached": true}
	ovMinStateVocab  = map[string]bool{"": true, "at-threshold": true, "above": true, "well-above": true}
	ovOnVocab        = map[string]bool{"": true, "anchor": true, "neighbour": true, "two-hop": true}
	ovPathVocab      = map[string]bool{
		"container.resources.limits.memory":            true,
		"container.resources.limits.cpu":               true,
		"container.resources.limits.ephemeral-storage": true,
		"pvc.spec.resources.requests.storage":          true,
		"node.status.allocatable.memory":               true,
	}
)

// loadOverlays reads every *.yaml/*.yml under dir (non-recursive, sorted). A missing
// dir is not an error: the base graph is then linted alone and its gaps stay visible.
func loadOverlays(dir string) ([]overlayDoc, error) {
	if dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read overlay dir %q: %w", dir, err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if ext := strings.ToLower(filepath.Ext(e.Name())); ext == ".yaml" || ext == ".yml" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	out := make([]overlayDoc, 0, len(names))
	for _, n := range names {
		p := filepath.Join(dir, n)
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read overlay %q: %w", p, err)
		}
		var d overlayDoc
		if err := yaml.Unmarshal(raw, &d); err != nil {
			return nil, fmt.Errorf("parse overlay %q: %w", p, err)
		}
		d.path = p
		out = append(out, d)
	}
	return out, nil
}

// validateOverlays checks every overlay against the graph document: provenance
// presence, vocabulary membership, referential integrity (phenomenon and signal
// ids), span/edge coherence, and rule-kind coherence. Violations are HARD errors —
// a defective authored delta must not merge.
func validateOverlays(doc kgDoc, ovls []overlayDoc) []string {
	nodeType := make(map[string]string, len(doc.Nodes))
	for _, n := range doc.Nodes {
		nodeType[n.ID] = n.Type
	}
	// participates_in membership (signal -> phenomenon), for check referential
	// integrity: a check may only bind an ALREADY-AUTHORED member (doc 07 §3.1).
	memberOf := map[string]map[string]bool{} // phen -> signal -> member
	for _, e := range doc.Edges {
		if e.Type == "participates_in" {
			if memberOf[e.Dst] == nil {
				memberOf[e.Dst] = map[string]bool{}
			}
			memberOf[e.Dst][e.Src] = true
		}
	}
	// phenomenon_relation edges (src->dst), for validating a relation override targets an
	// EXISTING relation.
	relationEdge := map[string]bool{} // "src\x00dst" -> exists
	for _, e := range doc.Edges {
		if e.Type == "phenomenon_relation" {
			relationEdge[e.Src+"\x00"+e.Dst] = true
		}
	}
	// EquivalenceGroup patterns, for validating an equivalence-pattern override removes a
	// pattern that is actually present. Built from the base AND from overlay-added groups/
	// patterns (a pre-pass), so an override may target overlay-authored content exactly as the
	// runtime allows (apply-then-override) — without this, graphlint would falsely reject a
	// correction the runtime accepts.
	groupPatterns := map[string]map[string]bool{} // group id -> pattern -> present
	for _, n := range doc.Nodes {
		if n.Type == "EquivalenceGroup" {
			set := map[string]bool{}
			for _, p := range n.Patterns {
				set[p] = true
			}
			groupPatterns[n.ID] = set
		}
	}
	for _, o := range ovls {
		for _, eg := range o.EquivGroups {
			if groupPatterns[eg.ID] == nil {
				groupPatterns[eg.ID] = map[string]bool{}
			}
			for _, p := range append(append([]string{}, eg.Patterns...), eg.AddPatterns...) {
				groupPatterns[eg.ID][p] = true
			}
		}
		for _, r := range o.Relations {
			relationEdge[r.Src+"\x00"+r.Dst] = true
		}
	}

	var errs []string
	seenRule := map[string]string{}
	// Cross-overlay state for the finalize checks (span/anchor/neighbour
	// coherence — spans, checks, and anchors may arrive from different files).
	spanOf := map[string]string{}             // phen -> declared span (across all overlays)
	anchorOf := map[string]string{}           // phen -> declared anchor
	dsOf := map[string]ovDetectionStatus{}    // phen -> declared detection_status (across all overlays)
	hasChecks := map[string]bool{}            // phen -> any check authored
	hasNeighbourCheck := map[string]bool{}    // phen -> any neighbour-scoped check
	hasTwoHopCheck := map[string]bool{}       // phen -> any two-hop-scoped check
	seenCheck := map[string]map[string]bool{} // phen -> signal -> dup guard
	for _, o := range ovls {
		at := filepath.Base(o.path)
		if strings.TrimSpace(o.Overlay) == "" {
			errs = append(errs, fmt.Sprintf("%s: missing 'overlay' name", at))
		}
		if strings.TrimSpace(o.Author) == "" {
			errs = append(errs, fmt.Sprintf("%s: missing author provenance (doc 02 §3.6)", at))
		}
		// doc 15 cap. A: register overlay-added Signal nodes FIRST (the runtime loader
		// applies signals before everything else) so this overlay's own members, rules,
		// and checks resolve them. id + name + data_type are required (data_type drives
		// the gauge/counter shape derivation); overlays may add, never redefine.
		for _, os := range o.Signals {
			if strings.TrimSpace(os.ID) == "" || strings.TrimSpace(os.Name) == "" {
				errs = append(errs, fmt.Sprintf("%s: overlay signal missing id/name", at))
				continue
			}
			if strings.TrimSpace(os.DataType) == "" {
				errs = append(errs, fmt.Sprintf("%s: overlay signal %q: data_type is required (drives the gauge/counter shape)", at, os.ID))
			}
			if t := nodeType[os.ID]; t != "" {
				errs = append(errs, fmt.Sprintf("%s: overlay signal %q already defined as %s (overlays may add, never redefine)", at, os.ID, t))
				continue
			}
			nodeType[os.ID] = "Signal"
		}
		// doc 21 §5.3: equivalence-group deltas — extend an existing group's dialect
		// patterns or define a new group. Mirrors the runtime loader's overlayEquivGroup:
		// id + rationale required; existing groups use add_patterns (never redefine
		// canonical); a new group needs label + canonical_otel + ≥1 pattern; every pattern
		// must compile (a bad regex would silently shrink the dialect bridge).
		for _, eg := range o.EquivGroups {
			if strings.TrimSpace(eg.ID) == "" {
				errs = append(errs, fmt.Sprintf("%s: equivalence_group with missing id", at))
				continue
			}
			if strings.TrimSpace(eg.Rationale) == "" {
				errs = append(errs, fmt.Sprintf("%s: equivalence_group %q needs a rationale (falsifiable claim, doc 02 §3.6)", at, eg.ID))
			}
			switch typ := nodeType[eg.ID]; typ {
			case "EquivalenceGroup":
				if len(eg.Patterns) > 0 {
					errs = append(errs, fmt.Sprintf("%s: equivalence_group %q already exists: use add_patterns to extend it (patterns: defines a NEW group)", at, eg.ID))
				}
				if len(eg.AddPatterns) == 0 {
					errs = append(errs, fmt.Sprintf("%s: equivalence_group %q: add_patterns is required to extend an existing group", at, eg.ID))
				}
			case "":
				if len(eg.AddPatterns) > 0 {
					errs = append(errs, fmt.Sprintf("%s: equivalence_group %q does not exist: use patterns to define it (add_patterns extends an EXISTING group)", at, eg.ID))
				}
				if strings.TrimSpace(eg.Label) == "" || strings.TrimSpace(eg.CanonicalOTel) == "" {
					errs = append(errs, fmt.Sprintf("%s: equivalence_group %q: a new group needs label + canonical_otel", at, eg.ID))
				}
				if len(eg.Patterns) == 0 {
					errs = append(errs, fmt.Sprintf("%s: equivalence_group %q: a new group needs at least one pattern", at, eg.ID))
				}
				nodeType[eg.ID] = "EquivalenceGroup" // register so a later overlay sees it
			default:
				errs = append(errs, fmt.Sprintf("%s: equivalence_group %q is a %s, not an EquivalenceGroup", at, eg.ID, typ))
			}
			for _, p := range append(append([]string{}, eg.Patterns...), eg.AddPatterns...) {
				if strings.TrimSpace(p) == "" {
					errs = append(errs, fmt.Sprintf("%s: equivalence_group %q: empty pattern", at, eg.ID))
					continue
				}
				if _, err := regexp.Compile(p); err != nil {
					errs = append(errs, fmt.Sprintf("%s: equivalence_group %q: pattern %q does not compile: %v", at, eg.ID, p, err))
				}
			}
		}
		// doc 15 Phase C: register overlay-added phenomena (so this overlay's own
		// spans/relations + later overlays resolve them) and validate added relations.
		for _, op := range o.Phenomena {
			if op.ID == "" || op.Label == "" {
				errs = append(errs, fmt.Sprintf("%s: overlay phenomenon missing id/label", at))
				continue
			}
			if t := nodeType[op.ID]; t != "" {
				errs = append(errs, fmt.Sprintf("%s: overlay phenomenon %q already defined as %s (overlays may add, never redefine)", at, op.ID, t))
				continue
			}
			if len(op.Signals) == 0 {
				errs = append(errs, fmt.Sprintf("%s: overlay phenomenon %q: at least one member signal is required", at, op.ID))
			}
			nodeType[op.ID] = "CorrelationGroup"
		}
		// doc 15 Phase C: an overlay phenomenon's structured members (the loader appends
		// these as participates_in edges). Register them so this overlay's own checks can
		// bind them — mirrors the runtime overlay loader's members block.
		for phen, ms := range o.Members {
			for _, m := range ms {
				if nodeType[m.Signal] != "Signal" {
					errs = append(errs, fmt.Sprintf("%s: %s member %q is not a known Signal", at, phen, m.Signal))
					continue
				}
				if memberOf[phen] == nil {
					memberOf[phen] = map[string]bool{}
				}
				memberOf[phen][m.Signal] = true
			}
		}
		// detection_status escape hatch (the membership-structuring acknowledgment): a
		// known phenomenon, a lane from the closed vocabulary, and a REQUIRED rationale
		// (a falsifiable claim, never a rubber stamp). All hard errors otherwise.
		dsIDs := make([]string, 0, len(o.DetectionStatuses))
		for id := range o.DetectionStatuses {
			dsIDs = append(dsIDs, id)
		}
		sort.Strings(dsIDs)
		for _, id := range dsIDs {
			ds := o.DetectionStatuses[id]
			if nodeType[id] != "CorrelationGroup" {
				errs = append(errs, fmt.Sprintf("%s: detection_status for unknown phenomenon %q", at, id))
			}
			if !ovDetectionLaneVocab[ds.Lane] {
				errs = append(errs, fmt.Sprintf("%s: %s: detection_status lane %q is not events-only|log-only|needs-scrape-lane|needs-entity-binding|wireable-backlog", at, id, ds.Lane))
			}
			if strings.TrimSpace(ds.Rationale) == "" {
				errs = append(errs, fmt.Sprintf("%s: %s: detection_status needs a rationale (a falsifiable authored claim, never a rubber stamp)", at, id))
			}
			// Cross-overlay conflict: the same phenomenon re-declared with a different lane
			// or rationale (mirrors the runtime loader's applyOverlay reject at
			// graph/overlay.go — offline lint must match runtime ingestion).
			if prev, dup := dsOf[id]; dup && (prev.Lane != ds.Lane || prev.Rationale != ds.Rationale) {
				errs = append(errs, fmt.Sprintf("%s: %s: detection_status conflict across overlays (%q vs %q)", at, id, prev.Lane, ds.Lane))
			}
			dsOf[id] = ds
		}
		// Governed corrections (doc 12): validate overrides target EXISTING base content and
		// carry a rationale — mirrors the runtime applyOverlay so the offline lint matches.
		for _, ro := range o.Overrides.PhenomenonRelations {
			if strings.TrimSpace(ro.Rationale) == "" {
				errs = append(errs, fmt.Sprintf("%s: override phenomenon_relation %s->%s needs a rationale (a governed correction, never silent)", at, ro.Src, ro.Dst))
			}
			if !ovRelationRoleVocab[ro.SetRole] {
				errs = append(errs, fmt.Sprintf("%s: override phenomenon_relation %s->%s: set_role %q is not trigger|downstream|corroborating", at, ro.Src, ro.Dst, ro.SetRole))
			}
			if nodeType[ro.Src] != "CorrelationGroup" || nodeType[ro.Dst] != "CorrelationGroup" {
				errs = append(errs, fmt.Sprintf("%s: override phenomenon_relation %s->%s: src/dst must be known phenomena", at, ro.Src, ro.Dst))
			} else if !relationEdge[ro.Src+"\x00"+ro.Dst] {
				errs = append(errs, fmt.Sprintf("%s: override phenomenon_relation %s->%s: no such relation exists to override (overrides correct EXISTING content)", at, ro.Src, ro.Dst))
			}
		}
		for _, eo := range o.Overrides.EquivalencePatterns {
			if strings.TrimSpace(eo.Rationale) == "" {
				errs = append(errs, fmt.Sprintf("%s: override equivalence_patterns %s needs a rationale", at, eo.ID))
			}
			pats, ok := groupPatterns[eo.ID]
			if !ok {
				errs = append(errs, fmt.Sprintf("%s: override equivalence_patterns: unknown equivalence group %q", at, eo.ID))
				continue
			}
			remaining := len(pats)
			for _, pat := range eo.Remove {
				if !pats[pat] {
					errs = append(errs, fmt.Sprintf("%s: override equivalence_patterns %s: pattern %q is not present to remove", at, eo.ID, pat))
				} else {
					remaining--
				}
			}
			if remaining <= 0 {
				errs = append(errs, fmt.Sprintf("%s: override equivalence_patterns %s: would empty the group (a dialect bridge must keep >=1 pattern)", at, eo.ID))
			}
		}
		for _, r := range o.Relations {
			if nodeType[r.Src] != "CorrelationGroup" {
				errs = append(errs, fmt.Sprintf("%s: overlay relation source %q is not a known phenomenon", at, r.Src))
			}
			if nodeType[r.Dst] != "CorrelationGroup" {
				errs = append(errs, fmt.Sprintf("%s: overlay relation destination %q is not a known phenomenon", at, r.Dst))
			}
			if strings.TrimSpace(r.Why) == "" {
				errs = append(errs, fmt.Sprintf("%s: overlay relation %s->%s needs a 'why' (surfaced verbatim)", at, r.Src, r.Dst))
			}
		}
		ids := make([]string, 0, len(o.Spans))
		for id := range o.Spans {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			s := o.Spans[id]
			if typ := nodeType[id]; typ == "" {
				errs = append(errs, fmt.Sprintf("%s: span for unknown phenomenon %q", at, id))
			} else if typ != "CorrelationGroup" {
				errs = append(errs, fmt.Sprintf("%s: span target %q is a %s, not a CorrelationGroup", at, id, typ))
			}
			if prev, dup := spanOf[id]; dup && prev != s.Span {
				errs = append(errs, fmt.Sprintf("%s: %s: span conflict across overlays (%q vs %q)", at, id, prev, s.Span))
			}
			spanOf[id] = s.Span
			if !ovSpanVocab[s.Span] {
				errs = append(errs, fmt.Sprintf("%s: %s: invalid span %q", at, id, s.Span))
			}
			for _, t := range s.TraversalEdgeTypes {
				if !ovEdgeVocab[t] {
					errs = append(errs, fmt.Sprintf("%s: %s: unknown traversal edge type %q", at, id, t))
				}
			}
			if s.Span != "entity-local" && len(s.TraversalEdgeTypes) == 0 && ovSpanVocab[s.Span] {
				errs = append(errs, fmt.Sprintf("%s: %s: span %s requires traversal edge types", at, id, s.Span))
			}
			if s.Span == "entity-local" && len(s.TraversalEdgeTypes) > 0 {
				errs = append(errs, fmt.Sprintf("%s: %s: entity-local span must not declare traversal edges", at, id))
			}
			if strings.TrimSpace(s.Rationale) == "" {
				errs = append(errs, fmt.Sprintf("%s: %s: span declaration needs a rationale (falsifiable claim, doc 02 §3.6)", at, id))
			}
		}
		for _, r := range o.Rules {
			if strings.TrimSpace(r.ID) == "" {
				errs = append(errs, fmt.Sprintf("%s: rule with missing id", at))
				continue
			}
			if prev, dup := seenRule[r.ID]; dup {
				errs = append(errs, fmt.Sprintf("%s: duplicate rule id %s (also in %s)", at, r.ID, prev))
			}
			seenRule[r.ID] = at
			if typ := nodeType[r.Signal]; typ == "" {
				errs = append(errs, fmt.Sprintf("%s: %s: unknown signal %q", at, r.ID, r.Signal))
			} else if typ != "Signal" {
				errs = append(errs, fmt.Sprintf("%s: %s: rule target %q is a %s, not a Signal", at, r.ID, r.Signal, typ))
			}
			if strings.TrimSpace(r.Metric) == "" {
				errs = append(errs, fmt.Sprintf("%s: %s: missing metric", at, r.ID))
			}
			if !ovKindVocab[r.Kind] {
				errs = append(errs, fmt.Sprintf("%s: %s: unknown kind %q", at, r.ID, r.Kind))
			}
			if !ovDirectionVocab[r.Direction] {
				errs = append(errs, fmt.Sprintf("%s: %s: direction must be above|below", at, r.ID))
			}
			if !ovScopeVocab[r.EntityScope] {
				errs = append(errs, fmt.Sprintf("%s: %s: unknown entity_scope %q", at, r.ID, r.EntityScope))
			}
			if _, err := time.ParseDuration(r.Window); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %s: bad window %q", at, r.ID, r.Window))
			}
			if r.Eligibility != "" && !ovPathVocab[r.Eligibility] {
				errs = append(errs, fmt.Sprintf("%s: %s: unknown eligibility_config_path %q", at, r.ID, r.Eligibility))
			}
			switch r.Kind {
			case "config-relative":
				if !ovPathVocab[r.ConfigPath] {
					errs = append(errs, fmt.Sprintf("%s: %s: config-relative rule needs a known config_path, got %q", at, r.ID, r.ConfigPath))
				}
				if r.Factor <= 0 {
					errs = append(errs, fmt.Sprintf("%s: %s: config-relative rule needs factor > 0", at, r.ID))
				}
			case "absolute", "rate-of-change":
				if r.Default == nil {
					errs = append(errs, fmt.Sprintf("%s: %s: %s rule needs an ontology default (flagged)", at, r.ID, r.Kind))
				}
				if r.ConfigPath != "" {
					errs = append(errs, fmt.Sprintf("%s: %s: %s rule must not carry config_path", at, r.ID, r.Kind))
				}
			}
		}

		// Anchors (doc 07 §3.2): a known phenomenon + a known entity kind; the
		// span/anchor coherence check runs after every overlay merged (below).
		anchorIDs := make([]string, 0, len(o.Anchors))
		for id := range o.Anchors {
			anchorIDs = append(anchorIDs, id)
		}
		sort.Strings(anchorIDs)
		for _, id := range anchorIDs {
			kind := o.Anchors[id]
			if typ := nodeType[id]; typ == "" {
				errs = append(errs, fmt.Sprintf("%s: anchor for unknown phenomenon %q", at, id))
			} else if typ != "CorrelationGroup" {
				errs = append(errs, fmt.Sprintf("%s: anchor target %q is a %s, not a CorrelationGroup", at, id, typ))
			}
			if !ovScopeVocab[kind] {
				errs = append(errs, fmt.Sprintf("%s: %s: unknown anchor kind %q", at, id, kind))
			}
			if prev, dup := anchorOf[id]; dup && prev != kind {
				errs = append(errs, fmt.Sprintf("%s: %s: anchor conflict across overlays (%q vs %q)", at, id, prev, kind))
			}
			anchorOf[id] = kind
		}

		// Member checks (doc 07 §3.1): referential (phenomenon + member signal),
		// vocabulary, one check per (phenomenon, signal) across ALL overlays.
		checkIDs := make([]string, 0, len(o.Checks))
		for id := range o.Checks {
			checkIDs = append(checkIDs, id)
		}
		sort.Strings(checkIDs)
		for _, phen := range checkIDs {
			if typ := nodeType[phen]; typ == "" {
				errs = append(errs, fmt.Sprintf("%s: checks for unknown phenomenon %q", at, phen))
				continue
			} else if typ != "CorrelationGroup" {
				errs = append(errs, fmt.Sprintf("%s: check target %q is a %s, not a CorrelationGroup", at, phen, typ))
				continue
			}
			for i, c := range o.Checks[phen] {
				hasChecks[phen] = true
				if nodeType[c.Signal] != "Signal" {
					errs = append(errs, fmt.Sprintf("%s: %s check %d: unknown signal %q", at, phen, i, c.Signal))
				} else if !memberOf[phen][c.Signal] {
					errs = append(errs, fmt.Sprintf("%s: %s check %d: signal %q is not a member (a check cannot bind an undeclared member)", at, phen, i, c.Signal))
				}
				if strings.TrimSpace(c.Metric) == "" {
					errs = append(errs, fmt.Sprintf("%s: %s check %d: missing metric", at, phen, i))
				}
				if !ovFacetVocab[c.Facet] {
					errs = append(errs, fmt.Sprintf("%s: %s check %d: unknown facet %q", at, phen, i, c.Facet))
				}
				if !ovExpectVocab[c.Expect] {
					errs = append(errs, fmt.Sprintf("%s: %s check %d: unknown expect %q", at, phen, i, c.Expect))
				}
				if !ovMinStateVocab[c.MinState] {
					errs = append(errs, fmt.Sprintf("%s: %s check %d: unknown min_state %q", at, phen, i, c.MinState))
				}
				if c.MinState != "" && c.Facet != "slope" {
					errs = append(errs, fmt.Sprintf("%s: %s check %d: min_state is only meaningful on a slope facet", at, phen, i))
				}
				if !ovOnVocab[c.On] {
					errs = append(errs, fmt.Sprintf("%s: %s check %d: unknown on %q (anchor|neighbour)", at, phen, i, c.On))
				}
				if c.On == "neighbour" || c.On == "two-hop" {
					hasNeighbourCheck[phen] = true
				}
				if c.On == "two-hop" {
					hasTwoHopCheck[phen] = true
				}
				if seenCheck[phen] == nil {
					seenCheck[phen] = map[string]bool{}
				}
				if seenCheck[phen][c.Signal] {
					errs = append(errs, fmt.Sprintf("%s: %s: duplicate check for signal %q", at, phen, c.Signal))
				}
				seenCheck[phen][c.Signal] = true
			}
		}
	}

	// Cross-overlay coherence (mirrors the runtime loader's finalizeOverlays):
	// span, anchor, and checks may each come from a different file.
	finalIDs := make([]string, 0, len(hasChecks)+len(anchorOf))
	seenFinal := map[string]bool{}
	for id := range hasChecks {
		finalIDs = append(finalIDs, id)
		seenFinal[id] = true
	}
	for id := range anchorOf {
		if !seenFinal[id] {
			finalIDs = append(finalIDs, id)
		}
	}
	sort.Strings(finalIDs)
	for _, id := range finalIDs {
		span := spanOf[id]
		spanned := span != "" && span != "entity-local"
		if anchorOf[id] != "" && !spanned {
			errs = append(errs, fmt.Sprintf("%s: anchor %q declared but span is %q — anchors are for spanned phenomena only", id, anchorOf[id], span))
		}
		if hasNeighbourCheck[id] && !spanned {
			errs = append(errs, fmt.Sprintf("%s: neighbour-scoped check on a non-spanned phenomenon (span %q)", id, span))
		}
		if hasTwoHopCheck[id] && span != "second-order" {
			errs = append(errs, fmt.Sprintf("%s: two-hop check requires a second-order span, got %q", id, span))
		}
		if spanned && hasChecks[id] && anchorOf[id] == "" {
			errs = append(errs, fmt.Sprintf("%s: spanned phenomenon with checks must declare an anchor (doc 07 §3.2)", id))
		}
	}
	return errs
}

// mergeOverlays applies span declarations onto the document's CorrelationGroup
// nodes (for the merged gap analysis) and returns the structured-rule count.
func mergeOverlays(doc *kgDoc, ovls []overlayDoc) (rules int) {
	spans := map[string]string{}
	for _, o := range ovls {
		// doc 15 cap. A: overlay-added signals become first-class nodes so the gap
		// report's signal total + Metric-shape accounting reflect the MERGED view.
		for _, os := range o.Signals {
			modality := os.Modality
			if modality == "" {
				modality = "Metric"
			}
			doc.Nodes = append(doc.Nodes, kgNode{ID: os.ID, Type: "Signal", Modality: modality, DataType: os.DataType})
		}
		// doc 15 Phase C: overlay-added phenomena become first-class nodes so the
		// gap report counts them (40, not 38) and credits their spans below.
		for _, op := range o.Phenomena {
			doc.Nodes = append(doc.Nodes, kgNode{ID: op.ID, Type: "CorrelationGroup"})
		}
		for id, s := range o.Spans {
			spans[id] = s.Span
		}
		rules += len(o.Rules)
	}
	for i := range doc.Nodes {
		n := &doc.Nodes[i]
		if n.Type == "CorrelationGroup" && n.Span == "" {
			if s, ok := spans[n.ID]; ok {
				n.Span = s
			}
		}
	}
	return rules
}
