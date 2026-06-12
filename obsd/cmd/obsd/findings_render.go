package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/binding"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/detect"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/unexplained"
)

// renderFindings writes the entity-local detection findings (doc 07) to w — the
// strongest claim the system makes about the present: a MEASURED co-occurrence in
// an AUTHORED pattern, the two kept adjacent and never fused. Each finding shows
// its match quality, the per-member evidence with the AUTHORED note (the only
// "why"), and what was unobservable. A single crossing is not here — only matched
// phenomena surface (doc 07 §3.5).
func renderFindings(w io.Writer, findings []detect.Finding, obs *binding.ObservabilityReport) {
	const bar = "================================================================================"
	// Binding-time observability per phenomenon (doc 04 M5): the pre-declared
	// "this phenomenon can only ever match degraded here" honesty, JOINED AT THE
	// SCREEN (doc 01: classes presented adjacently at the surfacing layer) —
	// the deterministic finding itself never carries availability state.
	declared := map[string]binding.PhenomenonCoverage{}
	if obs != nil {
		for _, pc := range obs.PerPhenomenon {
			declared[pc.PhenomenonID] = pc
		}
	}
	fmt.Fprintln(w, bar)
	if len(findings) == 0 {
		fmt.Fprintln(w, " detection (doc 07, entity-local + first-order) — no phenomena matched this tick")
		fmt.Fprintln(w, bar)
		return
	}
	fmt.Fprintf(w, " detection (doc 07, entity-local + first-order) — %d phenomenon match(es)\n", len(findings))
	for _, f := range findings {
		ent := f.Name
		if f.Namespace != "" {
			ent = f.Namespace + "/" + f.Name
		}
		span := f.Span
		if span == "" {
			span = "entity-local"
		}
		fmt.Fprintf(w, "\n  ▸ %s  [%s match · %s · completeness %.0f%%]\n", f.Label, f.Quality, span, f.Completeness*100)
		fmt.Fprintf(w, "    entity: %s (%s)\n", ent, f.Kind)
		fmt.Fprintf(w, "    required: %d met of %d (%d unobservable here); supporting: %d/%d\n",
			f.RequiredMet, f.RequiredTotal, f.RequiredUnobserved, f.SupportingMet, f.SupportingObservable)
		for _, mem := range f.Members {
			barProv := "config"
			if mem.BarFlagged {
				barProv = "default-flagged"
			}
			where := ""
			if mem.Neighbour != "" {
				// Cross-entity evidence: name the neighbour and the edge verdict it
				// crossed (doc 07 §3.8 span instantiation, on the evidence row).
				where = fmt.Sprintf(" ⤷ on %s via %s[%s]", ceiHuman(mem.Neighbour), mem.Via, mem.EdgeResult)
			}
			fmt.Fprintf(w, "      • %s [%s] %s=%s (bar:%s)%s — %s\n",
				mem.Role, mem.Temporal, shortMetric(mem.Metric), mem.State, barProv, where, authoredNote(mem.Note))
		}
		for _, u := range f.Unobservable {
			fmt.Fprintf(w, "      ! unobservable required member: %s\n", u)
		}
		for _, g := range f.SupportingGaps {
			fmt.Fprintf(w, "      ~ unobservable supporting member: %s\n", g)
		}
		if pc, ok := declared[f.Phenomenon]; ok && pc.Observability != "full" && f.Quality == detect.QualityDegraded {
			fmt.Fprintf(w, "    pre-declared (binding-time): this phenomenon can only ever match DEGRADED on this cluster (%d/%d required members obtainable)\n",
				pc.RequiredObtainable, pc.RequiredTotal)
		}
		for _, step := range f.SpanPath {
			fmt.Fprintf(w, "    span path: %s ─%s→ %s  [%s]\n", ceiHuman(step.From), step.Type, ceiHuman(step.To), step.Result)
		}
		for _, s := range f.SuspectEdges {
			fmt.Fprintf(w, "    ⚠ DEGRADED by suspect edge: %s\n", s)
		}
		// Blast radius (doc 07 §3.4): authored downstream relations made
		// concrete on the current topology — at-risk, never a prediction.
		for _, r := range f.BlastRadius {
			fmt.Fprintf(w, "    at risk (authored relation): %s → %s [%s/%s] — %s\n",
				ceiHuman(r.CEIKey), r.Phenomenon, r.Related, r.Temporal, authoredNote(r.Why))
		}
		fmt.Fprintf(w, "    provenance: MEASURED match · AUTHORED pattern %s · graph %s\n",
			f.Phenomenon, shortUID(strings.TrimPrefix(f.GraphVersion, "sha256:")))
	}
	fmt.Fprintln(w, bar)
}

