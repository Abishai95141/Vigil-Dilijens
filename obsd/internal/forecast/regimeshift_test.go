package forecast

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/params"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/qss"
)

// rsParams returns the dev-profile regime-shift settings (so the test pins the
// SHIPPED behaviour, not an invented config). MinContext drives the severe split.
func rsParams() params.ForecastParams {
	return params.ForecastParams{
		MinContext:            64,
		RegimeShiftFlag:       true,
		RegimeShiftFraction:   0.35,
		RegimeShiftMinSegment: 8,
		RegimeShiftPlateau:    0.35,
	}
}

// --- series builders (the shapes of real workloads) ---

func flatSeries(n int, level float64) []float64 {
	s := make([]float64, n)
	for i := range s {
		s[i] = level
	}
	return s
}

// noisyFlat is a flat regime with bounded sinusoidal noise (no net trend) — a real
// gauge never sits perfectly still.
func noisyFlat(n int, level, amp float64) []float64 {
	s := make([]float64, n)
	for i := range s {
		s[i] = level + amp*math.Sin(float64(i)*0.7)
	}
	return s
}

// linRamp is a linear climb from->to over n points — the LEAK shape. The detector
// must NEVER flag this (it is the early-warning signal itself).
func linRamp(n int, from, to float64) []float64 {
	s := make([]float64, n)
	for i := range s {
		s[i] = from + (to-from)*float64(i)/float64(n-1)
	}
	return s
}

// stepThenFlat is the CONTAMINATION shape: a flat pre-regime, a sharp rise at `at`,
// then a flat post-regime. `noise` adds bounded wobble to both regimes.
func stepThenFlat(pre, post int, lo, hi, noise float64) []float64 {
	s := make([]float64, 0, pre+post)
	for i := 0; i < pre; i++ {
		s = append(s, lo+noise*math.Sin(float64(i)*0.9))
	}
	for i := 0; i < post; i++ {
		s = append(s, hi+noise*math.Sin(float64(i)*0.9))
	}
	return s
}

func concat(parts ...[]float64) []float64 {
	var out []float64
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func TestDetectRegimeShift(t *testing.T) {
	tests := []struct {
		name       string
		series     []float64
		wantFound  bool
		wantSevere bool // only checked when wantFound
	}{
		// --- CARDINAL: never flag a leak / ongoing climb ---
		{"pure-linear-ramp-leak", linRamp(200, 0, 100), false, false},
		{"steep-short-ramp", linRamp(20, 0, 100), false, false},
		{"steep-ramp-then-plateau-leak", concat(linRamp(20, 0, 100), flatSeries(100, 100)), false, false},
		{"gentle-long-ramp", linRamp(400, 50, 300), false, false},
		{"sawtooth-remainder-real-leak", linRamp(72, 3.8, 191.7), false, false}, // the leak-saw-slow ramp shape (real corpus)

		// --- contamination: a step UP that plateaus ---
		{"step-up-recent-severe", stepThenFlat(200, 40, 100, 300, 0), true, true},    // post 40 < MinContext 64
		{"step-up-old-not-severe", stepThenFlat(200, 100, 100, 300, 0), true, false}, // post 100 >= 64
		{"step-up-noisy-regimes", stepThenFlat(160, 50, 200, 420, 6), true, true},    // post 50 < 64, with noise

		// --- not contamination ---
		{"flat", flatSeries(200, 100), false, false},
		{"noisy-flat", noisyFlat(200, 100, 4), false, false},
		{"step-DOWN-is-a-reset-not-a-shift", stepThenFlat(80, 80, 300, 100, 0), false, false}, // downward → never flagged
		{"single-spike-transient", concat(flatSeries(100, 100), []float64{900}, flatSeries(99, 100)), false, false},
		{"tiny-step-below-magnitude-floor", concat(flatSeries(100, 100), flatSeries(100, 108)), false, false}, // 8% jump vs range; below 35% floor
		{"too-short", flatSeries(12, 100), false, false},
	}
	p := rsParams()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, found := DetectRegimeShift(tc.series, p)
			if found != tc.wantFound {
				t.Fatalf("found = %v, want %v (shift=%+v)", found, tc.wantFound, got)
			}
			if found && got.Severe != tc.wantSevere {
				t.Fatalf("severe = %v, want %v (shift=%+v)", got.Severe, tc.wantSevere, got)
			}
			if found {
				if got.PostLevel <= got.PreLevel {
					t.Fatalf("a flagged shift must be UPWARD: pre=%v post=%v", got.PreLevel, got.PostLevel)
				}
				// the relative-rise floor: the new baseline rose by >= fraction
				if got.PreLevel > 1e-9 && got.PostLevel < got.PreLevel*(1+p.RegimeShiftFraction) {
					t.Fatalf("rise %v->%v below the relative floor (1+%v)", got.PreLevel, got.PostLevel, p.RegimeShiftFraction)
				}
				if got.PostPoints != len(tc.series)-got.AtIndex {
					t.Fatalf("postPoints %d != len-atIndex %d", got.PostPoints, len(tc.series)-got.AtIndex)
				}
			}
		})
	}
}

// TestDetectRegimeShiftDisabled — the flag is opt-out: off ⇒ never detects.
func TestDetectRegimeShiftDisabled(t *testing.T) {
	p := rsParams()
	p.RegimeShiftFlag = false
	if _, found := DetectRegimeShift(stepThenFlat(100, 40, 100, 400, 0), p); found {
		t.Fatal("disabled flag must never detect")
	}
}

