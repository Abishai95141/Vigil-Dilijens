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

const projectedLaneNote = "Projected (PROJECTED-class) early warnings render here once the forecasting layer ships (doc 09 / 10 M5, Phase 2). The lane is reserved and intentionally empty — never a fabricated future."

// BuildTimeline composes the timeline from the persisted findings + unexplained
// rows. Pure given its inputs; the API handler reads the store and calls it.
func BuildTimeline(now time.Time, findings []store.FindingRow, unexp []store.UnexplainedRow) *TimelineView {
	v := &TimelineView{
		GeneratedAt:   now.UTC(),
		Matches:       []TimelineSpan{},
		Unexplained:   []TimelineSpan{},
		Projected:     []TimelineSpan{},
		ProjectedNote: projectedLaneNote,
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
