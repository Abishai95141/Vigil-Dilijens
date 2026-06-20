package governance

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/binding"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
)

// Promotion PREVIEW (doc 21 §3-4, Phase 3 slice 2). Before a named human promotes an
// equivalence-group candidate, this computes — on a SCRATCH copy of the graph, never the live
// one — exactly which stray metrics the proposed regex would absorb: the deterministic
// stray→group RESOLUTION delta. It is the one promotion that moves MEASURED coverage (doc
// 21 §5), so the operator should see the move BEFORE committing. The result is a COUNT of
// MEASURED facts (which scope metrics newly resolve), never a confidence or a learned score,
// and the function is pure: identical (graph, input) always yields an identical delta.
//
// This package may not import internal/candidate (the read-firewall would couple the
// candidate space to a governance tool that the deterministic migration path also uses), so
// the caller (cmd/obsd, the composition root) parses the candidate payload and passes the
// proposal in plain terms.

// EquivGroupPreviewInput describes a proposed equivalence-group promotion in plain terms.
// Exactly one of TargetGroupID (extend an EXISTING group) or NewGroupID (define a NEW group)
// is set — the same XOR the proposal validator enforces.
type EquivGroupPreviewInput struct {
	TargetGroupID string   // extend this existing group with Pattern
	NewGroupID    string   // OR define this new group around Pattern
	NewCanonical  string   // canonical OTel for a new group (provenance on the scratch group)
	NewLabel      string   // human label for a new group
	Pattern       string   // the proposed dialect regex
	ScopeMetrics  []string // the metrics whose resolution to preview (the focal metric + its capture sample)
}

// EquivGroupPreview is the deterministic stray→group resolution delta a promotion would
// produce. Every slice is a set of MEASURED metric names — a count of facts, never a belief.
type EquivGroupPreview struct {
	GroupID         string   // the group the pattern lands in (existing or new)
	DefinesNewGroup bool     // true ⇒ the promotion defines a brand-new group
	Pattern         string   // the proposed regex (echoed for the surface)
	NewlyResolved   []string // scope metrics UNRESOLVED now → RESOLVED after promotion (the coverage move)
	AlreadyResolved []string // scope metrics that already resolve (the pattern is redundant for them)
	StillUnresolved []string // scope metrics the pattern still would NOT match (honest blind residue)
	GroupsBefore    int      // compiled equivalence groups before
	GroupsAfter     int      // …and after (one more iff a new group is defined)
}

// PreviewEquivGroupPromotion computes the resolution delta on a scratch graph. The live graph
// is NEVER mutated (a test asserts it). Returns an error only for a structurally impossible
// input (nil graph, empty/non-compiling pattern, ambiguous target) — never for an empty delta.
func PreviewEquivGroupPromotion(g *graph.Graph, in EquivGroupPreviewInput) (*EquivGroupPreview, error) {
	if g == nil {
		return nil, fmt.Errorf("governance preview: nil graph")
	}
	if strings.TrimSpace(in.Pattern) == "" {
		return nil, fmt.Errorf("governance preview: empty pattern")
	}
	if _, err := regexp.Compile(in.Pattern); err != nil {
		return nil, fmt.Errorf("governance preview: pattern %q does not compile: %w", in.Pattern, err)
	}
	hasExisting := strings.TrimSpace(in.TargetGroupID) != ""
	hasNew := strings.TrimSpace(in.NewGroupID) != ""
	if hasExisting == hasNew {
		return nil, fmt.Errorf("governance preview: exactly one of TargetGroupID (existing) or NewGroupID (new) must be set")
	}

	before, err := binding.NewEquivalenceResolver(g)
	if err != nil {
		return nil, fmt.Errorf("governance preview: build resolver (before): %w", err)
	}
	scratch := scratchGraphWithPattern(g, in)
	after, err := binding.NewEquivalenceResolver(scratch)
	if err != nil {
		return nil, fmt.Errorf("governance preview: build resolver (after): %w", err)
	}

	p := &EquivGroupPreview{
		Pattern:      in.Pattern,
		GroupsBefore: before.Groups(),
		GroupsAfter:  after.Groups(),
	}
	if hasNew {
		p.GroupID, p.DefinesNewGroup = in.NewGroupID, true
	} else {
		p.GroupID = in.TargetGroupID
	}
	seen := map[string]bool{}
	for _, m := range in.ScopeMetrics {
		if strings.TrimSpace(m) == "" || seen[m] {
			continue
		}
		seen[m] = true
		switch {
		case len(before.Resolve(m)) > 0:
			p.AlreadyResolved = append(p.AlreadyResolved, m)
		case len(after.Resolve(m)) > 0:
			p.NewlyResolved = append(p.NewlyResolved, m)
		default:
			p.StillUnresolved = append(p.StillUnresolved, m)
		}
	}
	sort.Strings(p.NewlyResolved)
	sort.Strings(p.AlreadyResolved)
	sort.Strings(p.StillUnresolved)
	return p, nil
}

// scratchGraphWithPattern builds a throwaway graph whose equivalence groups are a DEEP-ENOUGH
// copy of the live ones (patterns slice copied) plus the proposed pattern. The resolver reads
// ONLY EquivalenceGroups, so a bare graph literal with just that field is a faithful scratch.
// The live graph and its groups are never touched.
func scratchGraphWithPattern(g *graph.Graph, in EquivGroupPreviewInput) *graph.Graph {
	groups := make(map[string]*graph.EquivalenceGroup, len(g.EquivalenceGroups)+1)
	for id, eg := range g.EquivalenceGroups {
		cp := *eg
		cp.Patterns = append([]string(nil), eg.Patterns...) // copy the slice — never mutate the live group's backing array
		groups[id] = &cp
	}
	switch {
	case in.NewGroupID != "":
		groups[in.NewGroupID] = &graph.EquivalenceGroup{
			ID: in.NewGroupID, Label: in.NewLabel, CanonicalOTel: in.NewCanonical,
			Patterns: []string{in.Pattern},
		}
	default:
		if eg, ok := groups[in.TargetGroupID]; ok {
			eg.Patterns = append(eg.Patterns, in.Pattern)
		} else {
			// The named existing group is absent from the live graph (a stale proposal):
			// model it as a fresh group so the preview still shows the resolution effect,
			// rather than silently dropping the pattern.
			groups[in.TargetGroupID] = &graph.EquivalenceGroup{
				ID: in.TargetGroupID, CanonicalOTel: in.NewCanonical, Patterns: []string{in.Pattern},
			}
		}
	}
	return &graph.Graph{EquivalenceGroups: groups}
}
