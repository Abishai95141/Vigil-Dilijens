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
	// The coverage frontier (doc 33 §1): how this phenomenon could be closed.
	// GapClass ∈ covered | emission | completeness. (Mapping — a bar-less but
	// observable pair — is rule-level, see UnboundedWorkloads; attribution/"why" is
	// never a coverage-report gap — it is the inference agent's domain, doc 33 §2.)
	GapClass string
	// Closer is the concrete, human-actionable step that would close the gap
	// (deploy a source · add a scrape lane · probe a capability · author members).
	Closer string
}

// ObservabilityReport is the per-phenomenon rollup.
type ObservabilityReport struct {
	PerPhenomenon []PhenomenonCoverage
	Full          int
	Partial       int
	None          int
	// Coverage-frontier rollup (doc 33 §1): the closeable work, by class.
	Covered      int
	Emission     int
	Completeness int
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
		classifyFrontier(&pc)
		switch pc.GapClass {
		case "emission":
			rep.Emission++
		case "completeness":
			rep.Completeness++
		default:
			rep.Covered++
		}
		rep.PerPhenomenon = append(rep.PerPhenomenon, pc)
	}
	return rep
}

// classifyFrontier sets pc.GapClass + pc.Closer — the coverage frontier (doc 33 §1).
// A fully-observable phenomenon is "covered". Otherwise we read its MissingReasons:
// EMISSION (a required member is unobservable because no tool emits it, its endpoint
// isn't scraped, or a capability can't be API-confirmed) dominates COMPLETENESS (the
// phenomenon has no structured members to gate on), because authoring a member cannot
// rescue a signal nothing produces. The Closer names the exact deploy/scrape/probe/
// author step. Mapping (bar-less but observable) is rule-level; attribution is the
// agent's job — neither is a phenomenon-observability gap.
func classifyFrontier(pc *PhenomenonCoverage) {
	if pc.Observability == "full" {
		pc.GapClass = "covered"
		return
	}
	var deploy, scrape, probe []string
	seen := map[string]bool{}
	add := func(set *[]string, v string) {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			return
		}
		seen[v] = true
		*set = append(*set, v)
	}
	for _, r := range pc.MissingReasons {
		switch {
		case strings.Contains(r, "/metrics endpoint not scraped"):
			add(&scrape, before(r, " /metrics"))
		case strings.Contains(r, "not API-derivable"):
			add(&probe, capToken(r))
		case strings.Contains(r, "no emitting tool deployed"):
			add(&deploy, after(r, "deployed: "))
		case strings.Contains(r, "not met ("):
			add(&deploy, deployTarget(r))
			// Anything else (no structured members, meta-member, inline patterns, or an
			// unrecognised reason) is a COMPLETENESS gap — authoring is the catch-all closer,
			// reached via the fall-through below.
		}
	}
	if len(deploy)+len(scrape)+len(probe) > 0 {
		pc.GapClass = "emission"
		var parts []string
		if len(deploy) > 0 {
			parts = append(parts, "deploy "+strings.Join(deploy, ", "))
		}
		if len(scrape) > 0 {
			parts = append(parts, "add scrape lane for "+strings.Join(scrape, ", "))
		}
		if len(probe) > 0 {
			parts = append(parts, "node-probe to assert "+strings.Join(probe, ", "))
		}
		pc.Closer = strings.Join(parts, " · ")
		return
	}
	pc.GapClass = "completeness"
	pc.Closer = "author structured required members (resolve inline patterns into signal nodes)"
}

// small reason-string extractors (the reasons are stable templates from obtain.go).
func before(s, sep string) string {
	if i := strings.Index(s, sep); i >= 0 {
		return s[:i]
	}
	return s
}
func after(s, sep string) string {
	if i := strings.Index(s, sep); i >= 0 {
		return s[i+len(sep):]
	}
	return s
}
func between(s, open, close string) string {
	if i := strings.Index(s, open); i >= 0 {
		rest := s[i+len(open):]
		if j := strings.Index(rest, close); j >= 0 {
			return rest[:j]
		}
	}
	return s
}

// deployTarget extracts the missing source from a "capability CAP_X not met (… deployed)"
// reason: the parenthetical names the absent thing ("fluent-bit not deployed",
// "no log pipeline deployed"). Falls back to the CAP token if it can't be cleaned.
func deployTarget(r string) string {
	in := between(r, "(", ")")
	in = strings.TrimSuffix(in, " not deployed")
	in = strings.TrimSuffix(in, " deployed")
	in = strings.TrimPrefix(in, "no ")
	in = strings.TrimSpace(in)
	if in == "" || in == r {
		return capToken(r)
	}
	return in
}

// capToken pulls the CAP_… token out of a capability reason.
func capToken(s string) string {
	for _, w := range strings.Fields(s) {
		if strings.HasPrefix(w, "CAP_") {
			return strings.TrimRight(w, ":")
		}
	}
	return s
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
