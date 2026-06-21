package main

import (
	"fmt"
	"sort"
	"strings"
)

// Membership-structuring analysis — the inline-vs-structured member gate (doc 02 §3.6,
// doc 07 §3.1). A phenomenon expresses its required members in TWO unreconciled forms:
//
//   - INLINE: the phenomenon node's `signals` tuples [pattern, role, temporal, note] —
//     DOCUMENTARY curator intent; the detection matcher (obsd/internal/detect/match.go)
//     never reads them.
//   - STRUCTURED: participates_in edges (Signal -> phenomenon) — what the matcher actually
//     reads for RequiredTotal/RequiredMet/Completeness/Quality.
//
// When a phenomenon authors required INLINE members but has ZERO structured required
// members, the matcher computes RequiredTotal==0 and the phenomenon is structurally
// undetectable by the metric matcher — a silent capability hole. This analyzer makes that
// gap an ENFORCED gate (hard fail) instead of a silent backlog, with an authored escape
// hatch (detection_status) for phenomena whose detection legitimately lives off the metric
// matcher (events lane, or pending a scrape lane / entity kind that does not exist yet).
//
// This is a SHACL-style cardinality shape over authored content (a required-member must
// resolve to >=1 structured member) and a requirements-traceability forward-coverage scan
// (an authored requirement with no implemented detector is an orphan). It is pure
// arithmetic over AUTHORED counts — MEASURED-class, no model output, no runtime effect.

// StructuringDeficit is one phenomenon's inline-vs-structured required-member accounting.
type StructuringDeficit struct {
	ID                 string
	InlineRequired     int
	StructuredRequired int
	Acknowledged       bool   // a detection_status escape hatch is declared
	Lane               string // the declared lane (when Acknowledged)
}

// StructuringReport is the per-graph membership-structuring view: the under-structured
// backlog (informational), the dangerous undetectable+unacknowledged tail (the hard
// fails), and the acknowledged off-matcher phenomena.
type StructuringReport struct {
	PhenomenaTotal int
	Deficits       []StructuringDeficit // inlineRequired > structuredRequired — the informational backlog
	HardFails      []StructuringDeficit // inlineRequired>=1, structuredRequired==0, NOT acknowledged — the gate
	Acknowledged   []StructuringDeficit // inlineRequired>=1, structuredRequired==0, escape-hatched
}

// analyzeStructuringGap computes the membership-structuring report over the MERGED view.
// CRITICAL: structured required members may arrive via overlay `members:` blocks that
// mergeOverlays does NOT project into doc.Edges — so the merged structured-required set
// must UNION base participates_in edges with every overlay's `members:` entries (empty
// role defaults to "required", matching obsd/internal/graph applyOverlay). Reading only
// doc.Edges would spuriously hard-fail a phenomenon whose required members are
// overlay-supplied (e.g. PHEN_INIT_CONTAINER_FAILURE).
func analyzeStructuringGap(doc kgDoc, ovls []overlayDoc) StructuringReport {
	phenSet := map[string]bool{}
	inlineReq := map[string]int{}

	// Inline required members: base phenomenon nodes carry their `signals` tuples;
	// overlay-ADDED phenomena do not (mergeOverlays projects them as bare nodes), so
	// read their inline tuples from the overlay docs.
	for _, n := range doc.Nodes {
		if n.Type != "CorrelationGroup" {
			continue
		}
		phenSet[n.ID] = true
		for _, tup := range n.Signals {
			if len(tup) >= 2 && tup[1] == "required" {
				inlineReq[n.ID]++
			}
		}
	}
	for _, o := range ovls {
		for _, op := range o.Phenomena {
			phenSet[op.ID] = true
			for _, tup := range op.Signals {
				if len(tup) >= 2 && tup[1] == "required" {
					inlineReq[op.ID]++
				}
			}
		}
	}

	// Structured required members, deduped by signal id (so a >=1 verdict is robust even
	// if a base edge and an overlay member name the same signal): base participates_in
	// edges with role "required" UNION overlay `members:` entries with effective-required
	// role (empty => "required").
	structReq := map[string]map[string]bool{}
	addStruct := func(phen, sig string) {
		if structReq[phen] == nil {
			structReq[phen] = map[string]bool{}
		}
		structReq[phen][sig] = true
	}
	for _, e := range doc.Edges {
		if e.Type == "participates_in" && e.Role == "required" {
			addStruct(e.Dst, e.Src)
		}
	}
	for _, o := range ovls {
		for phen, ms := range o.Members {
			for _, m := range ms {
				role := m.Role
				if role == "" {
					role = "required"
				}
				if role == "required" {
					addStruct(phen, m.Signal)
				}
			}
		}
	}

	// Escape-hatch declarations (union across overlays; a later identical re-declaration
	// is harmless — the runtime loader rejects a CONFLICTING one).
	hatch := map[string]ovDetectionStatus{}
	for _, o := range ovls {
		for id, ds := range o.DetectionStatuses {
			hatch[id] = ds
		}
	}

	rep := StructuringReport{PhenomenaTotal: len(phenSet)}
	ids := make([]string, 0, len(phenSet))
	for id := range phenSet {
		ids = append(ids, id)
	}
	sort.Strings(ids) // determinism: every emitted slice is phenomenon-id ordered
	for _, id := range ids {
		ir := inlineReq[id]
		sr := len(structReq[id])
		ds, acked := hatch[id]
		d := StructuringDeficit{ID: id, InlineRequired: ir, StructuredRequired: sr, Acknowledged: acked}
		if acked {
			d.Lane = ds.Lane
		}
		if ir > sr {
			rep.Deficits = append(rep.Deficits, d)
		}
		if ir >= 1 && sr == 0 {
			if acked {
				rep.Acknowledged = append(rep.Acknowledged, d)
			} else {
				rep.HardFails = append(rep.HardFails, d)
			}
		}
	}
	return rep
}

