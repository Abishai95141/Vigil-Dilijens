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

type overlayDoc struct {
	path    string
	Overlay string            `yaml:"overlay"`
	Version int               `yaml:"version"`
	Author  string            `yaml:"author"`
	Status  string            `yaml:"status"`
	Spans   map[string]ovSpan `yaml:"spans"`
	Rules   []ovRule          `yaml:"rules"`
}

var (
	ovSpanVocab      = map[string]bool{"entity-local": true, "first-order": true, "second-order": true}
	ovEdgeVocab      = map[string]bool{"runs-on": true, "mounts": true, "selects": true, "node-lease": true}
	ovKindVocab      = map[string]bool{"config-relative": true, "absolute": true, "rate-of-change": true, "co-occurrence": true}
	ovDirectionVocab = map[string]bool{"above": true, "below": true}
	ovScopeVocab     = map[string]bool{"Container": true, "Pod": true, "Node": true, "PVC": true}
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
	var errs []string
	seenRule := map[string]string{}
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
