package observe

import (
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/qss"
)

const band = 0.05

// The ladder for an ABOVE bar: bar=100, factor=0.95 (so the raw limit is the
// well-above line ≈105.26), band 5%.
func TestEvalThresholdAboveBar(t *testing.T) {
	bar := 100.0
	wa := WellAboveLine(bar, 0.95, 1.10, "above") // 100/0.95 = 105.263…
	if wa < 105.2 || wa > 105.3 {
		t.Fatalf("well-above line = %v, want ~105.26 (raw limit)", wa)
	}
	cases := []struct {
		value float64
		want  ThresholdState
	}{
		{90, StateBelow},         // < 95
		{94.9, StateBelow},       // just under the band
		{95, StateAtThreshold},   // band edge (100*0.95), inclusive
		{99.9, StateAtThreshold}, // approaching
		{100, StateAbove},        // exactly at the bar = crossed
		{104, StateAbove},        // crossed, below the raw limit (105.263)
		{105.2, StateAbove},      // still a hair under the raw-limit line
		{106, StateWellAbove},    // past the raw limit
		{200, StateWellAbove},
	}
	for _, c := range cases {
		if got := EvalThreshold(c.value, bar, wa, band, "above"); got != c.want {
			t.Errorf("value %v: ladder = %s, want %s", c.value, got, c.want)
		}
	}
}

// The ladder for a BELOW bar (node MemAvailable floor): bar=1000, violation is
// going under it; well-above (deep) line = bar/1.10 ≈ 909.
func TestEvalThresholdBelowBar(t *testing.T) {
	bar := 1000.0
	wa := WellAboveLine(bar, 0.10, 1.10, "below") // 1000/1.10 ≈ 909.09
	cases := []struct {
		value float64
		want  ThresholdState
	}{
		{1200, StateBelow},       // well above the floor = healthy
		{1051, StateBelow},       // just past the band edge → healthy
		{1050, StateAtThreshold}, // band edge bar*(1+band), inclusive
		{1040, StateAtThreshold}, // within 5% above the floor
		{1000, StateAtThreshold}, // at the floor (not yet under)
		{999, StateAbove},        // dropped under the floor = crossed
		{950, StateAbove},
		{909, StateWellAbove}, // at the deep line (1000/1.10 ≈ 909.09, value ≤ line)
		{500, StateWellAbove},
	}
	for _, c := range cases {
		if got := EvalThreshold(c.value, bar, wa, band, "below"); got != c.want {
			t.Errorf("value %v: ladder = %s, want %s", c.value, got, c.want)
		}
	}
}

// A default-sourced bar (factor 0) uses the multiplicative well-above fallback.
func TestWellAboveFallback(t *testing.T) {
	if got := WellAboveLine(0.25, 0, 1.10, "above"); got != 0.275 {
		t.Errorf("default-bar well-above = %v, want 0.275 (1.10×)", got)
	}
}

func at(base time.Time, sec int) time.Time { return base.Add(time.Duration(sec) * time.Second) }

func samples(base time.Time, pairs ...float64) []qss.Sample {
	// pairs: sec0, val0, sec1, val1, ...
	var out []qss.Sample
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, qss.Sample{At: at(base, int(pairs[i])), Value: pairs[i+1]})
	}
	return out
}

var rateBase = time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)

// A clean monotone counter: rate = total increase / elapsed.
func TestEvalRateMonotone(t *testing.T) {
	// 0s:100, 15s:115, 30s:130, 45s:145 → +45 over 45s = 1.0/s.
	s := samples(rateBase, 0, 100, 15, 115, 30, 130, 45, 145)
	r := EvalRate(s, at(rateBase, 45), 5*time.Minute, 15*time.Second)
	if r.Resets != 0 || r.GapBroken {
		t.Fatalf("clean counter shouldn't reset/break: %+v", r)
	}
	if r.WindowDelta != 45 || r.PerSecond != 1.0 {
		t.Errorf("rate = delta %v per-sec %v, want 45 / 1.0", r.WindowDelta, r.PerSecond)
	}
}