// hardErrors renders the gate's hard failures: a phenomenon the curator declared
// metric-detectable (>=1 required inline member) that the matcher is structurally blind to
// (zero structured required members) and that carries no acknowledgment.
func (r StructuringReport) hardErrors() []string {
	out := make([]string, 0, len(r.HardFails))
	for _, d := range r.HardFails {
		out = append(out, fmt.Sprintf(
			"phenomenon %s: %d required inline member(s) but ZERO structured (participates_in) required members — "+
				"undetectable by the metric matcher; wire a required member or declare a detection_status escape hatch",
			d.ID, d.InlineRequired))
	}
	return out
}

// String renders the soft (always-printed) membership-structuring report. The
// "reads only X of Y authored required members" line is the load-bearing honesty: it
// makes concrete that a sparsely-structured phenomenon reporting Quality "full" overstates
// confidence relative to the curator's authored intent.
func (r StructuringReport) String() string {
	var b strings.Builder
	b.WriteString("    membership structuring (inline-required vs structured-required members):\n")
	if len(r.Deficits) == 0 {
		b.WriteString("      no under-structured phenomena — detection reads every authored required member\n")
	} else {
		fmt.Fprintf(&b, "      %d phenomena under-structured (detection reads fewer required members than authored):\n", len(r.Deficits))
		for _, d := range r.Deficits {
			tag := ""
			if d.Acknowledged {
				tag = " [acknowledged off-matcher: " + d.Lane + "]"
			}
			fmt.Fprintf(&b, "        %s: detection reads only %d of %d authored required members (deficit %d)%s\n",
				d.ID, d.StructuredRequired, d.InlineRequired, d.InlineRequired-d.StructuredRequired, tag)
		}
	}
	if len(r.HardFails) > 0 {
		fmt.Fprintf(&b, "      %d HARD FAIL (>=1 inline-required, ZERO structured-required, undetectable + unacknowledged): %s\n",
			len(r.HardFails), strings.Join(deficitIDs(r.HardFails), ", "))
	}
	if len(r.Acknowledged) > 0 {
		fmt.Fprintf(&b, "      %d acknowledged off the metric matcher (detection_status escape hatch): %s\n",
			len(r.Acknowledged), strings.Join(deficitIDs(r.Acknowledged), ", "))
	}
	return b.String()
}

func deficitIDs(ds []StructuringDeficit) []string {
	out := make([]string, 0, len(ds))
	for _, d := range ds {
		out = append(out, d.ID)
	}
	return out
}
