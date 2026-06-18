package forecast

import (
	"math"
	"testing"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/clock"
)

func sampleForecast() *clock.Forecast {
	// point rises 10→13; a symmetric-ish band around it.
	return &clock.Forecast{
		Point: []float64{10, 11, 12, 13},
		Quantiles: [][]float64{
			{8, 9, 10, 11},   // lower (p10)
			{10, 11, 12, 13}, // mid (p50) == point
			{12, 13, 14, 15}, // upper (p90)
		},
	}
}

func TestCalibrateIdentityIsByteIdentical(t *testing.T) {
	fc := sampleForecast()
	for _, cal := range []float64{1.0, 0, -1} { // 1.0 = identity; <=0 = treated as identity
		got := CalibrateBand(fc, cal)
		if got != fc { // identity returns the SAME pointer (no copy) ⇒ byte-identical path
			t.Errorf("cal=%v must be the identity (same forecast), got a copy %+v", cal, got)
		}
	}
}

func TestCalibrateWidensAndNarrowsAroundPoint(t *testing.T) {
	fc := sampleForecast()

	wide := CalibrateBand(fc, 2.0)
	narrow := CalibrateBand(fc, 0.5)
	for s := range fc.Point {
		p := fc.Point[s]
		// the point trajectory is never touched (calibration shapes uncertainty only).
		if wide.Quantiles[1][s] != p || narrow.Quantiles[1][s] != p {
			t.Errorf("step %d: point/mid quantile must be untouched", s)
		}
		// widen: half-width doubles around the point.
		rawLo, rawHi := fc.Quantiles[0][s], fc.Quantiles[2][s]
		if !approxF(wide.Quantiles[0][s], p+(rawLo-p)*2) || !approxF(wide.Quantiles[2][s], p+(rawHi-p)*2) {
			t.Errorf("step %d: cal=2 did not double the half-width: %v", s, wide.Quantiles)
		}
		// narrow: half-width halves — but never collapses (stays a band).
		if !approxF(narrow.Quantiles[0][s], p+(rawLo-p)*0.5) {
			t.Errorf("step %d: cal=0.5 did not halve the lower half-width", s)
		}
		if narrow.Quantiles[0][s] >= narrow.Quantiles[2][s] {
			t.Errorf("step %d: a narrowed band must still be a band (lower < upper), got %v..%v", s, narrow.Quantiles[0][s], narrow.Quantiles[2][s])
		}
		// quantile ORDER preserved (monotone non-decreasing across levels).
		if !(wide.Quantiles[0][s] <= wide.Quantiles[1][s] && wide.Quantiles[1][s] <= wide.Quantiles[2][s]) {
			t.Errorf("step %d: widened band broke quantile order: %v", s, []float64{wide.Quantiles[0][s], wide.Quantiles[1][s], wide.Quantiles[2][s]})
		}
	}
	// the raw input is not mutated.
	if fc.Quantiles[0][0] != 8 {
		t.Errorf("CalibrateBand mutated its input")
	}
}

// A tiny-but-positive calibration must NEVER collapse a nonzero band to a line
// (the charter's "bands never collapse" invariant).
func TestCalibrateNeverCollapses(t *testing.T) {
	fc := sampleForecast()
	got := CalibrateBand(fc, 1e-6)
	for s := range fc.Point {
		if got.Quantiles[0][s] >= got.Quantiles[2][s] {
			t.Fatalf("step %d: band collapsed under a positive calibration: %v..%v", s, got.Quantiles[0][s], got.Quantiles[2][s])
		}
	}
}

func approxF(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
