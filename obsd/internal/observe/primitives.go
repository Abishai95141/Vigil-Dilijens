package observe

import (
	"math"
	"sort"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/qss"
)

// The three primitives (doc 05 §3.2) — the system's ENTIRE statistical vocabulary,
// closed by commitment. Everything here is arithmetic: subtraction (threshold),
// differencing (rate), conjunction (co-occurrence). No baseline, no seasonal model,
// no distribution fit, no anomaly score exists on this path, ever.
//
// Determinism (doc 05 §3.5): same readings + same bars + same windows => same
// outputs, always. These functions are pure: no clock, no map iteration, no state.

// ---- Primitive 1: Threshold (subtraction) ----------------------------------

// ThresholdState is the 4-rung ladder (doc 05 §3.4). It is a SEVERITY ladder
// oriented to the bar's direction-of-violation, NOT a raw numeric position:
//   - Below      — on the healthy side of the bar (not crossed)
//   - AtThreshold — approaching the bar from the healthy side (within the band)
//   - Above      — has crossed the config-sourced bar into the violating region
//   - WellAbove  — deep into violation, past the well-above line (A6)
//
// So for an `above` bar (e.g. working set vs limit) "Above" means numerically
// greater; for a `below` bar (e.g. node MemAvailable vs allocatable floor) "Above"
// means numerically LESS. The severity meaning is identical; the renderer shows the
// direction so the rung is never ambiguous.
type ThresholdState uint8

const (
	StateUnknown     ThresholdState = iota // no usable sample
	StateBelow                             // healthy side
	StateAtThreshold                       // approaching (within the band)
	StateAbove                             // crossed the bar
	StateWellAbove                         // deep into violation (A6 line)
)

func (s ThresholdState) String() string {
	switch s {
	case StateBelow:
		return "below"
	case StateAtThreshold:
		return "at-threshold"
	case StateAbove:
		return "above"
	case StateWellAbove:
		return "well-above"
	default:
		return "unknown"
	}
}

// Crossed reports whether the state is at or past the bar (the violating rungs).
func (s ThresholdState) Crossed() bool { return s == StateAbove || s == StateWellAbove }

// WellAboveLine computes the A6 deep-violation line from the bar.
//
//	above bar, factor in (0,1): the bar is factor×limit, so the well-above line is
//	  the RAW LIMIT itself (barValue / factor) — crossing the real configured cap.
//	above bar, otherwise:       wellAboveFactor × barValue (default 1.10×).
//	below bar:                  the raw-limit notion doesn't apply (the config value
//	  sits on the healthy side); use the multiplicative fallback INVERTED — a value
//	  a further wellAboveFactor deeper below the bar (barValue / wellAboveFactor).
func WellAboveLine(barValue, factor, wellAboveFactor float64, direction string) float64 {
	if direction == "below" {
		if wellAboveFactor <= 0 {
			return barValue
		}
		return barValue / wellAboveFactor
	}
	if factor > 0 && factor < 1 {
		return barValue / factor // the raw limit
	}
	return barValue * wellAboveFactor
}

// EvalThreshold places a value on the ladder against a bar (doc 05 §3.4). band is
// the approach-zone fraction (params.observation.at_threshold_band): a value within
// band×bar of the bar on the HEALTHY side is "at-threshold". direction is the bar's
// violation direction ("above" | "below"). wellAbove is the precomputed A6 line.
func EvalThreshold(value, barValue, wellAbove, band float64, direction string) ThresholdState {
	// A non-finite value (a divide-by-tiny, a 0/0) must NOT land on a fabricated
	// rung: ordered comparisons against NaN are all false, which would silently
	// return Below. Surface it as Unknown instead.
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return StateUnknown
	}
	if direction == "below" {
		// Violation is value < bar. Healthy is value > bar.
		switch {
		case value <= wellAbove:
			return StateWellAbove
		case value < barValue:
			return StateAbove
		case value <= barValue*(1+band):
			return StateAtThreshold
		default:
			return StateBelow
		}
	}
	// Default: above bar. Violation is value > bar.
	switch {
	case value >= wellAbove:
		return StateWellAbove
	case value >= barValue:
		return StateAbove
	case value >= barValue*(1-band):
		return StateAtThreshold
	default:
		return StateBelow
	}
}

// ---- Primitive 2: Rate-of-change (differencing) ----------------------------

