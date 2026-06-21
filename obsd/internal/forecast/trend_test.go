package forecast

import (
	"math"
	"math/rand"
	"reflect"
	"testing"
)

// flatCV mirrors the runner's flat() so the tests can assert the FULL gate decision
// (silence ⟺ flat AND NOT trend) on the same series the runner would see.
func flatCV(series []float64, eps float64) bool { return flat(series, eps) }

// gateSilences replicates the runner gate: silence as flat only if low-variance AND no
// recent sustained onset.
func gateSilences(series []float64, eps float64, p TrendParams) bool {
	return flatCV(series, eps) && !Trend(series, p).Tripped
}

const eps = 0.005 // the default flat_epsilon

// THE HEADLINE: a series flat for 60 points then creeping up in the last few is silenced
// by flat() alone (low CV) but MUST be rescued by the trend detector — the late-onset leak.
func TestTrendRescuesLateOnsetCreep(t *testing.T) {
	p := DefaultTrendParams()
	cases := []struct {
		name  string
		creep []float64
	}{
		{"very-early +0.9%", []float64{100.05, 100.15, 100.4, 100.9}},
		{"early +2%", []float64{100.2, 100.5, 101.0, 102.0}},
		{"gentle exponential +2.6%", []float64{100.2, 100.6, 101.3, 102.6}},
	}
	for _, c := range cases {
		series := append(flatRun(60, 100), c.creep...)
		if !flatCV(series, eps) {
			// these are deliberately low-CV; if this ever fails the fixture drifted
			t.Fatalf("%s: fixture not low-CV (CV=%.4f) — pick a gentler creep", c.name, cv(series))
		}
		tr := Trend(series, p)
		if !tr.Tripped || tr.Direction != "up" {
			t.Errorf("%s: trend must rescue an up-creep, got %+v", c.name, tr)
		}
		if gateSilences(series, eps, p) {
			t.Errorf("%s: the gate FALSELY SILENCED a real late-onset leak (the bug)", c.name)
		}
	}
}

// The dual: a NOISY but trendless series must NOT be newly forecast by the rescue. (Its CV
// is actually high enough that flat() doesn't silence it anyway — but the trend detector
// must not TRIP on pure noise, or it would flood the forecast budget with false dynamics.)
func TestTrendSilentOnNoiseBattery(t *testing.T) {
	p := DefaultTrendParams()
	const trials = 400
	trips := 0
	for s := 0; s < trials; s++ {
		rng := rand.New(rand.NewSource(int64(7000 + s)))
		series := make([]float64, 96)
		for i := range series {
			series[i] = 100 + rng.NormFloat64()*2 // stationary noise, no trend
		}
		if Trend(series, p).Tripped {
			trips++
		}
	}
	rate := float64(trips) / trials
	t.Logf("false-trip rate on stationary noise = %d/%d = %.3f", trips, trials, rate)
	if rate >= 0.02 {
		t.Errorf("trend false-trips on noise at %.3f (want < 0.02) — would flood the forecast budget", rate)
	}
}

// A truly flat series is still silenced (the rescue does not over-fire).
func TestTrendSilentOnFlat(t *testing.T) {
	p := DefaultTrendParams()
	series := flatRun(96, 42)
	if Trend(series, p).Tripped {
		t.Error("a perfectly flat series must not trip the trend rescue")
	}
	if !gateSilences(series, eps, p) {
		t.Error("a flat series must stay silenced")
	}
}

// A late-onset DOWNWARD creep (a series draining toward a 'below' bar) is rescued as down.
func TestTrendRescuesDownCreep(t *testing.T) {
	series := append(flatRun(60, 100), 99.8, 99.5, 99.0, 98.0)
	tr := Trend(series, DefaultTrendParams())
	if !tr.Tripped || tr.Direction != "down" {
		t.Errorf("a down-creep must be rescued as down, got %+v", tr)
	}
}

// An OLD step that has since PLATEAUED is NOT "in progress" — it is the regime-shift
// detector's jurisdiction, not the trend rescue's. RecentWindow must exclude it.
func TestTrendIgnoresOldPlateau(t *testing.T) {
	p := DefaultTrendParams()
	p.RecentWindow = 20
	// step up early (index ~14), then a long flat plateau at the new level to the end.
	series := append(flatRun(14, 100), flatRun(82, 130)...)
	if Trend(series, p).Tripped {
		t.Error("an old step that plateaued long ago must not count as an in-progress drift")
	}
}

// The NewSlope count drives the early-onset confidence flag; it must reflect the points
// since the drift began.
func TestTrendNewSlopeLength(t *testing.T) {
	series := append(flatRun(60, 100), 100.4, 101.2, 102.8, 106.0) // 4 creep points
	tr := Trend(series, DefaultTrendParams())
	if !tr.Tripped {
		t.Fatal("expected a trip")
	}
	if tr.NewSlope < 3 || tr.NewSlope > 8 {
		t.Errorf("new-slope length = %d, want ~4 (the creep length, allowing backtrack)", tr.NewSlope)
	}
}

// Disabled (H<=0) ⇒ never trips ⇒ the gate is byte-identical to flat()-only.
func TestTrendDisabledIsByteIdenticalGate(t *testing.T) {
	off := DefaultTrendParams()
	off.H = 0
	series := append(flatRun(60, 100), 100.2, 100.5, 101.0, 102.0) // the rescued case
	if Trend(series, off).Tripped {
		t.Error("H<=0 must disable the rescue")
	}
	// with the rescue off, the gate decision == flat() alone
	if gateSilences(series, eps, off) != flatCV(series, eps) {
		t.Error("disabled rescue must make the gate identical to flat()-only")
	}
}

func TestTrendDeterministic(t *testing.T) {
	series := append(flatRun(60, 100), 100.3, 100.9, 102.1, 104.5)
	a := Trend(series, DefaultTrendParams())
	b := Trend(series, DefaultTrendParams())
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("trend not deterministic:\n%+v\n%+v", a, b)
	}
}

// --- helpers ---------------------------------------------------------------

func flatRun(n int, v float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func cv(series []float64) float64 {
	tail := series
	if len(tail) > 64 {
		tail = tail[len(tail)-64:]
	}
	var m float64
	for _, v := range tail {
		m += v
	}
	m /= float64(len(tail))
	var vs float64
	for _, v := range tail {
		vs += (v - m) * (v - m)
	}
	return math.Sqrt(vs/float64(len(tail))) / math.Abs(tail[len(tail)-1])
}
