package forecast

import "math"

// Trend is the eligibility RESCUE for the flat() coefficient-of-variation gate (doc 09
// §3.6 companion). flat() measures AMPLITUDE — stddev/|level| over the 64-point tail — and
// so a series that is flat for 60 points and has just BEGUN a small directional creep in
// the last few still scores "flat" (its CV is dominated by the long quiet majority) and is
// falsely silenced, missing the onset of a leak at the earliest, most valuable moment.
// Worse, CV cannot be tuned to fix it: a noisy-but-trendless series scores HIGHER than a
// real early creep, so amplitude and onset overlap. Direction/persistence — not amplitude —
// is the right signal, so this is an EWMA-residual CUSUM with sustained-shift confirmation
// (the same proven detector as the off-digest onset producer, doc 22 C2), reimplemented on
// the bare float series.
//
// CHARTER: this lives entirely in the NON-GATING forecast warm path (off the digest, doc
// 01). Its params are DECLARED constants (borrowed-normativity), never learned. It changes
// only WHICH series are eligible to forecast — it produces no statement and feeds no digest,
// detection, or governance. It is a pure function: same series + same params ⇒ same verdict.

// TrendParams are the pinned CUSUM constants (declared, never learned). Mirrors the onset
// producer's defaults. Alpha is the EWMA baseline smoothing; K is the per-step slack and H
// the decision threshold (both in residual-sigma units); Warmup seeds the sigma estimate and
// is never tripped; MinZ is the SUSTAINED-shift floor in signal-sigma units (the
// transient-spike defense); RecentWindow bounds how near the series END a drift must have
// begun to count as "in progress" (an old step that has since plateaued is not an ongoing
// leak — it is the regime-shift detector's jurisdiction, or it is genuinely flat-at-a-new-level).
type TrendParams struct {
	Alpha        float64
	K            float64
	H            float64
	Warmup       int
	MinZ         float64
	RecentWindow int
}

// DefaultTrendParams mirror the onset/C2 defaults (validated at 0/400 false-onset on
// stationary noise, 400/400 detect on a real 6σ step), with a recent-window of 48 points.
func DefaultTrendParams() TrendParams {
	return TrendParams{Alpha: 0.10, K: 0.5, H: 4.0, Warmup: 12, MinZ: 3.0, RecentWindow: 48}
}

// EarlyOnset is the cold-start confidence caveat (MEASURED, doc 09 §3.6 companion): the
// series was RESCUED from the flat() silence by a sustained drift, but the new slope is
// still short (< TrendMinOnsetPoints points), so this projection is EARLY and the band is
// low-confidence — it will firm as the slope establishes. Surfaced ADJACENT to the
// projection (never fused), exactly like the RegimeShift caveat. It does not change the
// band; it labels its trustworthiness honestly.
type EarlyOnset struct {
	Direction      string  `json:"direction"`
	NewSlopePoints int     `json:"newSlopePoints"` // points observed since the drift began
	StepZ          float64 `json:"stepZ"`          // sustained shift magnitude in signal-sigma units (MEASURED)
	Note           string  `json:"note"`
}

// TrendResult is the verdict for one series.
type TrendResult struct {
	Tripped   bool    // a sustained directional drift is in progress at the tail
	Direction string  // "up" | "down"
	OnsetIdx  int     // index in the (finite) series where the latest drift began
	NewSlope  int     // points since the drift began (the new-slope length, for cold-start confidence)
	StepZ     float64 // sustained pre→post shift in signal-sigma units (a MEASURED magnitude)
}

// Trend reports whether a SUSTAINED directional drift is in progress at the tail of the
// series. Disabled (returns not-tripped) when H<=0, so the forecast gate is byte-identical
// to the pre-existing flat()-only behaviour. Pure + deterministic.
func Trend(series []float64, p TrendParams) TrendResult {
	if p.H <= 0 {
		return TrendResult{}
	}
	xs := finiteFloats(series)
	if len(xs) < p.Warmup+4 {
		return TrendResult{}
	}
	alpha := p.Alpha
	if alpha <= 0 || alpha > 1 {
		alpha = 0.15
	}
	sigN := trendSigPrefix(len(xs), p.Warmup)
	z := trendResidualZ(xs, alpha, sigN)
	signalSigma := trendRobustSigma(xs[:sigN])

	var gp, gn float64
	armedAt := p.Warmup
	last := TrendResult{}
	for i := p.Warmup; i < len(z); i++ {
		gp = math.Max(0, gp+z[i]-p.K)
		gn = math.Max(0, gn-z[i]-p.K)
		var sign int
		switch {
		case gp > p.H && i >= armedAt:
			sign = +1
		case gn > p.H && i >= armedAt:
			sign = -1
		default:
			continue
		}
		if r, ok := trendConfirm(xs, z, signalSigma, i, sign, p); ok {
			last = r
		}
		gp, gn, armedAt = 0, 0, i+3
	}
	// "In progress": the latest confirmed drift must have begun within RecentWindow points
	// of the series end. An old step that has since plateaued is not an ongoing leak.
	if last.Tripped && len(xs)-1-last.OnsetIdx > p.RecentWindow {
		return TrendResult{}
	}
	return last
}

