package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/detect"
)

// renderFindings writes the entity-local detection findings (doc 07) to w — the
// strongest claim the system makes about the present: a MEASURED co-occurrence in
// an AUTHORED pattern, the two kept adjacent and never fused. Each finding shows
// its match quality, the per-member evidence with the AUTHORED note (the only
// "why"), and what was unobservable. A single crossing is not here — only matched
// phenomena surface (doc 07 §3.5).
func renderFindings(w io.Writer, findings []detect.Finding) {
	const bar = "================================================================================"
	fmt.Fprintln(w, bar)
	if len(findings) == 0 {
		fmt.Fprintln(w, " detection (doc 07, entity-local) — no phenomena matched this tick")
		fmt.Fprintln(w, bar)
		return
	}
	fmt.Fprintf(w, " detection (doc 07, entity-local) — %d phenomenon match(es)\n", len(findings))
	for _, f := range findings {
		ent := f.Name
		if f.Namespace != "" {
			ent = f.Namespace + "/" + f.Name
		}
		fmt.Fprintf(w, "\n  ▸ %s  [%s match · completeness %.0f%%]\n", f.Label, f.Quality, f.Completeness*100)
		fmt.Fprintf(w, "    entity: %s (%s)\n", ent, f.Kind)
		fmt.Fprintf(w, "    required: %d met of %d (%d unobservable here); supporting: %d/%d\n",
			f.RequiredMet, f.RequiredTotal, f.RequiredUnobserved, f.SupportingMet, f.SupportingObservble)
		for _, mem := range f.Members {
			barProv := "config"
			if mem.BarFlagged {
				barProv = "default-flagged"
			}
			fmt.Fprintf(w, "      • %s [%s] %s=%s (bar:%s) — %s\n",
				mem.Role, mem.Temporal, shortMetric(mem.Metric), mem.State, barProv, authoredNote(mem.Note))
		}
		for _, u := range f.Unobservable {
			fmt.Fprintf(w, "      ! unobservable required member: %s\n", u)
		}
		fmt.Fprintf(w, "    provenance: MEASURED match · AUTHORED pattern %s · graph %s\n",
			f.Phenomenon, shortUID(strings.TrimPrefix(f.GraphVersion, "sha256:")))
	}
	fmt.Fprintln(w, bar)
}

// authoredNote trims a member's authored note for one line; it is verbatim graph
// content (the only "why" the engine shows, doc 07 §4), never generated.
func authoredNote(n string) string {
	n = strings.TrimSpace(n)
	if n == "" {
		return "(no authored note)"
	}
	if len(n) > 90 {
		return n[:88] + "…"
	}
	return n
}