// RateResult summarizes a stream's change over a window (doc 05 §3.2, doc 14 A5).
type RateResult struct {
	WindowDelta float64       // total increase across valid segments (counter: reset-aware sum of positive deltas)
	PerSecond   float64       // WindowDelta / valid elapsed seconds (smoothed first difference)
	Window      time.Duration // the window evaluated
	Elapsed     time.Duration // valid elapsed time the rate is over
	Samples     int           // samples inside the window
	Resets      int           // counter resets observed (any decrease) — each starts a fresh segment
	GapBroken   bool          // a scrape gap > 2 intervals truncated the window to the latest contiguous run
}

// EvalRate computes the smoothed first difference of a COUNTER over the window,
// ending at the latest sample (doc 14 A5):
//   - counter reset (any decrease) starts a new segment; the negative jump is never
//     summed and never extrapolated across.
//   - a scrape gap > 2 × scrapeInterval breaks the window: only the contiguous run
//     ending at the latest sample is used, and GapBroken is set.
//
// samples must be sorted oldest→newest (the hot store guarantees arrival order; the
// caller passes a window slice). now anchors the window; scrapeInterval sets the
// gap threshold.
func EvalRate(samples []qss.Sample, now time.Time, window, scrapeInterval time.Duration) RateResult {
	res := RateResult{Window: window}
	// Restrict to the window [now-window, now].
	start := now.Add(-window)
	win := make([]qss.Sample, 0, len(samples))
	for _, s := range samples {
		if !s.At.Before(start) && !s.At.After(now) {
			win = append(win, s)
		}
	}
	res.Samples = len(win)
	if len(win) < 2 {
		return res
	}
	// The hot ring stores samples in ARRIVAL order and is documented not to reorder
	// (doc 05 §3.1). cAdvisor stamps samples with its own collection time, which can
	// regress across scrapes (doc 14 A12), so arrival order != timestamp order. This
	// function's reset/gap logic assumes chronological order, so enforce it here
	// deterministically rather than trusting the caller — a stable sort by timestamp
	// keeps replay byte-identical regardless of scrape interleaving.
	sort.SliceStable(win, func(i, j int) bool { return win[i].At.Before(win[j].At) })

	// Trim to the contiguous run ending at the latest sample (gaps break the window).
	gapThreshold := 2 * scrapeInterval
	firstIdx := 0
	for i := 1; i < len(win); i++ {
		if win[i].At.Sub(win[i-1].At) > gapThreshold {
			firstIdx = i // a gap here: discard everything before i
			res.GapBroken = true
		}
	}
	run := win[firstIdx:]
	if len(run) < 2 {
		return res
	}

	// Sum positive deltas across reset-segmented sub-runs.
	for i := 1; i < len(run); i++ {
		d := run[i].Value - run[i-1].Value
		if d < 0 {
			res.Resets++ // reset: new segment, do not sum the drop, do not extrapolate
			continue
		}
		res.WindowDelta += d
	}
	res.Elapsed = run[len(run)-1].At.Sub(run[0].At)
	if res.Elapsed > 0 {
		res.PerSecond = res.WindowDelta / res.Elapsed.Seconds()
	}
	return res
}

// ---- Primitive 3: Co-occurrence (conjunction) ------------------------------

// CoocState is the conjunction verdict.
type CoocState uint8

const (
	CoocUnknown CoocState = iota
	CoocAllMet            // every member condition holds
	CoocPartial           // some but not all
	CoocNone              // none
)

func (s CoocState) String() string {
	switch s {
	case CoocAllMet:
		return "all-met"
	case CoocPartial:
		return "partial"
	case CoocNone:
		return "none"
	default:
		return "unknown"
	}
}

// CoocResult is the co-occurrence summary (doc 05 §3.2): several conditions true at
// once within a window. members are the current member conditions; the window is
// carried for provenance. The TEMPORAL-overlap refinement (member states true
// within W of each other across a topological neighbourhood) is detection's job
// (07) — the primitive itself is conjunction and does not change there.
type CoocResult struct {
	State   CoocState
	Met     int
	Total   int
	Window  time.Duration
	Members []bool
}

// EvalCooccurrence ANDs the member conditions (doc 05 §3.2). An empty member set is
// CoocUnknown (nothing to conjoin), never spuriously "all-met".
func EvalCooccurrence(members []bool, window time.Duration) CoocResult {
	res := CoocResult{Total: len(members), Window: window, Members: members}
	if len(members) == 0 {
		res.State = CoocUnknown
		return res
	}
	for _, m := range members {
		if m {
			res.Met++
		}
	}
	switch {
	case res.Met == res.Total:
		res.State = CoocAllMet
	case res.Met == 0:
		res.State = CoocNone
	default:
		res.State = CoocPartial
	}
	return res
}