// trendConfirm turns a CUSUM alarm into a confirmed sustained drift: the post-alarm median
// must differ from the pre-step median by ≥ MinZ signal-sigmas in the alarm direction (a
// transient blip settles back and is dropped). OnsetIdx is backtracked to where the step
// began.
func trendConfirm(xs []float64, z []float64, signalSigma float64, alarm, sign int, p TrendParams) (TrendResult, bool) {
	start := trendBacktrack(z, alarm, sign)
	w := p.Warmup / 2
	if w < 4 {
		w = 4
	}
	pre := trendWindowMedian(xs, start-w, start)
	post := trendWindowMedian(xs, alarm, alarm+w)
	shift := post - pre
	stepZ := math.Min(math.Abs(shift)/signalSigma, 99.9)
	if stepZ < p.MinZ {
		return TrendResult{}, false
	}
	if (sign > 0) != (shift > 0) {
		return TrendResult{}, false
	}
	dir := "up"
	if shift < 0 {
		dir = "down"
	}
	return TrendResult{
		Tripped: true, Direction: dir, OnsetIdx: start,
		NewSlope: len(xs) - start, StepZ: math.Round(stepZ*1e4) / 1e4,
	}, true
}

// --- numeric helpers (self-contained float ports of the onset producer's math) ---------

func finiteFloats(series []float64) []float64 {
	xs := make([]float64, 0, len(series))
	for _, v := range series {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		xs = append(xs, v)
	}
	return xs
}

func trendResidualZ(xs []float64, alpha float64, sigN int) []float64 {
	base := trendEWMA(xs, alpha)
	resid := make([]float64, len(xs))
	for i := 1; i < len(xs); i++ {
		resid[i] = xs[i] - base[i-1]
	}
	sigma := trendRobustSigma(resid[:sigN])
	z := make([]float64, len(resid))
	for i := range resid {
		z[i] = resid[i] / sigma
	}
	return z
}

func trendSigPrefix(n, warmup int) int {
	sigN := n / 3
	if lo := 2 * warmup; sigN < lo {
		sigN = lo
	}
	if sigN > 60 {
		sigN = 60
	}
	if sigN > n {
		sigN = n
	}
	return sigN
}

func trendEWMA(x []float64, alpha float64) []float64 {
	out := make([]float64, len(x))
	if len(x) == 0 {
		return out
	}
	out[0] = x[0]
	for i := 1; i < len(x); i++ {
		out[i] = alpha*x[i] + (1-alpha)*out[i-1]
	}
	return out
}

func trendRobustSigma(x []float64) float64 {
	med := trendMedian(x)
	dev := make([]float64, len(x))
	for i, v := range x {
		dev[i] = math.Abs(v - med)
	}
	return math.Max(1.4826*trendMedian(dev), 1e-9)
}

func trendMedian(x []float64) float64 {
	if len(x) == 0 {
		return 0
	}
	cp := append([]float64(nil), x...)
	for i := 1; i < len(cp); i++ {
		for j := i; j > 0 && cp[j-1] > cp[j]; j-- {
			cp[j-1], cp[j] = cp[j], cp[j-1]
		}
	}
	n := len(cp)
	if n%2 == 1 {
		return cp[n/2]
	}
	return 0.5 * (cp[n/2-1] + cp[n/2])
}

func trendWindowMedian(xs []float64, lo, hi int) float64 {
	if lo < 0 {
		lo = 0
	}
	if hi > len(xs) {
		hi = len(xs)
	}
	if lo >= hi {
		if lo >= 0 && lo < len(xs) {
			return xs[lo]
		}
		return 0
	}
	return trendMedian(xs[lo:hi])
}

func trendBacktrack(z []float64, alarm, sign int) int {
	i := alarm
	for i > 0 && float64(sign)*z[i-1] > 0.5 {
		i--
	}
	return i
}
