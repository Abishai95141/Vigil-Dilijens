package api

import (
	"sort"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/store"
)

// TimelineView is the anomaly timeline payload (doc 10 §3.3, M4): findings of
// all three surfaces laid on time, so "what happened, what is happening, what
// is projected" reads as one without class confusion. Matches render as
// intervals (MEASURED), unexplained cards as aging spans (MEASURED, no reason),
// projections as forward-pointing bands (PROJECTED) — the projection lane is
// RESERVED and stated empty until the forecasting layer ships (09 / M5,
// Phase 2), never implied present. The TS type mirrors this shape.
type TimelineView struct {
	GeneratedAt   time.Time      `json:"generatedAt"`
	Window        TimelineWindow `json:"window"`
	Matches       []TimelineSpan `json:"matches"`       // MEASURED phenomenon matches (intervals)
	Unexplained   []TimelineSpan `json:"unexplained"`   // MEASURED loud-but-unmatched (aging spans)
	Projected     []TimelineSpan `json:"projected"`     // PROJECTED — empty until Phase 2 (stated)
	ProjectedNote string         `json:"projectedNote"` // why the projected lane is empty
}

// TimelineWindow is the [from, to] the spans fall within (the union of all span
// extents, or just `to`=now when empty).
type TimelineWindow struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// TimelineSpan is one finding's extent on the time axis. Class is the provenance
// class the surface renders it in (MEASURED / PROJECTED) — never mixed across
// lanes. Status carries the lifecycle (quality for matches; aging state for
// unexplained).
type TimelineSpan struct {
	Class     string    `json:"class"`   // MEASURED | PROJECTED
	Surface   string    `json:"surface"` // insight | unexplained | early-warning
	Label     string    `json:"label"`
	EntityCEI string    `json:"entityCei"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	Status    string    `json:"status"` // full | degraded | new | aging | superseded-by-match | resolved
	From      time.Time `json:"from"`
	To        time.Time `json:"to"`
}

// The PROJECTED lane note is CONDITIONAL on the lane's actual state (mirrors the
// EarlyWarnings surface): OFF (the gate rule) vs ON-but-quiet vs ON-with-bands —
// never a stale "ships in Phase 2" claim while the lane is live.
const projectedLaneOffNote = "Early warnings are OFF: no forecast class is operator-visible before its backtest calibration gate passes (doc 11 §3.5 / 09 M3). The lane is reserved — never a fabricated future."
const projectedLaneQuietNote = "Forecasting is ON; no projection crosses a configured bar within the horizon right now — silence is the default output (doc 09 §3.6), not an empty lane."
const projectedLaneBandsNote = "Forward-pointing PROJECTED bands: each span is the [earliest, latest] crossing window of an early warning — a band, never a point, never a certainty."

// BuildTimeline composes the timeline from the persisted findings + unexplained
// rows, plus the current early-warning projections (doc 10 §3.3: projections
// render as FORWARD-POINTING ranges, band-shaped, never a point — the span IS
// the [earliest, latest] crossing band). Pure given its inputs; the API handler
// reads the store + the warnings snapshot and calls it. `enabled` is the
// forecast lane's state, so the empty lane reads as ON-but-quiet (honest
// silence) or OFF (the gate rule) — never a stale Phase-2 placeholder.
func BuildTimeline(now time.Time, findings []store.FindingRow, unexp []store.UnexplainedRow, warnings []WarningCard, enabled bool) *TimelineView {
	v := &TimelineView{
		GeneratedAt: now.UTC(),
		Matches:     []TimelineSpan{},
		Unexplained: []TimelineSpan{},
		Projected:   []TimelineSpan{},
	}
	for _, w := range warnings {
		to := w.LatestAt
		if w.LatestBeyondHorizon {
			// The far edge is open: the span extends to the horizon's end and
			// the status says so — never a fabricated closing time.
			to = w.BasisAt.Add(time.Duration(float64(w.HorizonSteps) * w.CadenceSeconds * float64(time.Second)))
		}
		v.Projected = append(v.Projected, TimelineSpan{
			Class: "PROJECTED", Surface: "early-warning",
			Label:     "projected to cross — " + w.Metric,
			EntityCEI: w.EntityCEI, Name: w.Name, Kind: w.Kind,
			Status: w.Confidence, From: w.EarliestAt.UTC(), To: to.UTC(),
		})
	}
	switch {
	case !enabled:
		v.ProjectedNote = projectedLaneOffNote
	case len(v.Projected) > 0:
		v.ProjectedNote = projectedLaneBandsNote
	default:
		v.ProjectedNote = projectedLaneQuietNote
	}
	from := now

	for _, f := range findings {
		span := TimelineSpan{
			Class: "MEASURED", Surface: "insight", Label: f.Label,
			EntityCEI: f.EntityCEI, Name: f.Name, Kind: f.Kind,
			Status: f.Quality, From: f.FirstSeen.UTC(), To: f.LastSeen.UTC(),
		}
		v.Matches = append(v.Matches, span)
		if span.From.Before(from) {
			from = span.From
		}
	}
	for _, u := range unexp {
		span := TimelineSpan{
			Class: "MEASURED", Surface: "unexplained", Label: "anomalous — investigate",
			EntityCEI: u.Scope, Name: u.Name, Kind: u.Kind,
			Status: u.Status, From: u.FirstSeen.UTC(), To: u.LastSeen.UTC(),
		}
		v.Unexplained = append(v.Unexplained, span)
		if span.From.Before(from) {
			from = span.From
		}
	}

	sort.SliceStable(v.Matches, func(i, j int) bool { return v.Matches[i].From.Before(v.Matches[j].From) })
	sort.SliceStable(v.Unexplained, func(i, j int) bool { return v.Unexplained[i].From.Before(v.Unexplained[j].From) })
	v.Window = TimelineWindow{From: from.UTC(), To: now.UTC()}
	return v
}
