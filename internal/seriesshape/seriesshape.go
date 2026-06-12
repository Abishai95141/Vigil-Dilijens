// Package seriesshape normalizes the KG's free-text data_type spellings into a
// canonical series classification (doc 02 §3.6 authoring queue; doc 09 §3.2
// forecast funnel). It sits at the module root so BOTH consumers share one
// implementation: the runtime loader (obsd/internal/graph — forecast
// eligibility) and the ontology linter (tools/graphlint — the curation-queue
// gap report). One parser, one truth; drift between runtime and lint would be
// a silent false-coverage bug.
//
// The 589 signals carry ~112 distinct data_type spellings ("Gauge", "Gauges",
// "Counters + gauges", "Info gauge", "Event v1", "Struct", "OTLP spans", ...).
// The forecast funnel needs to know which signals are numeric series at all,
// and which contain a gauge (a level you project directly) vs counters
// (forecastable only via rate derivation, flagged) vs histograms (the
// quantile-derivation open question, doc 09 §8).
//
// This is a deterministic DERIVATION from the authored text, not new
// knowledge: the spelling stays in the graph verbatim, the parsed shape is
// computed wherever needed. Anything the parser cannot confidently classify
// lands Unknown and is surfaced in the graphlint gap report as the remaining
// curation queue — never silently guessed.
package seriesshape

import "strings"

// Shape is the canonical classification of one signal's data_type.
type Shape struct {
	Gauge     bool // contains a gauge component (cleanest forecast fit, doc 09 §3.2)
	Counter   bool // contains a counter component (rate-derived targets, flagged)
	Histogram bool // contains histogram/summary series (quantile derivation, doc 09 §8)
	NonSeries bool // structured/textual/event payloads — not a numeric series
	Unknown   bool // unclassifiable spelling — stays in the curation queue, stated
}

// Series reports whether the signal carries any numeric time series component.
func (s Shape) Series() bool { return s.Gauge || s.Counter || s.Histogram }

// nonSeriesMarkers identify data_type spellings that are payloads, not series:
// events, structured objects, logs, traces, profiles, files. Matched as
// substrings of the lowercased text AFTER series tokens are checked — a mixed
// declaration like "Gauges (state-derived)" stays a series.
var nonSeriesMarkers = []string{
	"event", "struct", "json", "yaml", "text", "log", "span", "trace",
	"profile", "file", "object", "manifest", "resource", "api", "snapshot",
	"dump", "report", "list", "table", "record", "kv", "key-value",
}

// Parse classifies one free-text data_type spelling. Deterministic,
// case-insensitive, and conservative: series tokens ("gauge", "counter",
// "histogram", "summary") classify positively wherever they appear; known
// payload markers classify NonSeries; anything else is Unknown — surfaced for
// curation, never guessed.
func Parse(dataType string) Shape {
	dt := strings.ToLower(strings.TrimSpace(dataType))
	if dt == "" {
		return Shape{Unknown: true}
	}
	var s Shape
	if strings.Contains(dt, "gauge") {
		s.Gauge = true
	}
	if strings.Contains(dt, "counter") {
		s.Counter = true
	}
	if strings.Contains(dt, "histogram") || strings.Contains(dt, "summary") {
		s.Histogram = true
	}
	if s.Series() {
		return s
	}
	for _, m := range nonSeriesMarkers {
		if strings.Contains(dt, m) {
			s.NonSeries = true
			return s
		}
	}
	s.Unknown = true
	return s
}
