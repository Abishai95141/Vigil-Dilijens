package forecast

import (
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/params"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/qss"
)

var decBase = time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)

func samplesFrom(vals []float64) []qss.Sample {
	out := make([]qss.Sample, len(vals))
	for i, v := range vals {
		out[i] = qss.Sample{At: decBase.Add(time.Duration(i) * 15 * time.Second), Value: v}
	}
	return out
}

func decParams() params.ForecastParams {
	return params.ForecastParams{
		Decompose: true, ResetDropFraction: 0.4, MaxExplainedFraction: 0.9, MinContext: 10,
	}
}

// ramp builds a linear ramp of n points from start by step.
func rampVals(start, step float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = start + step*float64(i)
	}
	return out
}

// TestDecomposeSawtoothSplicesLastReset is the headline 09 M5 fixture: a sawtooth
// (ramp→reset→ramp) is spliced at the LAST reset, so the clock sees only the current
// clean ramp — the fix for the 09 M3 sawtooth failure.
func TestDecomposeSawtoothSplicesLastReset(t *testing.T) {
	// two ramps of 40 points each (1..40), with a hard reset to 1 between them.
	vals := append(rampVals(1, 1, 40), rampVals(1, 1, 40)...)
	s := samplesFrom(vals)
	series, rec := Decompose(s, nil, decParams())

	if !rec.Spliced() {
		t.Fatalf("expected a splice on a sawtooth, got record %+v", rec)
	}
	if len(rec.Splices) != 1 || rec.Splices[0].Kind != FootprintReset || rec.Splices[0].AtIndex != 40 {
		t.Fatalf("expected one reset splice at index 40, got %+v", rec.Splices)
	}
	// The remainder is the SECOND ramp (40 points, starting at 1).
	if len(series) != 40 || series[0] != 1 || series[len(series)-1] != 40 {
		t.Fatalf("remainder should be the clean second ramp 1..40, got len=%d first=%v last=%v", len(series), series[0], series[len(series)-1])
	}
}

// TestDecomposeCleanRampIsNoOp: a clean monotonic ramp has no reset → decomposition
// is a NO-OP (the full window passes through unchanged). This is why decomposition
// cannot degrade the plateau corpus's band coverage.
func TestDecomposeCleanRampIsNoOp(t *testing.T) {
	vals := rampVals(100, 0.5, 60)
	s := samplesFrom(vals)
	series, rec := Decompose(s, nil, decParams())
	if rec.Spliced() {
		t.Fatalf("a clean ramp must not be spliced, got %+v", rec.Splices)
	}
	if len(series) != len(vals) {
		t.Fatalf("clean ramp must pass through whole: got %d of %d", len(series), len(vals))
	}
	for i := range vals {
		if series[i] != vals[i] {
			t.Fatalf("clean ramp altered at %d: %v != %v", i, series[i], vals[i])
		}
	}
}

// TestDecomposeOperatorSplice: an operator context-window boundary inside the window
// splices there (a deploy/config baseline change).
func TestDecomposeOperatorSplice(t *testing.T) {
	vals := rampVals(50, 1, 60) // no reset
	s := samplesFrom(vals)
	splice := decBase.Add(time.Duration(30) * 15 * time.Second) // index 30
	series, rec := Decompose(s, []time.Time{splice}, decParams())
	if !rec.Spliced() || rec.Splices[0].Kind != FootprintLevelShift || rec.Splices[0].AtIndex != 30 {
		t.Fatalf("expected an operator level-shift splice at index 30, got %+v", rec.Splices)
	}
	if len(series) != 30 {
		t.Fatalf("remainder should be 30 points after the operator splice, got %d", len(series))
	}
}

// TestDecomposeLatestBoundaryWins: with both a reset and an earlier operator splice,
// the LATEST boundary is used (everything after the most recent event).
func TestDecomposeLatestBoundaryWins(t *testing.T) {
	vals := append(rampVals(1, 1, 20), rampVals(1, 1, 40)...) // reset at index 20
	s := samplesFrom(vals)
	earlyOp := decBase.Add(time.Duration(5) * 15 * time.Second) // index 5, earlier than the reset
	series, rec := Decompose(s, []time.Time{earlyOp}, decParams())
	if rec.Splices[0].AtIndex != 20 || rec.Splices[0].Kind != FootprintReset {
		t.Fatalf("the later reset (idx 20) must win over the earlier op splice (idx 5), got %+v", rec.Splices)
	}
	if len(series) != 40 {
		t.Fatalf("remainder should be the 40-point post-reset ramp, got %d", len(series))
	}
}

// TestDecomposeAbortShortRemainder: if the clean remainder is shorter than MinContext,
// abort (forecasting a just-restarted stream is untrustworthy).
func TestDecomposeAbortShortRemainder(t *testing.T) {
	vals := append(rampVals(1, 1, 50), rampVals(1, 1, 5)...) // reset at 50, only 5 clean points after
	s := samplesFrom(vals)
	series, rec := Decompose(s, nil, decParams()) // MinContext 10
	if !rec.Aborted || series != nil {
		t.Fatalf("expected abort on a 5-point remainder (< MinContext 10), got aborted=%v len=%d", rec.Aborted, len(series))
	}
}

// TestDecomposeAbortTooExplained: if splicing removes more than MaxExplainedFraction
// of the window, abort even when the remainder is long enough.
func TestDecomposeAbortTooExplained(t *testing.T) {
	// 190 pre-reset points + 20 post: remainder 20 ≥ MinContext(10) but explained
	// fraction 190/210 ≈ 0.905 > 0.9 → abort.
	vals := append(rampVals(1, 1, 190), rampVals(1, 1, 20)...)
	s := samplesFrom(vals)
	_, rec := Decompose(s, nil, decParams())
	if !rec.Aborted || rec.AbortReason == "" {
		t.Fatalf("expected abort on >90%% explained, got %+v", rec)
	}
}