// A counter reset (container restart): the drop is NOT summed and NOT extrapolated.
func TestEvalRateCounterReset(t *testing.T) {
	// 0:100, 15:160, 30:5 (RESET), 45:25 → valid increase 60 (0→15) + 20 (30→45) = 80,
	// the −155 drop is dropped. Elapsed = 45s. per-sec = 80/45.
	s := samples(rateBase, 0, 100, 15, 160, 30, 5, 45, 25)
	r := EvalRate(s, at(rateBase, 45), 5*time.Minute, 15*time.Second)
	if r.Resets != 1 {
		t.Fatalf("expected 1 reset, got %+v", r)
	}
	if r.WindowDelta != 80 {
		t.Errorf("window delta = %v, want 80 (reset drop excluded, never extrapolated)", r.WindowDelta)
	}
	wantPS := 80.0 / 45.0
	if r.PerSecond < wantPS-1e-9 || r.PerSecond > wantPS+1e-9 {
		t.Errorf("per-sec = %v, want %v", r.PerSecond, wantPS)
	}
}

// A scrape gap > 2 intervals breaks the window: only the contiguous run AFTER the
// gap is used, and GapBroken is set (A5).
func TestEvalRateScrapeGapBreaksWindow(t *testing.T) {
	// 0:100, 15:110, [gap of 90s > 30s], 105:200, 120:230.
	// Only the post-gap run (105→120: +30 over 15s) counts.
	s := samples(rateBase, 0, 100, 15, 110, 105, 200, 120, 230)
	r := EvalRate(s, at(rateBase, 120), 5*time.Minute, 15*time.Second)
	if !r.GapBroken {
		t.Fatalf("gap should break the window: %+v", r)
	}
	if r.WindowDelta != 30 {
		t.Errorf("window delta = %v, want 30 (only the post-gap run)", r.WindowDelta)
	}
	if r.Elapsed != 15*time.Second {
		t.Errorf("elapsed = %v, want 15s (post-gap run only)", r.Elapsed)
	}
}

// Samples outside the window are excluded; a single in-window sample yields no rate.
func TestEvalRateWindowingAndSparse(t *testing.T) {
	// 5-minute window ending at 600s: the 0s/15s samples are outside.
	s := samples(rateBase, 0, 100, 15, 110, 590, 500, 600, 520)
	r := EvalRate(s, at(rateBase, 600), 5*time.Minute, 15*time.Second)
	if r.Samples != 2 || r.WindowDelta != 20 {
		t.Errorf("windowing wrong: %+v (want 2 samples, delta 20)", r)
	}
	// Single sample → no rate.
	one := samples(rateBase, 600, 520)
	if got := EvalRate(one, at(rateBase, 600), 5*time.Minute, 15*time.Second); got.PerSecond != 0 || got.WindowDelta != 0 {
		t.Errorf("single sample should yield no rate: %+v", got)
	}
}

// Determinism: identical inputs → identical result (and input order from the ring
// is already chronological).
func TestEvalRateDeterministic(t *testing.T) {
	s := samples(rateBase, 0, 100, 15, 160, 30, 5, 45, 25)
	a := EvalRate(s, at(rateBase, 45), 5*time.Minute, 15*time.Second)
	b := EvalRate(s, at(rateBase, 45), 5*time.Minute, 15*time.Second)
	if a != b {
		t.Error("EvalRate not deterministic")
	}
}

func TestEvalCooccurrence(t *testing.T) {
	w := 10 * time.Minute
	if got := EvalCooccurrence([]bool{true, true, true}, w); got.State != CoocAllMet || got.Met != 3 {
		t.Errorf("all true → %+v, want all-met 3/3", got)
	}
	if got := EvalCooccurrence([]bool{true, false, true}, w); got.State != CoocPartial || got.Met != 2 {
		t.Errorf("mixed → %+v, want partial 2/3", got)
	}
	if got := EvalCooccurrence([]bool{false, false}, w); got.State != CoocNone {
		t.Errorf("none → %+v, want none", got)
	}
	// Empty set is unknown, never spuriously all-met.
	if got := EvalCooccurrence(nil, w); got.State != CoocUnknown {
		t.Errorf("empty → %+v, want unknown", got)
	}
}
