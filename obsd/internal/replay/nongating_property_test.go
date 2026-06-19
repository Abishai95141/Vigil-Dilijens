package replay

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/clock"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/flow"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/params"
)

// CHARTER ABSOLUTE — non-gating byte-identity (CLAUDE.md "Non-gating (absolute)":
// "detection never waits on forecasting. The deterministic path produces
// IDENTICAL results whether the clock is present, degraded, or absent.").
//
// This is the single most important missing invariant test in the package. The
// detection digest is computed in evalTick from (fingerprints, findings,
// cascades, unexplained) ONLY — the forecast/projected-cross-service/cross-
// service/incident lanes run beside it and feed sinks, never the digest. The
// charter says that separation must hold OBSERVABLY: run the same bundle through
// detection with the forecast lane ON vs OFF and the detection output must be
// byte-identical, tick for tick. If a future change ever let a PROJECTED datum
// (or any warm-path lane) leak into a fingerprint, finding, cascade, or
// unexplained card, this test fails — which is exactly when it must.
//
// It is not vacuous: the forecast lane here is REAL work (a scripted clock that
// actually gets invoked, plus the projected/cross-service/incident passes all
// firing), proven by the call counter and event counter below. A run that
// silently no-op'd the forecast lane would still pass byte-identity but would
// NOT prove non-gating — so we assert the lane genuinely ran.

// countingClock is a deterministic injected ClockCaller that returns a valid
// rising forecast and counts its invocations. The forecast it returns is
// monotone and band-widening (a real, charter-clean projection), so the
// forecast lane does genuine work — but NONE of it may touch the digest.
type countingClock struct {
	calls int
}

func (c *countingClock) Forecast(_ context.Context, series []float64, h int, q []float64) (*clock.Forecast, error) {
	c.calls++
	last := 0.0
	if len(series) > 0 {
		last = series[len(series)-1]
	}
	point := make([]float64, h)
	bands := make([][]float64, len(q))
	for i := range bands {
		bands[i] = make([]float64, h)
	}
	for step := 0; step < h; step++ {
		// Keep rising past the basis so a crossing can project; a widening band
		// per step so it never collapses to a line (the charter band rule).
		point[step] = last + float64(step+1)*float64(8<<20)
		for i, level := range q {
			// level in [0,1]; spread the quantiles symmetrically and widen with
			// the step so earliest<point<latest with a non-degenerate band.
			spread := (level - 0.5) * float64(step+1) * float64(4<<20)
			bands[i][step] = point[step] + spread
		}
	}
	return &clock.Forecast{Point: point, Quantiles: bands}, nil
}

// nonGatingForecastParams is an explicit, plausible evaluation regime that makes
// the forecast lane actually invoke the clock against the fixture's rising leak
// gauge. min-context 2 (the minimum the params validator allows) so the clock
// fires on the early ticks before the leak crosses its bar (the later ticks
// legitimately silence as already-crossed — real funnel work either way). Off the
// manifest by design (the forecast regime is never implied — doc 11 §3.1).
func nonGatingForecastParams() params.ForecastParams {
	return params.ForecastParams{
		Enabled:         true,
		HorizonSteps:    8,
		Quantiles:       []float64{0.1, 0.5, 0.9},
		MinContext:      2,
		FlatEpsilon:     0.0, // never silence as flat (the leak is a real ramp)
		MaxBandRatio:    1e9, // never silence on band width (we want the lane to run)
		BandCalibration: 1.0, // identity (raw clock band)
	}
}

