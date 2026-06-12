package forecast

import (
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/clock"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/params"
)

// ClassProjected is the provenance class every candidate carries (doc 01).
const ClassProjected = "PROJECTED"

// Candidate is one early-warning candidate (doc 09 §3.9): the target, the
// projection against its bar with the MANDATORY band, a qualitative confidence
// class, and AUTHORED references — never a generated causal sentence. The
// surfacing register is fixed: "projected to cross", never "will cross".
type Candidate struct {
	Class        string `json:"class"`        // always PROJECTED
	IsProjection bool   `json:"isProjection"` // the mandatory mark (doc 09 §3.6)

	// Target reference (graph/binding-derived, AUTHORED provenance carried).
	EntityCEI  string  `json:"entityCei"`
	Entity     string  `json:"entity"`
	Metric     string  `json:"metric"`
	SeriesKind string  `json:"seriesKind"`
	BarValue   float64 `json:"barValue"`
	BarUnit    string  `json:"barUnit"`
	BarSource  string  `json:"barSource"`
	BarFlagged bool    `json:"barFlagged"` // default-sourced bar, surfaced on every card
	Direction  string  `json:"direction"`

	// AUTHORED references (ids into the graph — the surface cites them; the
	// candidate never paraphrases them into text).
	PrecursorPhenomena []string `json:"precursorPhenomena"`
	GraphVersion       string   `json:"graphVersion"`

	// The projection. Times derive from the LAST OBSERVED SAMPLE's stamp plus
	// forecast steps × cadence. LatestBeyondHorizon=true means the lower edge
	// of the band never crossed inside the horizon: the crossing MAY not
	// happen — stated, never clamped into false precision.
	GeneratedAt         time.Time     `json:"generatedAt"`
	BasisAt             time.Time     `json:"basisAt"` // last observed sample stamp
	CrossAt             time.Time     `json:"crossAt"`
	EarliestAt          time.Time     `json:"earliestAt"`
	LatestAt            time.Time     `json:"latestAt"` // zero when LatestBeyondHorizon
	LatestBeyondHorizon bool          `json:"latestBeyondHorizon"`
	TimeToCross         time.Duration `json:"timeToCross"`
	Confidence          string        `json:"confidence"` // tight | moderate | wide

	// Decomposition record (doc 09 §3.9) — v1: context length only; footprint
	// fields arrive with Phase 3.
	ContextPoints int           `json:"contextPoints"`
	HorizonSteps  int           `json:"horizonSteps"`
	Cadence       time.Duration `json:"cadence"`
	Quantiles     []float64     `json:"quantiles"`
}

// Silence is one target that produced NO candidate this cycle, with the
// guardrail that silenced it (doc 09 §3.6 — silence is the default output,
// and it is auditable, never just an absence).
type Silence struct {
	EntityCEI string `json:"entityCei"`
	Metric    string `json:"metric"`
	Reason    string `json:"reason"`
}

// Silence reasons (enumerated; §3.6's hard silences plus the pipeline's
// honest per-cycle skips).
const (
	SilenceNoStream        = "no-stream"
	SilenceAmbiguousStream = "ambiguous-stream"
	SilenceCounterStream   = "counter-stream-deferred" // the live stream exposes a counter; its level is not projectable
	SilenceShortContext    = "short-context"
	SilenceAlreadyCrossed  = "already-crossed" // the bar is crossed NOW — detection's jurisdiction, not a forecast
	SilenceFlat            = "flat-series"
	SilenceNoCrossing      = "no-crossing-within-horizon"
	SilenceBandTooWide     = "band-too-wide"
	SilenceClockDegraded   = "clock-degraded"
)

// Project judges one clock answer against the target's bar (doc 09 §3.3 step
// 6 + §3.6 guardrails). Pure: same forecast + same bar + same params ⇒ same
// verdict. Returns either a candidate or the silence reason.
//
// Band semantics: for an above-bar the UPPER quantile trajectory crosses
// soonest (earliest) and the LOWER latest; mirrored for below-bars. The point
// trajectory must cross within the horizon or the target stays silent.
func Project(t Target, fc *clock.Forecast, basisAt, generatedAt time.Time,
	cadence time.Duration, contextPoints int, graphVersion string, p params.ForecastParams) (*Candidate, string) {

	horizon := len(fc.Point)
	pointIdx := crossIndex(fc.Point, t.BarValue, t.Direction)
	if pointIdx < 0 {
		return nil, SilenceNoCrossing
	}
	lower, upper := fc.Quantiles[0], fc.Quantiles[len(fc.Quantiles)-1]
	optimist, pessimist := upper, lower // above-bar: upper crosses soonest
	if t.Direction == "below" {
		optimist, pessimist = lower, upper
	}
	earliestIdx := crossIndex(optimist, t.BarValue, t.Direction)
	if earliestIdx < 0 || earliestIdx > pointIdx {
		earliestIdx = pointIdx // the band edge never leads past the point estimate
	}
	latestIdx := crossIndex(pessimist, t.BarValue, t.Direction)
	beyond := latestIdx < 0

	// Band-width guardrail (§3.6): a band wider than MaxBandRatio × the
	// time-to-cross is not a useful warning. A beyond-horizon lower edge uses
	// the full horizon as its effective width — and is capped to "wide" below.
	latestEff := latestIdx
	if beyond {
		latestEff = horizon - 1
	}
	ttcSteps := pointIdx + 1
	widthSteps := latestEff - earliestIdx
	if p.MaxBandRatio > 0 && float64(widthSteps) > p.MaxBandRatio*float64(ttcSteps) {
		return nil, SilenceBandTooWide
	}
	confidence := "wide"
	if !beyond {
		switch ratio := float64(widthSteps) / float64(ttcSteps); {
		case ratio <= 0.5:
			confidence = "tight"
		case ratio <= 1.0:
			confidence = "moderate"
		}
	}

	c := &Candidate{
		Class: ClassProjected, IsProjection: true,
		EntityCEI: t.CEIKey, Entity: t.Entity, Metric: t.Metric, SeriesKind: t.SeriesKind,
		BarValue: t.BarValue, BarUnit: t.BarUnit, BarSource: t.BarSource,
		BarFlagged: t.BarFlagged, Direction: t.Direction,
		PrecursorPhenomena:  append([]string{}, t.PrecursorPhenomena...),
		GraphVersion:        graphVersion,
		GeneratedAt:         generatedAt.UTC(),
		BasisAt:             basisAt.UTC(),
		CrossAt:             stepTime(basisAt, cadence, pointIdx),
		EarliestAt:          stepTime(basisAt, cadence, earliestIdx),
		LatestBeyondHorizon: beyond,
		TimeToCross:         time.Duration(ttcSteps) * cadence,
		Confidence:          confidence,
		ContextPoints:       contextPoints,
		HorizonSteps:        horizon,
		Cadence:             cadence,
		Quantiles:           append([]float64{}, p.Quantiles...),
	}
	if !beyond {
		c.LatestAt = stepTime(basisAt, cadence, latestIdx)
	}
	return c, ""
}

// crossIndex returns the first forecast step at which the trajectory is at or
// past the bar in the violation direction, or -1.
func crossIndex(traj []float64, bar float64, direction string) int {
	for h, v := range traj {
		if direction == "below" {
			if v <= bar {
				return h
			}
		} else if v >= bar {
			return h
		}
	}
	return -1
}

func stepTime(basis time.Time, cadence time.Duration, idx int) time.Time {
	return basis.Add(time.Duration(idx+1) * cadence).UTC()
}
