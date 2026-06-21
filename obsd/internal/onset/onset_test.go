package onset

import (
	"math"
	"math/rand"
	"reflect"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/qss"
)

var base = time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)

// series builds gauge samples at 15s cadence from a value generator.
func series(n int, gen func(i int) float64) []qss.Sample {
	out := make([]qss.Sample, n)
	for i := range out {
		out[i] = qss.Sample{At: base.Add(time.Duration(i) * 15 * time.Second), Value: gen(i)}
	}
	return out
}

func noiseStep(seed int64, n, stepAt int, level, sigma, step float64) []qss.Sample {
	rng := rand.New(rand.NewSource(seed))
	return series(n, func(i int) float64 {
		v := level + rng.NormFloat64()*sigma
		if i >= stepAt {
			v += step
		}
		return v
	})
}

func TestOnsetDetectsRealStepAtItsStart(t *testing.T) {
	s := noiseStep(1, 120, 80, 100, 2, 14) // a clean +14 (7σ) up-step at index 80
	got := Detect("cei", "m", s, DefaultParams())
	if len(got) == 0 {
		t.Fatal("a clear 7σ step must be detected")
	}
	o := got[0]
	if o.Direction != "up" {
		t.Errorf("direction = %q, want up", o.Direction)
	}
	// At must mark the STEP START (~index 80), not the alarm a few samples later.
	idx := int(o.At.Sub(base) / (15 * time.Second))
	if idx < 76 || idx > 82 {
		t.Errorf("onset At index = %d, want ~80 (the step start)", idx)
	}
	if o.StepZ < 3 {
		t.Errorf("stepZ = %.2f, want a multi-sigma magnitude", o.StepZ)
	}
}

func TestOnsetDetectsDownStep(t *testing.T) {
	s := noiseStep(2, 120, 70, 100, 2, -14)
	got := Detect("cei", "m", s, DefaultParams())
	if len(got) == 0 || got[0].Direction != "down" {
		t.Fatalf("a downward step must be detected as down, got %+v", got)
	}
}

// THE ROBUSTNESS BATTERY (E8 parity with the competitor engine, reimplemented in Go):
// false-onset rate on pure stationary noise must stay low, detection on a real step high.
func TestOnsetRobustness_NoiseVsStep(t *testing.T) {
	const trials = 400
	falsePos := 0
	for s := 0; s < trials; s++ {
		rng := rand.New(rand.NewSource(int64(10000 + s))) // one stream per series, deterministic by seed
		noise := series(120, func(i int) float64 { return 100 + rng.NormFloat64()*2 })
		if len(Detect("c", "m", noise, DefaultParams())) > 0 {
			falsePos++
		}
	}
	detected := 0
	for s := 0; s < trials; s++ {
		if len(Detect("c", "m", noiseStep(int64(20000+s), 120, 80, 100, 2, 12), DefaultParams())) > 0 {
			detected++
		}
	}
	fpRate := float64(falsePos) / trials
	detRate := float64(detected) / trials
	t.Logf("false-onset rate on pure noise = %d/%d = %.3f", falsePos, trials, fpRate)
	t.Logf("detection rate on a real 6σ step = %d/%d = %.3f", detected, trials, detRate)
	// The sustained-shift confirmation drives FP to ~0; the bar is conservative vs the
	// measured 0.000 / 1.000 so it is not flaky across Go's rand stream versions.
	if fpRate >= 0.02 {
		t.Errorf("false-onset rate %.3f too high (want < 0.02)", fpRate)
	}
	if detRate <= 0.98 {
		t.Errorf("detection rate %.3f too low (want > 0.98)", detRate)
	}
}

func TestOnsetDeterministic(t *testing.T) {
	s := noiseStep(7, 120, 60, 50, 3, 20)
	a := Detect("cei", "m", s, DefaultParams())
	b := Detect("cei", "m", s, DefaultParams())
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("onset detection is not deterministic:\n%+v\n%+v", a, b)
	}
}