func TestNonGatingForecastLaneIsByteIdentical(t *testing.T) {
	g := loadGraph(t)
	dir := t.TempDir()
	recs := buildBundle(t, dir, g, -1)

	// The bundle must carry real findings, or "identical detection output" would
	// be a trivially-empty claim.
	fired := 0
	for _, r := range recs {
		fired += r.Findings
	}
	if fired == 0 {
		t.Fatal("the bundle must carry real findings, or non-gating proves nothing")
	}

	rel, err := flow.LoadRelation("../../../ontology/graph/overlays/experimental/flow-relation-v0.yaml")
	if err != nil {
		t.Fatalf("LoadRelation: %v", err)
	}

	// BASELINE: forecast lane OFF. Detection digests + canonical tick bytes here
	// are the reference the ON run must reproduce.
	offDir := t.TempDir()
	off, err := Run(Options{BundleDir: dir, Graph: g, OutDir: offDir})
	if err != nil {
		t.Fatalf("forecast-off Run: %v", err)
	}
	if off.Mismatches != 0 {
		t.Fatalf("baseline (forecast off) did not replay clean: %d mismatches", off.Mismatches)
	}

	// FORECAST LANE ON: the same bundle, same graph, but now the forecast cycle,
	// the projected-cross-service pass, the cross-service pass, AND the incident
	// pass all run beside detection. None of them may move a single byte of the
	// detection digest.
	cc := &countingClock{}
	var forecastTicks, projTicks, csTicks, incTicks int
	var clockInvocations int
	onDir := t.TempDir()
	on, err := Run(Options{
		BundleDir: dir, Graph: g, OutDir: onDir,
		Forecast: &ForecastEval{
			Clock:  cc,
			Params: nonGatingForecastParams(),
			Budget: 8,
			Events: func(tf TickForecast) {
				forecastTicks++
				clockInvocations += len(tf.Traces)
			},
		},
		ProjectedCrossService: &ProjectedCrossServiceEval{
			Relation: rel,
			Events:   func(TickProjectedCrossService) { projTicks++ },
		},
		CrossService: &CrossServiceEval{
			Relation: rel,
			Events:   func(TickCrossService) { csTicks++ },
		},
		Incident: &IncidentEval{
			ResolveGap: 45 * time.Second,
			Bucket:     7 * 24 * time.Hour,
			Events:     func(TickIncident) { incTicks++ },
		},
	})
	if err != nil {
		t.Fatalf("forecast-on Run: %v", err)
	}

	// The lane must have actually run — otherwise byte-identity is a no-op and the
	// non-gating claim is unproven. The clock must have been invoked at least once
	// (the fixture's rising leak gauge IS forecast-eligible) and every tick must
	// have driven the forecast/projected/cross-service/incident sinks.
	if cc.calls == 0 || clockInvocations == 0 {
		t.Fatalf("the forecast lane never invoked the clock — non-gating would be unproven (calls=%d traces=%d)", cc.calls, clockInvocations)
	}
	nTicks := len(off.Ticks)
	if forecastTicks != nTicks || projTicks != nTicks || csTicks != nTicks || incTicks != nTicks {
		t.Fatalf("warm-path lanes did not run on every tick: forecast=%d proj=%d cs=%d inc=%d (want %d each)",
			forecastTicks, projTicks, csTicks, incTicks, nTicks)
	}

	// (1) Per-tick detection digest byte-identity. The forecast lane being on must
	// not perturb a single detection digest, and the run must still verify clean
	// against the recorded (forecast-free) digests.
	if on.Mismatches != 0 {
		t.Fatalf("forecast lane ON broke verification: %d mismatches — detection is GATING on the clock", on.Mismatches)
	}
	if len(on.Ticks) != len(off.Ticks) {
		t.Fatalf("tick count changed with the forecast lane on: off=%d on=%d", len(off.Ticks), len(on.Ticks))
	}
	for i := range off.Ticks {
		if on.Ticks[i].ReplayedDigest != off.Ticks[i].ReplayedDigest {
			t.Fatalf("tick %d: the forecast lane changed the detection digest — NON-GATING BROKEN\n off=%s\n on =%s",
				i, off.Ticks[i].ReplayedDigest, on.Ticks[i].ReplayedDigest)
		}
		// The recorded digest itself was captured forecast-free; a clean match here
		// re-confirms detection depends on neither the recorded NOR the live clock.
		if !on.Ticks[i].Match {
			t.Fatalf("tick %d did not match its recorded digest with the forecast lane on", i)
		}
	}

	// (2) Canonical tick-output bytes byte-identical. The OutDir tick-*.json files
	// ARE the surfaced detection output; comparing the raw bytes catches any
	// forecast bleed-through the digest comparison could (in principle) alias.
	offFiles, _ := filepath.Glob(filepath.Join(offDir, "tick-*.json"))
	onFiles, _ := filepath.Glob(filepath.Join(onDir, "tick-*.json"))
	sort.Strings(offFiles)
	sort.Strings(onFiles)
	if len(offFiles) == 0 || len(offFiles) != len(onFiles) {
		t.Fatalf("canonical tick output count mismatch: off=%d on=%d", len(offFiles), len(onFiles))
	}
	for i := range offFiles {
		if filepath.Base(offFiles[i]) != filepath.Base(onFiles[i]) {
			t.Fatalf("tick output filenames diverged: %s vs %s", offFiles[i], onFiles[i])
		}
		offBytes, err := os.ReadFile(offFiles[i])
		if err != nil {
			t.Fatal(err)
		}
		onBytes, err := os.ReadFile(onFiles[i])
		if err != nil {
			t.Fatal(err)
		}
		if string(offBytes) != string(onBytes) {
			t.Fatalf("tick output %s differs with the forecast lane on — detection output is NOT forecast-independent", filepath.Base(offFiles[i]))
		}
	}
}