// TestDetectRegimeShiftDeterministic — same input ⇒ identical verdict (replay-stable).
func TestDetectRegimeShiftDeterministic(t *testing.T) {
	p := rsParams()
	s := stepThenFlat(120, 50, 150, 380, 7)
	a, fa := DetectRegimeShift(s, p)
	b, fb := DetectRegimeShift(s, p)
	if fa != fb || a != b {
		t.Fatalf("non-deterministic: (%v,%+v) vs (%v,%+v)", fa, a, fb, b)
	}
}

// TestDetectRegimeShiftLeakCorpusShapes is the CARDINAL guarantee on a battery of
// realistic leak/ramp shapes: a true early-warning signal is NEVER mistaken for
// contamination. A false positive here would discredit a real forecast — the one
// unacceptable error.
func TestDetectRegimeShiftLeakCorpusShapes(t *testing.T) {
	p := rsParams()
	shapes := map[string][]float64{
		"slow-creep":         linRamp(240, 100, 480),
		"fast-creep":         linRamp(80, 100, 490),
		"creep-with-noise":   addNoise(linRamp(200, 120, 470), 8),
		"accelerating-creep": accel(200, 100, 500),
		"post-reset-ramp":    linRamp(90, 5, 300), // the spliced remainder after a restart
		"creep-then-plateau": concat(linRamp(120, 100, 460), flatSeries(60, 460)),
		"two-stage-creep":    concat(linRamp(100, 100, 250), linRamp(100, 250, 480)),
	}
	for name, s := range shapes {
		if rs, found := DetectRegimeShift(s, p); found {
			t.Errorf("%s: FALSE POSITIVE — a leak/ramp flagged as contamination: %+v", name, rs)
		}
	}
}

func addNoise(s []float64, amp float64) []float64 {
	out := make([]float64, len(s))
	for i, v := range s {
		out[i] = v + amp*math.Sin(float64(i)*0.8)
	}
	return out
}

func accel(n int, from, to float64) []float64 {
	s := make([]float64, n)
	for i := range s {
		f := float64(i) / float64(n-1)
		s[i] = from + (to-from)*f*f // quadratic: slow then fast
	}
	return s
}

// flatStepSamples builds a flat-then-stepped-up []qss.Sample (pre points at lo, post
// points at hi) with increasing timestamps — the contamination shape, as a stream.
func flatStepSamples(pre, post int, lo, hi float64) []qss.Sample {
	n := pre + post
	out := make([]qss.Sample, 0, n)
	for i := 0; i < n; i++ {
		v := lo
		if i >= pre {
			v = hi
		}
		out = append(out, qss.Sample{At: t0.Add(time.Duration(i-n+1) * 15 * time.Second), Value: v})
	}
	return out
}

// TestRunCycleRegimeShift proves the WIRING end-to-end: a recent undeclared upward
// step that plateaus under the bar is SILENCED (SilenceRegimeShift) rather than
// forecast off the stale baseline, while a genuine leak ramp is NEVER silenced as a
// regime shift (the cardinal rule) and emits a clean, unflagged candidate.
func TestRunCycleRegimeShift(t *testing.T) {
	p := fp()
	p.MinContext = 64
	p.RegimeShiftFlag = true
	p.RegimeShiftFraction = 0.35
	p.RegimeShiftMinSegment = 8
	p.RegimeShiftPlateau = 0.35

	stepT := wsTarget() // bar 486, direction above
	stepT.CEIKey, stepT.StreamUID = "i|cl|shop|Pod|step|uid-step", "uid-step"
	leakT := wsTarget()
	leakT.CEIKey, leakT.StreamUID = podA, "uid-a"

	m := wsTarget().Metric
	r := &fakeReader{
		streams: map[string][]string{
			"uid-step\x1f" + m: {"s-step"},
			"uid-a\x1f" + m:    {"s-leak"},
		},
		samples: map[string][]qss.Sample{
			// 200 @100 then 40 @300 — a baseline that just lurched up but stays under
			// the bar; the new regime (40 pts) is < MinContext ⇒ severe.
			"s-step": flatStepSamples(200, 40, 100, 300),
			// a genuine leak ramp toward the bar (300..419) — the early-warning signal.
			"s-leak": ramp(120, 300, 1),
		},
	}
	sc := &scriptedClock{fc: scriptedForecast(64, 419, 5, 20)}
	res := RunCycle(context.Background(), sc, CycleInput{
		Now: t0, Targets: []Target{stepT, leakT}, Reader: r,
		Cadence: 15 * time.Second, GraphVersion: "v", P: p,
	})

	gotStep := ""
	for _, s := range res.Silences {
		if s.EntityCEI == stepT.CEIKey {
			gotStep = s.Reason
		}
		if s.EntityCEI == leakT.CEIKey && s.Reason == SilenceRegimeShift {
			t.Fatal("CARDINAL VIOLATION: a leak ramp was silenced as a regime shift")
		}
	}
	if gotStep != SilenceRegimeShift {
		t.Fatalf("step target: silence = %q, want %q", gotStep, SilenceRegimeShift)
	}
	if len(res.Candidates) != 1 || res.Candidates[0].EntityCEI != leakT.CEIKey {
		t.Fatalf("the leak target must emit exactly one candidate: %+v", res.Candidates)
	}
	if res.Candidates[0].RegimeShift != nil {
		t.Fatalf("a clean leak ramp must carry no regime-shift flag: %+v", res.Candidates[0].RegimeShift)
	}
}
