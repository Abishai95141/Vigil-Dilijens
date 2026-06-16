// Package departure is the "anomaly" half of Capability C (doc 15 §4.C): a MEASURED
// sample that fell OUTSIDE the PROJECTED band its OWN recent forecast drew for that time —
// the series did something its own forecast did not anticipate. It is SALVAGED from a
// KILLED shortcut (doc 15 §3 — "anomaly = band-departure surfaced MEASURED in the
// unexplained channel"): that died because a PROJECTED band edge entering the replay digest
// breaks byte-replay AND calling the comparison MEASURED launders PROJECTED→MEASURED.
// Departure survives ONLY off-digest, classed PROJECTED, in its own lane.
//
// CHARTER (the strictest in the system — this is the model-adjacent edge, doc 01):
//   - PROJECTED, never MEASURED. The band is the clock's forecast (PROJECTED); the realized
//     sample is MEASURED; a departure JOINS them ("the measured sample left its projected
//     band"), never fuses into a MEASURED anomaly SCORE. Weakest-input rule ⇒ PROJECTED.
//   - OFF-DIGEST. Zero bytes into replay.Digest (a model-derived flag is non-deterministic
//     by class and would break byte-replay). Its determinism is the honest, narrower one:
//     "same recorded band + sample + params ⇒ same flag" — a pure function, re-run + diff,
//     NOT the tick digest.
//   - NEVER feeds governance / the candidate funnel. A model-derived signal must not
//     influence an authored graph edge or a curated threshold.
//   - STRUCTURAL FP DEFENSE, not a learned threshold. The band IS the bar: a noisy-but-
//     stationary series gets a WIDE forecast band from the clock, so normal noise stays
//     INSIDE and never departs. The detector adds NO threshold of its own — it compares the
//     measured sample to the clock's own uncertainty. The only knob is a structural margin
//     scaled to the band width (below).
package departure

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// BandObservation is one forecast band recorded for a series at a time, paired with the
// MEASURED sample that realized at that time. The band is PROJECTED (clock-derived); the
// realized value is MEASURED. The producer JOINS them — never fuses.
type BandObservation struct {
	EntityCEI  string
	Metric     string
	At         time.Time // the time this band was FOR (and the realized sample's time)
	Lower      float64   // PROJECTED forecast band lower edge for At
	Upper      float64   // PROJECTED forecast band upper edge for At
	Realized   float64   // the MEASURED sample at At
	Confidence string    // tight | moderate | wide (carried verbatim from the forecast)
}

// Departure is a MEASURED sample that left its own PROJECTED forecast band. Classed
// PROJECTED (weakest input: the band). It is NOT a MEASURED "anomaly score" and carries no
// causal claim — only the honest join of a measured value against a projected window.
type Departure struct {
	EntityCEI string    `json:"entityCei"`
	Metric    string    `json:"metric"`
	At        time.Time `json:"at"`
	Class     string    `json:"class"` // "PROJECTED band ⋈ MEASURED sample (joined, never fused)"
	Side      string    `json:"side"`  // "above" | "below"
	Realized  float64   `json:"realized"`
	Lower     float64   `json:"lower"`
	Upper     float64   `json:"upper"`
	BandWidth float64   `json:"bandWidth"`
	// Exceedance is how far past the BAND EDGE the sample landed (≥ 0), NOT past the fire
	// threshold (the edge + the structural margin). So when a margin is active a firing
	// departure's Exceedance is always > the margin — exceedances within the margin are
	// absorbed (jitter) and never surfaced.
	Exceedance float64 `json:"exceedance"`
	Confidence string  `json:"confidence"`
	Detail     string  `json:"detail"` // honest text — no causal/anomaly-score claim
}

// Params are the (pinned) departure constants. MinExceedanceFraction is the ONLY knob: a
// STRUCTURAL margin (not a learned threshold) — a departure must exceed the band edge by at
// least this fraction of the band WIDTH, so a sample a hair outside a tight band does not
// flag. Because the width is the clock's own uncertainty, the margin scales with it. 0 =
// strict (any amount outside the band is a departure).
type Params struct {
	MinExceedanceFraction float64
}

const departureClass = "PROJECTED band ⋈ MEASURED sample (joined, never fused)"

// Detect flags every band observation whose MEASURED realized sample fell outside its
// PROJECTED band (beyond the structural margin). Pure and deterministic: same observations
// + same params ⇒ same departures, byte-for-byte. A malformed band (non-finite edges, or
// upper < lower) NEVER fabricates a departure — it is skipped (the degrade-never-fabricate
// rule extends to the anomaly edge).
func Detect(obs []BandObservation, p Params) []Departure {
	var out []Departure
	for _, o := range obs {
		if !finite(o.Lower) || !finite(o.Upper) || !finite(o.Realized) || o.Upper <= o.Lower {
			// A malformed band (non-finite, upper < lower) OR a ZERO-WIDTH band (upper ==
			// lower) is not trustworthy evidence: a band must NEVER collapse to a line
			// (doc 01), so a collapsed forecast is a degenerate output, not a tight one.
			// Comparing a sample to it would make the structural margin (a fraction of the
			// width) zero — a one-ULP hair-trigger, exactly where the FP defense must hold.
			// Skip it: degrade-never-fabricate extends to the model-adjacent anomaly edge.
			continue
		}
		width := o.Upper - o.Lower
		margin := 0.0
		if p.MinExceedanceFraction > 0 && width > 0 {
			margin = p.MinExceedanceFraction * width
		}
		var side string
		var exceed float64
		switch {
		case o.Realized > o.Upper+margin:
			side, exceed = "above", o.Realized-o.Upper
		case o.Realized < o.Lower-margin:
			side, exceed = "below", o.Lower-o.Realized
		default:
			continue // inside the band (or within the structural margin): normal, no departure
		}
		out = append(out, Departure{
			EntityCEI: o.EntityCEI, Metric: o.Metric, At: o.At.UTC(),
			Class: departureClass, Side: side,
			Realized: o.Realized, Lower: o.Lower, Upper: o.Upper, BandWidth: width,
			Exceedance: exceed, Confidence: confOrNA(o.Confidence),
			Detail: fmt.Sprintf("the measured %s sample %.4g left its projected band [%.4g, %.4g] (%s by %.4g) — its own recent forecast did not anticipate this",
				sideWord(side), o.Realized, o.Lower, o.Upper, side, exceed),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].EntityCEI != out[j].EntityCEI {
			return out[i].EntityCEI < out[j].EntityCEI
		}
		if out[i].Metric != out[j].Metric {
			return out[i].Metric < out[j].Metric
		}
		return out[i].At.Before(out[j].At)
	})
	return out
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func confOrNA(c string) string {
	if c == "" {
		return "confidence n/a"
	}
	return c
}

func sideWord(side string) string {
	if side == "below" {
		return "low"
	}
	return "high"
}
