package forecast

import "github.com/Abishai95141/Vigil-Dilijens/obsd/internal/clock"

// CalibrateBand scales the clock band's half-width around the point estimate by the
// DECLARED calibration constant (doc 20 P5). The constant is OFFLINE-derived (from the
// backtest gate's measured band coverage), customer-INVARIANT, and versioned — the same
// class as MaxBandRatio, NOT online learning. cal == 1 (the identity) and cal <= 0 both
// return the input UNCHANGED, so the default path is byte-identical to no calibration.
//
// The scale is AFFINE around the point estimate (p + (q-p)·cal), so:
//   - the point trajectory is untouched (calibration shapes uncertainty, not the forecast);
//   - quantile ORDER is preserved (a positive scale of a monotone band stays monotone);
//   - the band NEVER collapses to a line for cal > 0 (a nonzero spread stays nonzero) —
//     the charter's "bands never collapse" invariant holds by construction.
//
// It returns a NEW Forecast (the input is not mutated) so the raw clock answer remains
// available to any other reader.
func CalibrateBand(fc *clock.Forecast, cal float64) *clock.Forecast {
	if fc == nil || cal <= 0 || cal == 1 {
		return fc
	}
	out := &clock.Forecast{Point: fc.Point, Quantiles: make([][]float64, len(fc.Quantiles))}
	for q := range fc.Quantiles {
		row := make([]float64, len(fc.Quantiles[q]))
		for s := range fc.Quantiles[q] {
			var p float64
			if s < len(fc.Point) {
				p = fc.Point[s]
			}
			row[s] = p + (fc.Quantiles[q][s]-p)*cal
		}
		out.Quantiles[q] = row
	}
	return out
}