func TestOnsetShortSeriesSilent(t *testing.T) {
	s := series(10, func(i int) float64 { return float64(i) * 100 }) // < warmup+4, never flagged
	if got := Detect("cei", "m", s, DefaultParams()); len(got) != 0 {
		t.Errorf("a too-short series must be silent (honest), got %+v", got)
	}
}

// A flat series produces no onset (the MAD floor prevents a divide-by-zero hair trigger).
func TestOnsetFlatSeriesSilent(t *testing.T) {
	s := series(120, func(i int) float64 { return 42 })
	if got := Detect("cei", "m", s, DefaultParams()); len(got) != 0 {
		t.Errorf("a flat series must be silent, got %+v", got)
	}
}

// FLAT baseline + a clean step (the real-cluster shape: cAdvisor reports an identical
// memory value every housekeeping interval, so MAD→0). The onset must FIRE and its reported
// StepZ must be CLAMPED to MaxStepZ, never the 2.6e17 the unclamped ratio produced live.
func TestOnsetFlatBaselineStepClampsMagnitude(t *testing.T) {
	// 30 identical baseline samples (~20MB) then a 250MB step held flat — byte scale.
	s := series(60, func(i int) float64 {
		if i < 30 {
			return 20e6
		}
		return 270e6
	})
	got := Detect("cei", "container_memory_working_set_bytes", s, DefaultParams())
	if len(got) == 0 || got[0].Direction != "up" {
		t.Fatalf("a flat-baseline step must fire as up, got %+v", got)
	}
	if got[0].StepZ != MaxStepZ {
		t.Errorf("flat-baseline StepZ = %v, want clamped to MaxStepZ=%v (not a 1e17 blowup)", got[0].StepZ, MaxStepZ)
	}
}

// A SUSTAINED RAMP (a memory leak — the live failure mode the full test exercised) must
// register onsets, all "up", and the EARLIEST near the ramp's start. A continuous ramp
// re-trips the CUSUM so Detect returns several onsets per series; the surfacing layer keeps
// the latest (onsetLoop) — here we only assert detection + direction + that the first onset
// lands early in the ramp.
func TestOnsetSustainedRampDetectsUpOnsets(t *testing.T) {
	// flat baseline for 30, then a steady ramp of ~+0.6σ/sample for the rest.
	s := series(120, func(i int) float64 {
		v := 100.0
		if i >= 30 {
			v += 1.2 * float64(i-30)
		}
		return v + 0.4*float64((i*7)%3-1) // mild deterministic jitter
	})
	got := Detect("cei", "container_memory_working_set_bytes", s, DefaultParams())
	if len(got) == 0 {
		t.Fatal("a sustained ramp must register at least one onset")
	}
	for _, o := range got {
		if o.Direction != "up" {
			t.Errorf("ramp onset direction = %q, want up", o.Direction)
		}
	}
	firstIdx := int(got[0].At.Sub(base) / (15 * time.Second))
	if firstIdx < 26 || firstIdx > 40 {
		t.Errorf("first onset at index %d, want near the ramp start (~30)", firstIdx)
	}
}

// A sub-threshold step (one that never crosses any bar) is still caught — this is the gap
// the existing primitives leave that onset closes.
func TestOnsetCatchesSubThresholdShift(t *testing.T) {
	// level shift 20 -> 45, both far below any plausible bar; a clean changepoint.
	s := noiseStep(9, 120, 65, 20, 1.5, 25)
	got := Detect("cei", "m", s, DefaultParams())
	if len(got) == 0 || got[0].Direction != "up" {
		t.Fatalf("a sub-threshold up-shift must be detected, got %+v", got)
	}
	if math.Abs(float64(int(got[0].At.Sub(base)/(15*time.Second)))-65) > 6 {
		t.Errorf("sub-threshold onset mislocated: %+v", got[0])
	}
}
