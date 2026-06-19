package candidate

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Equivalence-group mapping candidate (doc 21 §5). A KindEquivGroup candidate proposes that
// an unmapped OPERATIONAL stray metric belongs in an equivalence group — either an EXISTING
// group (GroupID set) or a NEW one (NewGroup set). On promotion a named human authors the
// regex into an equivalence_groups overlay block (PromotedEquivGroupOverlayYAML); on reload
// the deterministic binding.EquivalenceResolver absorbs the pattern and the metric stops
// being a stray. This is the ONE promotion that moves MEASURED coverage. The agent only
// proposes; the pattern it suggests is a deterministic regex, never a learned weight.

// EquivGroupProposal is the typed payload of a KindEquivGroup candidate. Exactly one of
// GroupID (map into an existing group) or NewGroup (define a new group) is set. Pattern is
// the regex that would capture Metric; CaptureSample is the deterministic SUPPORT — the
// other current strays the same pattern would also absorb (computed by EquivGroupSupport,
// surfaced for the reviewer's diff: "this pattern also captures these N strays").
type EquivGroupProposal struct {
	Metric        string         // the stray metric being mapped (the focal subject)
	GroupID       string         // map into an EXISTING group; mutually exclusive with NewGroup
	NewGroup      *NewEquivGroup // define a NEW group; mutually exclusive with GroupID
	Pattern       string         // the regex that captures Metric (and CaptureSample)
	CaptureSample []string       // other current strays this pattern would also capture (support)
}

// NewEquivGroup is the identity of a proposed new equivalence group: a stable id, a human
// label, and the canonical OTel variable the dialect maps onto.
type NewEquivGroup struct {
	ID            string
	Label         string
	CanonicalOTel string
}

// EquivGroupSubject renders the canonical subject for a stray→group candidate. The subject
// carries the focal metric so StrayCandidateActionable / classification can read it back.
func EquivGroupSubject(metric string) string { return "stray:" + strings.TrimSpace(metric) }

// EquivGroupPayload marshals a proposal into the opaque candidate payload map.
func EquivGroupPayload(p EquivGroupProposal) map[string]any {
	m := map[string]any{
		"metric":  p.Metric,
		"pattern": p.Pattern,
	}
	if p.GroupID != "" {
		m["group_id"] = p.GroupID
	}
	if p.NewGroup != nil {
		m["new_group"] = map[string]any{
			"id":             p.NewGroup.ID,
			"label":          p.NewGroup.Label,
			"canonical_otel": p.NewGroup.CanonicalOTel,
		}
	}
	if len(p.CaptureSample) > 0 {
		sample := append([]string(nil), p.CaptureSample...)
		sort.Strings(sample)
		s := make([]any, len(sample))
		for i, v := range sample {
			s[i] = v
		}
		m["capture_sample"] = s
	}
	return m
}

// ParseEquivGroupPayload reads a proposal back out of the candidate payload map.
func ParseEquivGroupPayload(payload map[string]any) (EquivGroupProposal, error) {
	var p EquivGroupProposal
	p.Metric, _ = payload["metric"].(string)
	p.Pattern, _ = payload["pattern"].(string)
	if gid, ok := payload["group_id"].(string); ok {
		p.GroupID = gid
	}
	if ng, ok := payload["new_group"].(map[string]any); ok {
		g := &NewEquivGroup{}
		g.ID, _ = ng["id"].(string)
		g.Label, _ = ng["label"].(string)
		g.CanonicalOTel, _ = ng["canonical_otel"].(string)
		p.NewGroup = g
	}
	if cs, ok := payload["capture_sample"].([]any); ok {
		for _, v := range cs {
			if s, ok := v.(string); ok {
				p.CaptureSample = append(p.CaptureSample, s)
			}
		}
	}
	return p, nil
}

// ValidateEquivGroupProposal enforces the structural floor BEFORE staging (the dgx verify
// step): a metric, a compiling pattern that actually matches the metric, exactly one target
// (existing group XOR new group), and a complete identity for a new group. It is the same
// discipline the overlay loader applies at promotion — a proposal that could never compile
// or never match its own metric is rejected here, not surfaced to a human.
func ValidateEquivGroupProposal(p EquivGroupProposal) error {
	if strings.TrimSpace(p.Metric) == "" {
		return fmt.Errorf("equiv_group: empty metric")
	}
	if strings.TrimSpace(p.Pattern) == "" {
		return fmt.Errorf("equiv_group: empty pattern")
	}
	re, err := regexp.Compile(p.Pattern)
	if err != nil {
		return fmt.Errorf("equiv_group: pattern %q does not compile: %w", p.Pattern, err)
	}
	if !re.MatchString(p.Metric) {
		return fmt.Errorf("equiv_group: pattern %q does not match its own metric %q", p.Pattern, p.Metric)
	}
	hasExisting := strings.TrimSpace(p.GroupID) != ""
	hasNew := p.NewGroup != nil
	if hasExisting == hasNew {
		return fmt.Errorf("equiv_group: exactly one of group_id (existing) or new_group must be set")
	}
	if hasNew {
		if strings.TrimSpace(p.NewGroup.ID) == "" || strings.TrimSpace(p.NewGroup.Label) == "" || strings.TrimSpace(p.NewGroup.CanonicalOTel) == "" {
			return fmt.Errorf("equiv_group: a new group needs id + label + canonical_otel")
		}
	}
	return nil
}

