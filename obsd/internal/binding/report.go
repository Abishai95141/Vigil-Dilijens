package binding

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
)

// Per-phenomenon observability (doc 04 §3.5, doc 05 M4): which of the graph's
// phenomena are fully, partially, or not observable on THIS cluster, derived from
// the availability of their member signals. This feeds degraded-match honesty in
// detection (07): a phenomenon whose required members cannot be observed here can
// never fire fully, and the report says so BEFORE detection ships.

// PhenomenonCoverage is one phenomenon's observability verdict.
type PhenomenonCoverage struct {
	PhenomenonID          string
	Label                 string
	Severity              string // AUTHORED prioritisation (doc 21 Phase 5): critical|high|medium|low|"" (undeclared)
	Observability         string // full | partial | none
	RequiredTotal         int
	RequiredObtainable    int
	RequiredIndeterminate int
	RequiredOutOfScope    int
	// MissingReasons name why required members are unobservable (deduped, sorted).
	MissingReasons []string
}

// ObservabilityReport is the per-phenomenon rollup.
type ObservabilityReport struct {
	PerPhenomenon []PhenomenonCoverage
	Full          int
	Partial       int
	None          int
}

// PhenomenonObservability computes the rollup from member signals' availability.
// Members come from the structured participates_in edges (signal -> phenomenon);
// only role=required members decide observability (corroborating members enrich a
// match but their absence does not degrade it — doc 02 §3.5 member roles).
func PhenomenonObservability(g *graph.Graph, avail *AvailabilityReport) *ObservabilityReport {
	rep := &ObservabilityReport{}
	ids := make([]string, 0, len(g.Phenomena))
	for id := range g.Phenomena {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		p := g.Phenomena[id]
		pc := PhenomenonCoverage{PhenomenonID: id, Label: p.Label, Severity: p.Severity}
		reasons := map[string]bool{}
		for _, m := range p.Members {
			if m.Role != "required" {
				continue
			}
			pc.RequiredTotal++
			av, ok := avail.PerSignal[m.SignalID]
			switch {
			case !ok:
				// A member edge pointing at a non-signal node (meta-phenomena
				// reference other phenomena): counted indeterminate, stated.
				pc.RequiredIndeterminate++
				reasons["member "+m.SignalID+" is not a gateable signal (meta-member)"] = true
			case av.State == Obtainable:
				pc.RequiredObtainable++
			case av.State == Indeterminate:
				pc.RequiredIndeterminate++
				for _, r := range av.Reasons {
					reasons[r] = true
				}
			default:
				pc.RequiredOutOfScope++
				for _, r := range av.Reasons {
					reasons[r] = true
				}
			}
		}
		for r := range reasons {
			pc.MissingReasons = append(pc.MissingReasons, r)
		}
		sort.Strings(pc.MissingReasons)

		switch {
		case pc.RequiredTotal == 0:
			// No structured required members (inline members are patterns, not yet
			// resolved to signal nodes): observability cannot be computed — stated
			// as none-with-reason rather than guessed.
			pc.Observability = "none"
			pc.MissingReasons = append(pc.MissingReasons, "no structured required members to gate on (inline patterns pending resolution)")
			rep.None++
		case pc.RequiredOutOfScope == 0 && pc.RequiredIndeterminate == 0:
			pc.Observability = "full"
			rep.Full++
		case pc.RequiredObtainable > 0:
			pc.Observability = "partial"
			rep.Partial++
		default:
			pc.Observability = "none"
			rep.None++
		}
		rep.PerPhenomenon = append(rep.PerPhenomenon, pc)
	}
	return rep
}

// String renders the rollup header line.
func (r *ObservabilityReport) String() string {
	return fmt.Sprintf("phenomena observability: %d full · %d partial · %d none (of %d)",
		r.Full, r.Partial, r.None, len(r.PerPhenomenon))
}

// Names lists phenomenon ids with the given observability, shortened.
func (r *ObservabilityReport) Names(observability string) []string {
	var out []string
	for _, p := range r.PerPhenomenon {
		if p.Observability == observability {
			out = append(out, strings.TrimPrefix(p.PhenomenonID, "PHEN_"))
		}
	}
	return out
}
