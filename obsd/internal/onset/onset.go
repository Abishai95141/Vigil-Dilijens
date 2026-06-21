// Package onset is an OFF-DIGEST MEASURED producer (doc 22 C2): the deterministic
// changepoint TIME of a gauge series — "the series stepped (up|down) at T" — computed by
// EWMA-residual CUSUM with a sustained-shift confirmation. It exists to give a precise
// onset TIME to a level shift, which the three on-digest primitives (threshold / rate /
// co-occurrence) do not pinpoint, and which a sub-threshold shift (a jump that never
// crosses a bar) leaves entirely unmarked.
//
// CHARTER (doc 01 / 08 — read before touching this):
//   - MEASURED, but OFF-DIGEST. An onset is a deterministic arithmetic consequence of the
//     readings (EWMA, robust MAD sigma, cumulative sums, pre/post medians) — MEASURED by
//     class. BUT the three statistical primitives are a CLOSED set (observe/primitives.go);
//     a changepoint is not one of them, so it must NOT enter the deterministic
//     fingerprint/digest. It lives off-digest exactly like departure: its determinism is
//     the honest, narrower one — "same samples + same params ⇒ same onsets, byte-for-byte"
//     (a pure function, re-run and diff), NOT the replay tick digest.
//   - NOT A LOUDNESS CLAUSE. It does NOT expand what counts as loud/anomalous in the
//     unexplained channel (doc 08 keeps loudness = bar-crossing + rate-excursion only, "no
//     novelty score, no looks-unusual"). It only ANNOTATES an already-loud signal with a
//     "stepped at T" time, and supplies direction-free temporal adjacency for C3 (doc 22) —
//     which side moved first, surfaced for a human to judge, never as a cause.
//   - NO causal claim, NO novelty/anomaly vocabulary. StepZ is a MEASURED magnitude (the
//     sustained level shift in robust-sigma units), an arithmetic quantity, never a "this
//     is unusual" score. The text says what happened (a step), never that it is bad.
//   - NEVER feeds governance / the candidate funnel directly. (C3 stages a firewalled,
//     direction-free candidate from co-onsets; that path, not this producer, is the gate.)
package onset

import (
	"fmt"
	"math"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/qss"
)

// Onset is one detected level changepoint in a gauge series: the time the step began, its
// direction, and the size of the SUSTAINED shift in robust-sigma units. MEASURED
// (deterministic arithmetic), no causal or novelty claim.
type Onset struct {
	EntityCEI string    `json:"entityCei"`
	Metric    string    `json:"metric"`
	At        time.Time `json:"at"`        // changepoint time, backtracked to where the step began
	Direction string    `json:"direction"` // "up" | "down"
	StepZ     float64   `json:"stepZ"`     // sustained |pre→post| shift in robust-sigma units (a MEASURED magnitude)
	Class     string    `json:"class"`
	Detail    string    `json:"detail"`
}

const onsetClass = "MEASURED changepoint (off-digest; a step's onset TIME, not an anomaly score)"

// MaxStepZ is the reported-magnitude ceiling: a step on a (near-)flat baseline is
// effectively infinitely many robust deviations, so the surfaced StepZ is clamped here to
// keep it meaningful. It does not affect whether an onset fires (the CUSUM + MinZ gate do).
const MaxStepZ = 99.9

// Params are the pinned CUSUM constants (declared, never learned — borrowed-normativity).
// Alpha is the EWMA baseline smoothing (smaller ⇒ a step stays in the residual longer). K
// is the per-step slack the cumulative sum tolerates; H is its decision threshold (both in
// residual-sigma units). Warmup samples at the head are never flagged and seed the sigma
// estimate. MinZ is the SUSTAINED-SHIFT floor: an alarm is only emitted if the pre→post
// level actually moved by at least this many signal-sigmas — the false-positive defense
// (a transient spike settles back to baseline and is dropped; a real step persists).
type Params struct {
	Alpha  float64
	K      float64
	H      float64
	Warmup int
	MinZ   float64
}

// DefaultParams are the pinned defaults, tuned by the in-package sweep (sweep_test.go) for
// a low false-onset rate on stationary noise while reliably catching a genuine sustained
// step. Validated in onset_test.go (the competitor-engine E8 battery, reimplemented).
func DefaultParams() Params { return Params{Alpha: 0.10, K: 0.5, H: 4.0, Warmup: 12, MinZ: 3.0} }

// Detect returns the changepoints in one gauge series, oldest first. Pure and
// deterministic: same samples + same params ⇒ identical onsets. A series shorter than the
// warmup-plus-a-little is never flagged (too little to judge — honest silence). Non-finite
// samples are treated as gaps and skipped.
func Detect(entityCEI, metric string, samples []qss.Sample, p Params) []Onset {
	if p.H <= 0 {
		return nil
	}
	xs, times := finiteValues(samples)
	if len(xs) < p.Warmup+4 {
		return nil
	}
	alpha := p.Alpha
	if alpha <= 0 || alpha > 1 {
		alpha = 0.15
	}
	sigN := sigPrefixLen(len(xs), p.Warmup)
	z := residualZ(xs, alpha, sigN)
	signalSigma := robustSigma(xs[:sigN]) // noise level of the SIGNAL (for the sustained-shift magnitude)

	var out []Onset
	var gp, gn float64 // upper / lower CUSUM accumulators
	armedAt := p.Warmup
	for i := p.Warmup; i < len(z); i++ {
		gp = math.Max(0, gp+z[i]-p.K)
		gn = math.Max(0, gn-z[i]-p.K)
		switch {
		case gp > p.H && i >= armedAt:
			if o, ok := confirmOnset(entityCEI, metric, xs, times, z, signalSigma, i, +1, p); ok {
				out = append(out, o)
			}
			gp, gn, armedAt = 0, 0, i+3
		case gn > p.H && i >= armedAt:
			if o, ok := confirmOnset(entityCEI, metric, xs, times, z, signalSigma, i, -1, p); ok {
				out = append(out, o)
			}
			gp, gn, armedAt = 0, 0, i+3
		}
	}
	return out
}

