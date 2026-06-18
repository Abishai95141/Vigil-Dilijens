package api

import "time"

// TraceGraphClass labels the /api/trace-graph payload: the MEASURED observed service call
// graph (doc 20 P4 TRACE lane). An edge is OBSERVED STRUCTURE — service A invoked service
// B, with observed call counts/errors/latency — never a cause. Latency is surfaced as
// MEASURED; "slow" is decided only against a DECLARED SLO, never a distribution-derived
// cutoff. "This call should be an authored topology edge" is a separate PROPOSED candidate
// (surfaced at /api/candidates), never a fact here.
const TraceGraphClass = "MEASURED observed call graph (who called whom + observed latency; not a cause)"

const traceGraphNote = "Observed service call graph (doc 20 P4 TRACE): parent→child spans aggregated into " +
	"service-to-service call edges with observed call counts, errors, and latency percentiles. MEASURED structure — " +
	"the arrow is who-called-whom, never a cause. Spans are SAMPLED, so the graph is census-incomplete (orphanSpans " +
	"counts child spans whose parent was not sampled). Each observed edge is also staged as a STRUCTURAL topology " +
	"candidate (relation \"topology\", never causal) at /api/candidates for human promotion. Off the deterministic " +
	"digest; never feeds detection or replay. Latency is surfaced MEASURED; a bar comes only from a declared SLO."

const traceGraphOffNote = "The trace lane is not enabled (--traces-enabled with a --traces-path). " +
	"Detection and forecasting are unaffected."

const traceGraphWarmingNote = "The trace lane is enabled and awaiting its first read cycle. Detection and forecasting are unaffected."

// TraceEdgeRow is one observed call edge, surfaced read-only.
type TraceEdgeRow struct {
	Caller    string  `json:"caller"`
	Callee    string  `json:"callee"`
	Calls     int     `json:"calls"`
	Errors    int     `json:"errors"`
	P50Millis float64 `json:"p50Millis"`
	P95Millis float64 `json:"p95Millis"`
	MaxMillis float64 `json:"maxMillis"`
}

// TraceGraphView is the /api/trace-graph payload.
type TraceGraphView struct {
	Class            string         `json:"class"`
	Available        bool           `json:"available"`
	GeneratedAt      time.Time      `json:"generatedAt"`
	CensusComplete   bool           `json:"censusComplete"` // always false: spans are sampled (honest partial coverage)
	SpansObserved    int            `json:"spansObserved"`
	Traces           int            `json:"traces"`
	OrphanSpans      int            `json:"orphanSpans"`
	SourceTruncated  bool           `json:"sourceTruncated"`  // the spans file exceeded the read cap, so older spans were dropped (honest partial coverage)
	CandidatesStaged int            `json:"candidatesStaged"` // topology candidates staged this cycle (surfaced at /api/candidates)
	Edges            []TraceEdgeRow `json:"edges"`
	Note             string         `json:"note"`
}

// NewTraceGraphView builds the available view from already-mapped rows + census accounting.
// sourceTruncated states whether the spans file exceeded the read cap (surfaced, never hidden).
func NewTraceGraphView(now time.Time, spansObserved, traces, orphanSpans, candidatesStaged int, sourceTruncated bool, rows []TraceEdgeRow) *TraceGraphView {
	if rows == nil {
		rows = []TraceEdgeRow{}
	}
	note := traceGraphNote
	if sourceTruncated {
		note += " NOTE: the spans file exceeded the read cap; older spans were dropped this cycle (extra census incompleteness)."
	}
	return &TraceGraphView{
		Class: TraceGraphClass, Available: true, GeneratedAt: now,
		CensusComplete: false, SpansObserved: spansObserved, Traces: traces, OrphanSpans: orphanSpans,
		SourceTruncated: sourceTruncated, CandidatesStaged: candidatesStaged, Edges: rows, Note: note,
	}
}

// UnavailableTraceGraph is the honest OFF state.
func UnavailableTraceGraph(now time.Time) *TraceGraphView {
	return &TraceGraphView{
		Class: TraceGraphClass, Available: false, GeneratedAt: now,
		Edges: []TraceEdgeRow{}, Note: traceGraphOffNote,
	}
}

// WarmingTraceGraph is the honest state for an ENABLED lane that has not completed its
// first read cycle — distinct from the OFF state (which would falsely say "not enabled").
func WarmingTraceGraph(now time.Time) *TraceGraphView {
	return &TraceGraphView{
		Class: TraceGraphClass, Available: false, GeneratedAt: now,
		Edges: []TraceEdgeRow{}, Note: traceGraphWarmingNote,
	}
}