// renderCascades writes the recognized cascades (doc 07 §3.4): a trigger
// phenomenon and its graph-declared downstream both lit on topologically
// related entities within the window — surfaced as ONE correlated story,
// strictly descriptive: the AUTHORED relationship is currently manifest,
// never a causal proof.
func renderCascades(w io.Writer, cs []detect.Cascade) {
	if len(cs) == 0 {
		return
	}
	fmt.Fprintf(w, " cascades (doc 07 §3.4) — %d authored relation(s) currently manifest:\n", len(cs))
	for _, c := range cs {
		fmt.Fprintf(w, "\n  ⛓ %s ⇒ %s  [%s]\n", c.Trigger.Phenomenon, c.Downstream.Phenomenon, c.Temporal)
		fmt.Fprintf(w, "    trigger:    %s at %s  [%s]\n",
			ceiHuman(c.Trigger.EntityCEI), c.Trigger.EvaluatedAt.Format("15:04:05"), c.Trigger.Quality)
		fmt.Fprintf(w, "    downstream: %s at %s  [%s]\n",
			ceiHuman(c.Downstream.EntityCEI), c.Downstream.EvaluatedAt.Format("15:04:05"), c.Downstream.Quality)
		fmt.Fprintf(w, "    related: %s · authored note: %s\n", c.Related, authoredNote(c.Why))
		fmt.Fprintln(w, "    provenance: two MEASURED co-occurrences joined by an AUTHORED relation — adjacent, never fused; not a causal proof")
	}
	fmt.Fprintln(w)
}

// renderUnexplained writes the unexplained-anomaly channel (doc 08): loud
// activity matching NO curated phenomenon, surfaced for human investigation with
// NO causal claim — the absence of a reason is the point, kept visible. The
// channel discloses its own residual blind spot (§3.2). MEASURED, never AUTHORED.
func renderUnexplained(w io.Writer, cards []unexplained.Finding, tr *unexplained.Tracker) {
	const bar = "================================================================================"
	if len(cards) == 0 {
		return
	}
	fmt.Fprintln(w, bar)
	fmt.Fprintf(w, " unexplained channel (doc 08) — %d loud-but-unmatched card(s) [%s]\n", len(cards), unexplained.Mark)
	for _, c := range cards {
		ent := c.Name
		if c.Namespace != "" {
			ent = c.Namespace + "/" + c.Name
		}
		fmt.Fprintf(w, "\n  ◇ %s (%s)  [%s · seen %d window(s)]\n", ent, c.Kind, c.Status, c.Occurrences)
		for _, s := range c.LoudStates {
			prov := "config"
			if s.Flagged {
				prov = "default-flagged"
			}
			fmt.Fprintf(w, "      • loud: %s = %s (%s, bar:%s)\n", shortMetric(s.Metric), s.State, s.Kind, prov)
		}
		fmt.Fprintf(w, "    match check: %s\n", c.MatchCheck)
		fmt.Fprintf(w, "    provenance: MEASURED · NOT a reason (this channel describes loudness, it never explains)\n")
	}
	// Candidate-phenomenon reports (doc 08 §3.6) — recurring unexplained patterns
	// proposed for human curation; the system proposes, it never authors.
	if tr != nil {
		if cands := tr.Candidates(8, 3); len(cands) > 0 {
			fmt.Fprintf(w, "\n  ⌬ curation feedback (doc 08 §3.6) — %d candidate-phenomenon report(s):\n", len(cands))
			for _, cr := range cands {
				fmt.Fprintf(w, "    · %s\n", cr.Rationale)
			}
		}
	}
	fmt.Fprintf(w, "  %s\n", unexplained.BlindSpotNotice)
	fmt.Fprintln(w, bar)
}

// ceiHuman compacts a CEI key to ns/name (kind) for log lines.
func ceiHuman(key string) string {
	parts := strings.Split(key, "|")
	if len(parts) == 6 && parts[0] == "i" {
		if parts[2] != "" {
			return parts[2] + "/" + parts[4] + " (" + parts[3] + ")"
		}
		return parts[4] + " (" + parts[3] + ")"
	}
	return key
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