// EquivGroupSupport is the deterministic SUPPORT of a proposed pattern: the subset of the
// given stray metric names the pattern would capture, sorted. This is the MEASURED score the
// review UI shows ("this pattern also captures these N strays") — a count of facts, never a
// model confidence or a learned probability. A non-compiling pattern is an error, not a
// silent empty set.
func EquivGroupSupport(pattern string, strays []string) ([]string, error) {
	re, err := regexp.Compile(strings.TrimSpace(pattern))
	if err != nil {
		return nil, fmt.Errorf("equiv_group support: pattern %q does not compile: %w", pattern, err)
	}
	seen := map[string]bool{}
	var out []string
	for _, s := range strays {
		if re.MatchString(s) && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out, nil
}

// equivGroupOverlayEntry is one rendered equivalence_groups entry (matches graph.overlayFile).
type equivGroupOverlayEntry struct {
	ID            string   `yaml:"id"`
	Label         string   `yaml:"label,omitempty"`
	CanonicalOTel string   `yaml:"canonical_otel,omitempty"`
	Patterns      []string `yaml:"patterns,omitempty"`
	AddPatterns   []string `yaml:"add_patterns,omitempty"`
	Rationale     string   `yaml:"rationale"`
}

// equivGroupOverlayDoc is the rendered overlay file for a promoted equiv_group candidate.
type equivGroupOverlayDoc struct {
	Overlay     string                   `yaml:"overlay"`
	Version     int                      `yaml:"version"`
	Author      string                   `yaml:"author"`
	Status      string                   `yaml:"status"`
	EquivGroups []equivGroupOverlayEntry `yaml:"equivalence_groups"`
}

const equivGroupOverlayHeader = "# Promoted equivalence-group mapping — AUTHORED overlay (doc 21 §5).\n" +
	"# Commit to ontology/graph/overlays/, add to a new release, run `graphlint -strict`, reload.\n" +
	"# On reload binding.EquivalenceResolver absorbs the pattern and the metric stops being a\n" +
	"# stray — the one promotion that moves MEASURED coverage. The named human owns the rationale.\n"

// PromotedEquivGroupOverlayYAML renders the committable equivalence_groups overlay for a
// PROMOTED KindEquivGroup candidate. Unlike the generic promotion artifact, this is a REAL
// overlay block graph.applyOverlay + graphlint accept verbatim. It refuses a non-promoted
// candidate, a missing author, a wrong kind, or an empty note (the rationale a falsifiable
// authored delta requires, doc 02 §3.6). Pure given the candidate + graph version.
func PromotedEquivGroupOverlayYAML(c Candidate, graphVersion string) (string, error) {
	if c.Kind != KindEquivGroup {
		return "", fmt.Errorf("candidate %s is kind %q, not %q", c.ID, c.Kind, KindEquivGroup)
	}
	if c.Status != StatusPromoted {
		return "", fmt.Errorf("candidate %s is not promoted (status %q)", c.ID, c.Status)
	}
	if c.DecidedBy == "" {
		return "", fmt.Errorf("candidate %s has no named human — an authored overlay needs an author", c.ID)
	}
	if strings.TrimSpace(c.Note) == "" {
		return "", fmt.Errorf("candidate %s: an equivalence-group promotion needs an authored note (the rationale a falsifiable delta requires)", c.ID)
	}
	p, err := ParseEquivGroupPayload(c.Payload)
	if err != nil {
		return "", err
	}
	if err := ValidateEquivGroupProposal(p); err != nil {
		return "", err
	}
	var entry equivGroupOverlayEntry
	entry.Rationale = c.Note
	if p.GroupID != "" {
		entry.ID = p.GroupID
		entry.AddPatterns = []string{p.Pattern}
	} else {
		entry.ID = p.NewGroup.ID
		entry.Label = p.NewGroup.Label
		entry.CanonicalOTel = p.NewGroup.CanonicalOTel
		entry.Patterns = []string{p.Pattern}
	}
	doc := equivGroupOverlayDoc{
		Overlay:     "equiv-group-" + shortID(c.ID),
		Version:     1,
		Author:      c.DecidedBy,
		Status:      "promoted",
		EquivGroups: []equivGroupOverlayEntry{entry},
	}
	raw, err := yaml.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("candidate: render equiv-group overlay: %w", err)
	}
	return equivGroupOverlayHeader + string(raw), nil
}

// shortID returns a stable short suffix of a content id for an overlay file name.
func shortID(id string) string {
	s := strings.TrimPrefix(id, "cand:")
	if len(s) > 12 {
		s = s[:12]
	}
	if s == "" {
		s = "promoted"
	}
	return s
}