// confirmOnset turns a CUSUM alarm into an Onset only if the level shift is SUSTAINED: the
// post-alarm median must differ from the pre-step median by at least MinZ signal-sigmas, in
// the alarm's direction. This is the FP defense — a transient blip settles back so its
// pre→post shift is ~0 and it is dropped; a genuine step persists. At marks where the step
// BEGAN (backtracked), not where the accumulator tripped.
func confirmOnset(cei, metric string, xs []float64, times []time.Time, z []float64, signalSigma float64, alarm, sign int, p Params) (Onset, bool) {
	start := backtrack(z, alarm, sign)
	w := p.Warmup / 2
	if w < 4 {
		w = 4
	}
	pre := windowMedian(xs, start-w, start)
	post := windowMedian(xs, alarm, alarm+w)
	shift := post - pre
	// A step on a (near-)flat baseline has ~zero MAD, so signalSigma hits its floor and the
	// raw ratio explodes (a real cluster step on a flat memory baseline reported 2.6e17). The
	// magnitude is genuinely "very large"; report a sane ceiling rather than a meaningless
	// number. Detection is unaffected — only the surfaced StepZ is clamped.
	stepZ := math.Min(math.Abs(shift)/signalSigma, MaxStepZ)
	if stepZ < p.MinZ {
		return Onset{}, false // not a sustained shift — a transient, filtered
	}
	if (sign > 0) != (shift > 0) {
		return Onset{}, false // alarm side and realized shift disagree — don't trust it
	}
	dir := "up"
	if shift < 0 {
		dir = "down"
	}
	return Onset{
		EntityCEI: cei, Metric: metric, At: times[start].UTC(), Direction: dir,
		StepZ: round4(stepZ), Class: onsetClass,
		Detail: fmt.Sprintf("the series stepped %s at this time by %.3g robust deviations (sustained changepoint, not an anomaly score)", dir, stepZ),
	}, true
}

// residualZ returns the one-step-ahead EWMA residual of xs, standardized by a robust sigma
// estimated on the quiet prefix resid[:sigN].
func residualZ(xs []float64, alpha float64, sigN int) []float64 {
	baseEWMA := ewma(xs, alpha)
	resid := make([]float64, len(xs))
	for i := 1; i < len(xs); i++ {
		resid[i] = xs[i] - baseEWMA[i-1] // predict x[i] from the baseline through i-1
	}
	sigma := robustSigma(resid[:sigN])
	z := make([]float64, len(resid))
	for i := range resid {
		z[i] = resid[i] / sigma
	}
	return z
}

// sigPrefixLen is the quiet-prefix length for sigma estimation: len/3, clamped to
// [2*warmup, 60] (too short an estimate under-states sigma and fabricates onsets).
func sigPrefixLen(n, warmup int) int {
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

func ewma(x []float64, alpha float64) []float64 {
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

// robustSigma is the MAD-based sigma (1.4826 * median-abs-deviation), floored so a flat
// prefix can never divide by zero.
func robustSigma(x []float64) float64 {
	med := median(x)
	dev := make([]float64, len(x))
	for i, v := range x {
		dev[i] = math.Abs(v - med)
	}
	return math.Max(1.4826*median(dev), 1e-9)
}

func median(x []float64) float64 {
	if len(x) == 0 {
		return 0
	}
	cp := append([]float64(nil), x...)
	sortFloats(cp)
	n := len(cp)
	if n%2 == 1 {
		return cp[n/2]
	}
	return 0.5 * (cp[n/2-1] + cp[n/2])
}

// windowMedian returns the median of xs over [lo, hi), clamped to the slice bounds.
func windowMedian(xs []float64, lo, hi int) float64 {
	if lo < 0 {
		lo = 0
	}
	if hi > len(xs) {
		hi = len(xs)
	}
	if lo >= hi {
		if lo < len(xs) {
			return xs[lo]
		}
		return 0
	}
	return median(xs[lo:hi])
}

// backtrack walks from the alarm sample back to the first sample contributing to the shift
// in the alarm's direction — so At marks where the step BEGAN, not where the accumulator
// finally tripped.
func backtrack(z []float64, alarm, sign int) int {
	i := alarm
	for i > 0 && float64(sign)*z[i-1] > 0.5 {
		i--
	}
	return i
}

func finiteValues(samples []qss.Sample) ([]float64, []time.Time) {
	xs := make([]float64, 0, len(samples))
	ts := make([]time.Time, 0, len(samples))
	for _, s := range samples {
		if math.IsNaN(s.Value) || math.IsInf(s.Value, 0) {
			continue
		}
		xs = append(xs, s.Value)
		ts = append(ts, s.At)
	}
	return xs, ts
}

func round4(v float64) float64 { return math.Round(v*1e4) / 1e4 }

// sortFloats is a tiny in-place ascending insertion sort (median is over small windows).
func sortFloats(a []float64) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}