// A DEGRADED clock (every call errors) is the charter's "degraded" arm: detection
// must still produce byte-identical output. A forecast lane whose model is down
// can NEVER change a single detection byte — that is the absolute non-gating rule
// under failure, not just under absence.
func TestNonGatingForecastLaneDegradedIsByteIdentical(t *testing.T) {
	g := loadGraph(t)
	dir := t.TempDir()
	buildBundle(t, dir, g, -1)

	off, err := Run(Options{BundleDir: dir, Graph: g})
	if err != nil {
		t.Fatalf("forecast-off Run: %v", err)
	}
	if off.Mismatches != 0 {
		t.Fatalf("baseline did not replay clean: %d mismatches", off.Mismatches)
	}

	var degradedSeen bool
	on, err := Run(Options{
		BundleDir: dir, Graph: g,
		Forecast: &ForecastEval{
			Clock:  errClock{},
			Params: nonGatingForecastParams(),
			Budget: 8,
			Events: func(tf TickForecast) {
				if tf.Degraded {
					degradedSeen = true
				}
			},
		},
	})
	if err != nil {
		t.Fatalf("forecast-degraded Run: %v", err)
	}
	if !degradedSeen {
		t.Fatal("the degraded clock must mark at least one cycle Degraded — otherwise the degraded arm was never exercised")
	}
	if on.Mismatches != 0 {
		t.Fatalf("a DEGRADED forecast lane broke verification: %d mismatches — detection is gating on clock health", on.Mismatches)
	}
	if len(on.Ticks) != len(off.Ticks) {
		t.Fatalf("tick count changed under a degraded clock: off=%d on=%d", len(off.Ticks), len(on.Ticks))
	}
	for i := range off.Ticks {
		if on.Ticks[i].ReplayedDigest != off.Ticks[i].ReplayedDigest {
			t.Fatalf("tick %d: a DEGRADED clock changed the detection digest — NON-GATING BROKEN UNDER FAILURE\n off=%s\n on =%s",
				i, off.Ticks[i].ReplayedDigest, on.Ticks[i].ReplayedDigest)
		}
	}
}

// errClock is the degraded arm: every forecast call fails.
type errClock struct{}

func (errClock) Forecast(_ context.Context, _ []float64, _ int, _ []float64) (*clock.Forecast, error) {
	return nil, context.DeadlineExceeded
}
