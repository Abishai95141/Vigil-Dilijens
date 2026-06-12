package main

import (
	"fmt"
	"os"
	"path/filepath"
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

type overlayDoc struct {
	path    string
	Overlay string               `yaml:"overlay"`
	Version int                  `yaml:"version"`
	Author  string               `yaml:"author"`
	Status  string               `yaml:"status"`
	Spans   map[string]ovSpan    `yaml:"spans"`
	Rules   []ovRule             `yaml:"rules"`
	Checks  map[string][]ovCheck `yaml:"checks"`
	Anchors map[string]string    `yaml:"anchors"`
}

var (
	ovSpanVocab      = map[string]bool{"entity-local": true, "first-order": true, "second-order": true}
	ovEdgeVocab      = map[string]bool{"runs-on": true, "mounts": true, "selects": true, "node-lease": true}
	ovKindVocab      = map[string]bool{"config-relative": true, "absolute": true, "rate-of-change": true, "co-occurrence": true}
	ovDirectionVocab = map[string]bool{"above": true, "below": true}
	ovScopeVocab     = map[string]bool{"Container": true, "Pod": true, "Node": true, "PVC": true}
	ovFacetVocab     = map[string]bool{"level": true, "slope": true, "ratio": true, "rate-guard": true}
	ovExpectVocab    = map[string]bool{"rising": true, "falling": true, "crossed": true, "at-or-above": true, "breached": true}
	ovMinStateVocab  = map[string]bool{"": true, "at-threshold": true, "above": true, "well-above": true}
	ovOnVocab        = map[string]bool{"": true, "anchor": true, "neighbour": true, "two-hop": true}
	ovPathVocab      = map[string]bool{
		"container.resources.limits.memory":   true,
		"container.resources.limits.cpu":      true,
		"pvc.spec.resources.requests.storage": true,
		"node.status.allocatable.memory":      true,
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

	var errs []string
	seenRule := map[string]string{}
	// Cross-overlay state for the finalize checks (span/anchor/neighbour
	// coherence — spans, checks, and anchors may arrive from different files).
	spanOf := map[string]string{}             // phen -> declared span (across all overlays)
	anchorOf := map[string]string{}           // phen -> declared anchor
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