// TestDecomposeTransientDipNoSplice is THE exit-gate regression: a clean ramp with a
// single GC/cache dip that RECOVERS next step must NOT be spliced (a transient dip is
// not a restart). Without the persistence check this falsely truncated a clean series
// and degraded band coverage — the gate-forbidden failure the adversarial review caught.
func TestDecomposeTransientDipNoSplice(t *testing.T) {
	vals := rampVals(100, 1, 60)
	vals[40] = vals[39] * 0.5 // a 50% single-step dip that recovers (index 41 back on the ramp)
	series, rec := Decompose(samplesFrom(vals), nil, decParams())
	if rec.Spliced() {
		t.Fatalf("a transient dip-and-recover on a clean ramp must NOT splice, got %+v", rec.Splices)
	}
	if len(series) != len(vals) {
		t.Fatalf("clean ramp with a transient dip must pass through whole, got %d of %d", len(series), len(vals))
	}
}

// TestDecomposeFastRampingRestartDetected pins the live-discovered calibration: a
// container that restarts and then ramps FAST (a leaker reaching a fraction of its
// former peak within a minute) is still a RESET — its post-restart climb exceeds
// (1−frac)×prev quickly but stays below the pre-drop level, so it must be spliced.
func TestDecomposeFastRampingRestartDetected(t *testing.T) {
	// peak ramp to 96, OOM, then a fast post-restart ramp that climbs back toward 95.
	vals := rampVals(1, 1, 96)
	vals = append(vals, 10, 25, 40, 55, 64, 70, 76, 80, 84, 88, 92, 95)
	_, rec := Decompose(samplesFrom(vals), nil, decParams())
	if !rec.Spliced() || rec.Splices[0].Kind != FootprintReset || rec.Splices[0].AtIndex != 96 {
		t.Fatalf("a fast-ramping restart (96→10→fast-climb) must be spliced at the reset, got %+v", rec.Splices)
	}
}

// TestDecomposeOscillatingNoSplice: a healthy oscillating gauge (cache churn — rises,
// dips, recovers, repeats) must NOT splice (every dip recovers).
func TestDecomposeOscillatingNoSplice(t *testing.T) {
	var vals []float64
	for cycle := 0; cycle < 5; cycle++ {
		base := 100.0 + float64(cycle)*10
		for k := 0; k < 10; k++ {
			vals = append(vals, base+float64(k))
		}
		vals = append(vals, base*0.5) // a 50% dip that recovers next cycle
	}
	series, rec := Decompose(samplesFrom(vals), nil, decParams())
	if rec.Spliced() {
		t.Fatalf("an oscillating-but-recovering gauge must NOT splice, got %+v", rec.Splices)
	}
	if len(series) != len(vals) {
		t.Fatalf("oscillating gauge must pass through whole, got %d of %d", len(series), len(vals))
	}
}

// TestDecomposeNearZeroNoiseNoSplice: a small-baseline wobble must NOT trip a reset
// (the magnitude floor relative to the series range — fixes the near-zero defect).
func TestDecomposeNearZeroNoiseNoSplice(t *testing.T) {
	vals := []float64{0.5, 0.6, 0.5, 0.2, 0.5, 0.6}
	vals = append(vals, rampVals(1, 2.5, 56)...) // then a big ramp to ~140
	series, rec := Decompose(samplesFrom(vals), nil, decParams())
	if rec.Spliced() {
		t.Fatalf("a sub-unit wobble on a small baseline must NOT splice (tiny vs the series range), got %+v", rec.Splices)
	}
	if len(series) != len(vals) {
		t.Fatalf("near-zero-noise series must pass through whole")
	}
}

// TestDecomposeCoincidentSpliceSingleRecord: a reset and an operator window on the
// SAME index produce ONE splice record (a deploy that caused the restart), not two.
func TestDecomposeCoincidentSpliceSingleRecord(t *testing.T) {
	vals := append(rampVals(1, 1, 20), rampVals(1, 1, 40)...) // reset at index 20
	s := samplesFrom(vals)
	op := s[20].At // operator window exactly at the reset boundary
	_, rec := Decompose(s, []time.Time{op}, decParams())
	if len(rec.Splices) != 1 || rec.Splices[0].Kind != FootprintReset {
		t.Fatalf("coincident reset+operator boundary must record ONE reset splice, got %+v", rec.Splices)
	}
}

// TestDecomposeDisabledIsNoOp: with Decompose=false the window passes through.
func TestDecomposeDisabledIsNoOp(t *testing.T) {
	vals := append(rampVals(1, 1, 40), rampVals(1, 1, 40)...)
	p := decParams()
	p.Decompose = false
	series, rec := Decompose(samplesFrom(vals), nil, p)
	if rec.Spliced() || len(series) != len(vals) {
		t.Fatalf("disabled decomposition must be a no-op, got spliced=%v len=%d", rec.Spliced(), len(series))
	}
}

// TestDecomposeDeterministic: pure function — same inputs, identical output.
func TestDecomposeDeterministic(t *testing.T) {
	vals := append(rampVals(1, 1, 30), rampVals(1, 1, 30)...)
	s := samplesFrom(vals)
	a, ra := Decompose(s, nil, decParams())
	b, rb := Decompose(s, nil, decParams())
	if len(a) != len(b) || ra.Splices[0].AtIndex != rb.Splices[0].AtIndex {
		t.Fatal("decomposition is not deterministic")
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("non-deterministic value at %d", i)
		}
	}
}
