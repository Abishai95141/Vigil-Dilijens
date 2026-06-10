package main

import (
	"fmt"
	"sort"
	"strings"
)

// GapReport is the authoring-gap analysis (doc 14 A14): what a human must still
// author before the graph is detection-ready, plus informational stats. This is the
// bridge to governance (doc 12) — humans author; the tool only reports.
type GapReport struct {
	PhenomenaTotal       int
	PhenomenaWithSpan    int
	PhenomenaMissingSpan int
	MissingSpanIDs       []string

	SignalsTotal  int
	MetricSignals int

	// ThresholdHintEdges counts owned_by_agent edges carrying a free-text threshold
	// hint — the closest the KG has to borrowed-normativity rules, which still need
	// authoring into structured config-relative threshold rules (doc 04 §3.4).
	ThresholdHintEdges int

	TemporalVocabulary []string // distinct temporal tags actually used (informational)
	DataTypeVariants   int      // distinct free-text data_type values (forecast-funnel normalization, doc 09)
	EdgesTotal         int
}

const maxMissingSpanList = 50

func analyzeGap(doc kgDoc) GapReport {
	g := GapReport{EdgesTotal: len(doc.Edges)}
	dtypes := map[string]struct{}{}
	for _, n := range doc.Nodes {
		switch n.Type {
		case "CorrelationGroup":
			g.PhenomenaTotal++
			if strings.TrimSpace(n.Span) != "" {
				g.PhenomenaWithSpan++
			} else {
				g.PhenomenaMissingSpan++
				if len(g.MissingSpanIDs) < maxMissingSpanList {
					g.MissingSpanIDs = append(g.MissingSpanIDs, n.ID)
				}
			}
		case "Signal":
			g.SignalsTotal++
			if n.Modality == "Metric" {
				g.MetricSignals++
			}
			if n.DataType != "" {
				dtypes[n.DataType] = struct{}{}
			}
		}
	}
	vocab := map[string]struct{}{}
	for _, e := range doc.Edges {
		if e.TemporalOrder != "" {
			vocab[e.TemporalOrder] = struct{}{}
		}
		if e.Type == "owned_by_agent" && strings.TrimSpace(e.Threshold) != "" {
			g.ThresholdHintEdges++
		}
	}
	g.DataTypeVariants = len(dtypes)
	g.TemporalVocabulary = sortedSet(vocab)
	return g
}

func sortedSet(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// hasGaps reports whether any authoring gap remains (drives -strict failure).
func (g GapReport) hasGaps() bool { return g.PhenomenaMissingSpan > 0 }

// String renders the human-facing gap report.
func (g GapReport) String() string {
	var b strings.Builder
	fmt.Fprintln(&b, "  authoring gap (doc 14 A14):")
	fmt.Fprintf(&b, "    phenomena: %d total, %d with span, %d MISSING span (must be authored before detection ships)\n",
		g.PhenomenaTotal, g.PhenomenaWithSpan, g.PhenomenaMissingSpan)
	if g.PhenomenaMissingSpan > 0 {
		ids := g.MissingSpanIDs
		more := ""
		if g.PhenomenaMissingSpan > len(ids) {
			more = fmt.Sprintf(" (+%d more)", g.PhenomenaMissingSpan-len(ids))
		}
		fmt.Fprintf(&b, "      span-less: %s%s\n", strings.Join(ids, ", "), more)
	}
	fmt.Fprintf(&b, "    signals: %d total, %d metric; threshold rules: 0 structured (config-relative), %d agent hints to author\n",
		g.SignalsTotal, g.MetricSignals, g.ThresholdHintEdges)
	fmt.Fprintf(&b, "    data_type variants: %d (needs normalization for the forecast funnel, doc 09)\n", g.DataTypeVariants)
	fmt.Fprintf(&b, "    temporal vocabulary in use (%d): %s\n", len(g.TemporalVocabulary), strings.Join(g.TemporalVocabulary, " "))
	return b.String()
}
