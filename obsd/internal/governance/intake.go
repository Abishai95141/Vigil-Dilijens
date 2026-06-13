package governance

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/binding"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/unexplained"
)

// Curation intake — how the graph grows (doc 12 §3.6). Three feeds terminate here,
// NONE of which writes to the graph itself:
//
//   - Candidate phenomena from the unexplained channel (08 §3.6): recurring
//     loud-but-unmatched patterns, aggregated with evidence.
//   - Coverage gaps from binding (04): unresolved/suspect bindings, unbounded
//     workloads suggesting missing default policy.
//   - Falsification discrepancies from the harness (11 §3.3): tags or spans
//     contradicted by the corpus.
//
// Each becomes a triaged PROPOSAL in the workflow above. The loop closes the system's
// only learning path — the product never learns; the knowledge base grows, through
// HUMANS. The intake queue's volume and age are themselves visible governance-health
// metrics (doc 12 §6: knowledge stagnation is countered by making intake visible).

// IntakeSource names which feed an item came from.
type IntakeSource string

const (
	SourceUnexplained   IntakeSource = "unexplained-candidate" // 08 §3.6
	SourceCoverageGap   IntakeSource = "coverage-gap"          // 04
	SourceFalsification IntakeSource = "falsification"         // 11 §3.3
)

// IntakeItem is a triaged candidate for human curation. It PROPOSES; it never
// authors (doc 12 §3.6). The Rationale carries no causal vocabulary — only the
// recurrence/coverage/contradiction fact that motivates a human to look.
type IntakeItem struct {
	Source         IntakeSource  `json:"source"`
	Signature      string        `json:"signature"`      // dedup key
	Summary        string        `json:"summary"`        // curator-readable, no causal vocabulary
	Recurrence     int           `json:"recurrence"`     // ranking signal (windows / entities / occurrences)
	Evidence       []EvidenceRef `json:"evidence"`       //
	ProposedAction string        `json:"proposedAction"` // what KIND of authoring this suggests
}

// FromUnexplainedCandidates turns recurring loud-but-unmatched candidate reports
// (08 §3.6) into intake items proposing a new authored phenomenon.
func FromUnexplainedCandidates(cands []unexplained.CandidateReport) []IntakeItem {
	out := make([]IntakeItem, 0, len(cands))
	for _, c := range cands {
		sig := "unexplained:" + c.EntityKind + ":" + strings.Join(c.Metrics, ",")
		out = append(out, IntakeItem{
			Source:    SourceUnexplained,
			Signature: sig,
			Summary: fmt.Sprintf("recurring unexplained loudness on {%s} across %d %s, %d window(s)",
				strings.Join(c.Metrics, ", "), len(c.Entities), c.EntityKind, c.Windows),
			Recurrence:     c.Windows,
			Evidence:       []EvidenceRef{{Kind: "unexplained-recurrence", Ref: sig}},
			ProposedAction: "author candidate phenomenon (members from the recurring loud-signal set)",
		})
	}
	return out
}

// FromCoverageGaps turns binding coverage gaps (04) into intake items: unbounded
// workloads suggest missing default policy; failed/suspect semantic-QA findings
// suggest equivalence or rule review.
func FromCoverageGaps(rep *binding.CoverageReport) []IntakeItem {
	var out []IntakeItem
	// Unbounded workloads cluster → missing default-policy proposal.
	if n := len(rep.UnboundedWorkloads); n > 0 {
		// Aggregate by metric (the policy gap is per-variable, not per-instance).
		byMetric := map[string]int{}
		for _, w := range rep.UnboundedWorkloads {
			byMetric[unboundedMetric(w)]++
		}
		for _, metric := range sortedKeysInt(byMetric) {
			out = append(out, IntakeItem{
				Source:         SourceCoverageGap,
				Signature:      "unbounded:" + metric,
				Summary:        fmt.Sprintf("%d workload(s) unbounded on %s — no crossable limit, Tier-B ineligible", byMetric[metric], metric),
				Recurrence:     byMetric[metric],
				Evidence:       []EvidenceRef{{Kind: "coverage-report", Ref: "unbounded:" + metric}},
				ProposedAction: "author a default-policy threshold rule (flagged) or confirm intentional unboundedness",
			})
		}
	}
	// Failed semantic-QA findings → equivalence/rule review.
	for _, f := range rep.Validation.Findings {
		if strings.HasPrefix(f, "FAILED") {
			out = append(out, IntakeItem{
				Source:         SourceCoverageGap,
				Signature:      "qa-failed:" + f,
				Summary:        "semantic-QA failure: " + f,
				Recurrence:     1,
				Evidence:       []EvidenceRef{{Kind: "binding-qa", Ref: f}},
				ProposedAction: "review the equivalence group / threshold rule the QA contradicts",
			})
		}
	}
	return out
}

