package main

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/selection"
)

// renderSelection prints the monitoring selection verdict (doc 06 M1): the
// Tier-A watch list summary AND the none-list with reasons — attention is
// always auditable, exclusions are never silent.
func renderSelection(w io.Writer, r *selection.Result) {
	if r == nil {
		return
	}
	fmt.Fprintf(w, " selection (doc 06, gates 1-2) — %d entities: %d Tier-A (detection)",
		len(r.Records), r.TierACount)
	reasons := make([]string, 0, len(r.NoneByReason))
	for reason := range r.NoneByReason {
		reasons = append(reasons, string(reason))
	}
	sort.Strings(reasons)
	parts := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		parts = append(parts, fmt.Sprintf("%d %s", r.NoneByReason[selection.Reason(reason)], reason))
	}
	if len(parts) > 0 {
		fmt.Fprintf(w, ", none: %s", strings.Join(parts, " · "))
	}
	fmt.Fprintln(w)

	// The boutique-namespace none-list, named: the honest "why is this not
	// watched" answer per entity (Tier-A entities appear in the fingerprint
	// table below; the excluded ones would otherwise be invisible).
	named := 0
	for _, rec := range r.Records {
		if rec.Tier == selection.TierA || rec.Namespace != boutiqueNamespace {
			continue
		}
		if named == 0 {
			fmt.Fprintf(w, "   not watched in %s:", boutiqueNamespace)
		}
		fmt.Fprintf(w, " %s(%s)", rec.Name, rec.Reason)
		named++
	}
	if named > 0 {
		fmt.Fprintln(w)
	}

	// Neighbourhood closure (doc 06 M2): the span-expanded watch set + every
	// recorded gap. Absences are coverage facts, never silent.
	nbrs, suspect := 0, 0
	for _, rec := range r.Records {
		for _, n := range rec.Neighbourhood {
			nbrs++
			if n.Result == "suspect" {
				suspect++
			}
		}
	}
	if nbrs > 0 || len(r.ClosureGaps) > 0 {
		fmt.Fprintf(w, "   neighbourhood closure (06 M2): %d neighbour ref(s) pulled into the watch set", nbrs)
		if suspect > 0 {
			fmt.Fprintf(w, " (%d over suspect edges)", suspect)
		}
		if len(r.ClosureGaps) > 0 {
			fmt.Fprintf(w, " · %d gap(s)", len(r.ClosureGaps))
		}
		fmt.Fprintln(w)
		for i, gap := range r.ClosureGaps {
			if i == 4 {
				fmt.Fprintf(w, "     … %d more gaps\n", len(r.ClosureGaps)-4)
				break
			}
			fmt.Fprintf(w, "     gap: %s · %s — %s\n", ceiHuman(gap.EntityCEI), gap.Phenomenon, gap.Reason)
		}
	}
	fmt.Fprintf(w, "   tier B (forecasting): structurally present, EMPTY until Phase 2 (09 funnel + budget) · graph %.19s…\n\n", r.GraphVersion)
}
