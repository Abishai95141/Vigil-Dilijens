package binding

import (
	"fmt"
	"regexp"
	"sort"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
)

// Equivalence-group resolution (doc 04 §3.1 mechanism 2, axis 1 of §3.3): whatever
// the customer's exporter calls a quantity, it maps to the canonical variable
// through the group's authored variants. Resolution happens ONCE at binding time
// per stream name; the match is recorded on the binding (which variant matched),
// and a name matching MORE than one group is a semantic red flag handed to QA —
// never silently disambiguated.

// EquivalenceMatch is one resolved name→canonical mapping.
type EquivalenceMatch struct {
	GroupID   string // EquivalenceGroup node id
	Canonical string // canonical (OTel) variable name
	Variant   string // the authored pattern that matched (provenance)
}

// EquivalenceResolver holds the compiled pattern set of every authored group.
type EquivalenceResolver struct {
	groups []compiledGroup
}

type compiledGroup struct {
	id        string
	canonical string
	patterns  []*regexp.Regexp
	raw       []string
}

// NewEquivalenceResolver compiles every group's patterns. A non-compiling pattern
// is an authoring defect and fails loudly (it would otherwise silently shrink the
// dialect bridge).
func NewEquivalenceResolver(g *graph.Graph) (*EquivalenceResolver, error) {
	ids := make([]string, 0, len(g.EquivalenceGroups))
	for id := range g.EquivalenceGroups {
		ids = append(ids, id)
	}
	sort.Strings(ids) // deterministic resolution order
	r := &EquivalenceResolver{}
	for _, id := range ids {
		eg := g.EquivalenceGroups[id]
		cg := compiledGroup{id: id, canonical: eg.CanonicalOTel}
		for _, p := range eg.Patterns {
			re, err := regexp.Compile(p)
			if err != nil {
				return nil, fmt.Errorf("equivalence group %s: pattern %q does not compile: %w", id, p, err)
			}
			cg.patterns = append(cg.patterns, re)
			cg.raw = append(cg.raw, p)
		}
		r.groups = append(r.groups, cg)
	}
	return r, nil
}

// Resolve maps a stream name to its canonical variable. Returns every matching
// group: exactly one match is a resolution; zero means the name is outside the
// authored dialect bridge (NOT an error — the unexplained channel exists for a
// reason); more than one is ambiguous and must be surfaced as suspect by QA,
// never silently picked.
func (r *EquivalenceResolver) Resolve(streamName string) []EquivalenceMatch {
	var out []EquivalenceMatch
	for _, g := range r.groups {
		for i, re := range g.patterns {
			if re.MatchString(streamName) {
				out = append(out, EquivalenceMatch{GroupID: g.id, Canonical: g.canonical, Variant: g.raw[i]})
				break // one variant per group is enough; group membership is the claim
			}
		}
	}
	return out
}

// Groups returns the number of compiled groups (for the coverage report).
func (r *EquivalenceResolver) Groups() int { return len(r.groups) }
