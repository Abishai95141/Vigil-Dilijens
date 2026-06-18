package forecast

import (
	"sort"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/params"
)

// Regime-shift contamination flag (doc 09 M5 companion) — the honest answer to the
// "an undeclared event biases the forecast" gap.
//
// Decomposition (decompose.go) only removes a footprint at a DECLARED context window
// or an auto-detected gauge RESET (a DROP). A config change, deploy, or workload
// migration that RAISES the baseline toward the bar leaves no reset and — if the
// operator did not declare it — no splice. Both the pre- and post-shift regimes then
// enter the clock's input, and the forecast is regime-mixed: the band inflates, and
// (worse) when the new, higher baseline is recent the projection is dominated by the
// STALE low history and reads "flat, no crossing" — falsely reassuring exactly when
// the level just lurched toward the bar.
//
// This detector FLAGS that contamination as a MEASURED fact about the input; it NEVER
// trims it. The CARDINAL rule: an upward RAMP is the leak signal the forecaster exists
// to find — auto-removing an upward move would blind the very early warning. So the
// detector fires ONLY on a STEP that PLATEAUS (an old roughly-flat regime → a sharp
// rise → a new roughly-flat regime) and stays silent on any ongoing ramp. A declared
// context window remains the only thing that CLEANS the input (borrowed normativity:
// the operator declares the event; the system only measures the discontinuity).
//
// Structural + deterministic: means/medians over authored-width windows and authored
// thresholds (params) — no learned edge, weight, or threshold anywhere.

// RegimeShift is one detected UNDECLARED upward baseline shift in a forecast input
// (the post-decomposition series the clock sees). MEASURED — a fact about the input,
// never a forecast or a cause.
type RegimeShift struct {
	AtIndex      int     `json:"atIndex"`      // index in the series where the NEW regime begins
	PreLevel     float64 `json:"preLevel"`     // representative level of the old regime (median)
	PostLevel    float64 `json:"postLevel"`    // representative level of the new regime (median)
	JumpFraction float64 `json:"jumpFraction"` // (postLevel − preLevel) / window-range — the shift's magnitude
	PostPoints   int     `json:"postPoints"`   // points in the new regime (the forecastable remainder)

	// Severe ⇒ the new regime has fewer than MinContext points, so the projection
	// would be dominated by the STALE old baseline (falsely reassuring). The runner
	// SILENCES these (SilenceRegimeShift) rather than emit a misleading forecast.
	// When false, the projection is emitted but FLAGGED (its band may be inflated).
	Severe bool `json:"severe"`
}

// DetectRegimeShift scans a (post-decomposition) forecast input for an UNDECLARED
// upward LEVEL SHIFT that plateaus. It returns the most significant such shift and
// true, or a zero value and false when the series is a single regime, an ongoing
// ramp (a possible leak — never flagged), flat, or merely noisy.
//
// Pure + deterministic: same series + same params ⇒ same verdict.
func DetectRegimeShift(series []float64, p params.ForecastParams) (RegimeShift, bool) {
	if !p.RegimeShiftFlag {
		return RegimeShift{}, false
	}
	seg := p.RegimeShiftMinSegment
	if seg < 2 {
		seg = 2
	}
	n := len(series)
	if n < 2*seg {
		return RegimeShift{}, false // too short to carry two distinct regimes
	}

	lo, hi := series[0], series[0]
	for _, v := range series {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	rng := hi - lo
	if rng <= 0 {
		return RegimeShift{}, false // perfectly flat — no shift
	}

	// 1) Locate the boundary with the largest LOCAL upward jump — the mean of the
	// next `seg` points minus the mean of the prior `seg`. One forward pass. Only
	// UPWARD jumps are considered (a drop is a reset, decompose's job, and the
	// dangerous direction is the rise toward the bar).
	bestIdx, bestLocal := -1, 0.0
	for i := seg; i <= n-seg; i++ {
		if j := windowMean(series[i:i+seg]) - windowMean(series[i-seg:i]); j > bestLocal {
			bestLocal, bestIdx = j, i
		}
	}
	if bestIdx < 0 || bestLocal <= 0 {
		return RegimeShift{}, false // no upward step at all
	}

	// 2) The new baseline must be MATERIALLY higher than the old — a relative rise of
	// at least RegimeShiftFraction (a config/deploy that raised the baseline by ≥X%),
	// judged on the robust regime MEDIANS. A range-relative floor is wrong here: in a
	// flat→step→flat window the step IS the whole range, so any step would pass; the
	// rise must be material relative to the OLD LEVEL.
	pre, post := series[:bestIdx], series[bestIdx:]
	preLevel, postLevel := windowMedian(pre), windowMedian(post)
	jump := postLevel - preLevel
	if jump <= 0 {
		return RegimeShift{}, false
	}
	const eps = 1e-9
	if preLevel > eps && postLevel < preLevel*(1+p.RegimeShiftFraction) {
		return RegimeShift{}, false // rise too small to be a regime shift
	} // preLevel ≈ 0: any positive new baseline is itself a regime change

	// 3) Validate it is a STEP that PLATEAUS, not a ramp. Require BOTH regimes to be
	// roughly flat — net drift small relative to the jump. An ongoing ramp's segments
	// keep climbing → large drift → rejected. THIS is the leak-protection guard: a
	// real leak (a sustained climb) can never be flagged as contamination.
	guard := p.RegimeShiftPlateau * jump
	if segDrift(pre) > guard || segDrift(post) > guard {
		return RegimeShift{}, false // a climbing regime — never flag (it may be the leak)
	}

	return RegimeShift{
		AtIndex:      bestIdx,
		PreLevel:     preLevel,
		PostLevel:    postLevel,
		JumpFraction: jump / rng,
		PostPoints:   len(post),
		Severe:       len(post) < p.MinContext,
	}, true
}

func windowMean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

func windowMedian(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	c := append([]float64(nil), xs...)
	sort.Float64s(c)
	m := len(c) / 2
	if len(c)%2 == 1 {
		return c[m]
	}
	return (c[m-1] + c[m]) / 2
}

// segDrift is |median(last quartile) − median(first quartile)| of a segment — a
// robust net drift. Near zero for a plateau; large for an ongoing ramp.
func segDrift(seg []float64) float64 {
	q := len(seg) / 4
	if q < 1 {
		q = 1
	}
	d := windowMedian(seg[len(seg)-q:]) - windowMedian(seg[:q])
	if d < 0 {
		d = -d
	}
	return d
}