// FalsificationDiscrepancy is a harness finding that a tag or span is contradicted
// by the corpus (doc 11 §3.3) — the third intake feed.
type FalsificationDiscrepancy struct {
	Element   string // phenomenon/tag/span id contradicted
	Claim     string // the authored claim that failed
	Evidence  string // corpus reference
	Occurrend int    // how many corpus cases contradicted it
}

// FromFalsification turns harness falsification discrepancies into intake items
// proposing a tag/span revision.
func FromFalsification(ds []FalsificationDiscrepancy) []IntakeItem {
	out := make([]IntakeItem, 0, len(ds))
	for _, d := range ds {
		out = append(out, IntakeItem{
			Source:         SourceFalsification,
			Signature:      "falsified:" + d.Element + ":" + d.Claim,
			Summary:        fmt.Sprintf("authored claim contradicted by corpus: %s — %s", d.Element, d.Claim),
			Recurrence:     d.Occurrend,
			Evidence:       []EvidenceRef{{Kind: "falsification-corpus", Ref: d.Evidence}},
			ProposedAction: "revise the contradicted tag/span (behavioural-class change through the workflow)",
		})
	}
	return out
}

// Triage merges the three feeds into one deduped, ranked queue (doc 12 §3.6: each
// becomes a triaged proposal). Dedup is by signature; ranking is recurrence-desc
// within a source-priority order (falsification first — a contradicted claim is the
// most urgent; then unexplained growth; then coverage policy gaps).
func Triage(feeds ...[]IntakeItem) []IntakeItem {
	seen := map[string]bool{}
	var all []IntakeItem
	for _, f := range feeds {
		for _, it := range f {
			if seen[it.Signature] {
				continue
			}
			seen[it.Signature] = true
			all = append(all, it)
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		pi, pj := sourcePriority(all[i].Source), sourcePriority(all[j].Source)
		if pi != pj {
			return pi < pj
		}
		if all[i].Recurrence != all[j].Recurrence {
			return all[i].Recurrence > all[j].Recurrence
		}
		return all[i].Signature < all[j].Signature
	})
	return all
}

func sourcePriority(s IntakeSource) int {
	switch s {
	case SourceFalsification:
		return 0
	case SourceUnexplained:
		return 1
	case SourceCoverageGap:
		return 2
	default:
		return 3
	}
}

// QueueStats is the governance-health view of the intake queue (doc 12 §6: intake
// volume + age made visible so knowledge stagnation is a measured failure, not a
// silent one).
type QueueStats struct {
	Total    int
	BySource map[IntakeSource]int
}

// Stats summarizes a triaged queue.
func Stats(items []IntakeItem) QueueStats {
	st := QueueStats{Total: len(items), BySource: map[IntakeSource]int{}}
	for _, it := range items {
		st.BySource[it.Source]++
	}
	return st
}

// unboundedMetric extracts the metric name from an UnboundedWorkloads entry. The
// report formats entries as "<key> <metric>: <reason>" or similar; we take the last
// space-delimited token before a colon as a best-effort metric handle. Falls back to
// the whole string (dedup still works, just per-entry).
func unboundedMetric(w string) string {
	s := w
	if i := strings.Index(s, ":"); i >= 0 {
		s = s[:i]
	}
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return w
	}
	return fields[len(fields)-1]
}

func sortedKeysInt(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
