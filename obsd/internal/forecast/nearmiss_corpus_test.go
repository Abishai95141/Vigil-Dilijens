package forecast

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/clock"
)

// The forecast NEAR-MISS / recede decoy corpus (the FALSE-POSITIVE boundary of the
// early-warning DECISION layer). The live forecast corpus only ever ends in a REAL
// crossing, so the gate's "0 false warnings" was VACUOUS — there was no workload that
// climbs toward its bar then plateaus/recedes WITHOUT crossing, so the recede defence in
// forecast.Project (SilenceNoCrossing / SilenceBandTooWide) was never driven adversarially.
//
// This corpus folds the REAL forecast.Project over scripted forecast trajectories (NOT a
// reimplementation) and freezes its candidate-or-silence verdict byte-identically, graded
// against an INDEPENDENT label oracle. It certifies OUR deterministic decision code — IF
// the clock forecasts a non-crossing, the decision layer stays silent — and explicitly NOT
// TimesFM's raw skill at forecasting a plateau (that is a separate, model-bearing,
// on-demand concern; see corpus/labels/forecast-fp-gate.md and harness.forecast_fp_gate).
//
// Project is a PURE function of (target, forecast, bar, params), so the frozen corpus
// reproducing byte-identically is the off-digest determinism proof.
//
// Regenerate: REGEN_NEARMISS_CORPUS=1 go test ./obsd/internal/forecast -run RegenNearmiss

const nearmissCorpusDir = "../../../corpus/forecast-nearmiss"

// nmResult is the frozen producer output per scenario: the exact (emitted, silence reason,
// candidate) verdict Project returned. The silence reason is frozen (not just emitted=false)
// so a regression that silences for the WRONG reason (e.g. no-crossing where band-too-wide
// was intended) is caught.
type nmResult struct {
	Emitted   bool       `json:"emitted"`
	Silence   string     `json:"silence"`
	Candidate *Candidate `json:"candidate"`
}

// nmParams records the decision-boundary params in the label so the frozen verdict can
// never be silently re-interpreted under drifted params.
type nmParams struct {
	MaxBandRatio float64   `json:"maxBandRatio"`
	HorizonSteps int       `json:"horizonSteps"`
	Quantiles    []float64 `json:"quantiles"`
}

type nmLabel struct {
	Bundle        string   `json:"bundle"`
	Scenario      string   `json:"scenario"`
	Kind          string   `json:"kind"` // recede | plateau | seductive | too-wide | crossing | robustness | nonmonotone
	ExpectWarning bool     `json:"expectWarning"`
	ExpectSilence string   `json:"expectSilence"` // exact Silence* constant when not warning
	Params        nmParams `json:"params"`
	Note          string   `json:"note"`
}

type nmScenario struct {
	lbl    nmLabel
	target Target
	fc     *clock.Forecast
}

// pointForecast builds a forecast from an explicit point trajectory with band point±width.
func pointForecast(pts []float64, width float64) *clock.Forecast {
	h := len(pts)
	fc := &clock.Forecast{Point: append([]float64{}, pts...), Quantiles: make([][]float64, 3)}
	for i := range fc.Quantiles {
		fc.Quantiles[i] = make([]float64, h)
	}
	for s, v := range pts {
		fc.Quantiles[0][s] = v - width
		fc.Quantiles[1][s] = v
		fc.Quantiles[2][s] = v + width
	}
	return fc
}

// risefall: point climbs from lo to peak by riseSteps, then recedes back toward lo.
func risefall(h int, lo, peak float64, riseSteps int) []float64 {
	pts := make([]float64, h)
	for s := 0; s < h; s++ {
		if s <= riseSteps {
			pts[s] = lo + (peak-lo)*float64(s)/float64(riseSteps)
		} else {
			pts[s] = peak - (peak-lo)*float64(s-riseSteps)/float64(h-1-riseSteps)
		}
	}
	return pts
}

// fallrise: point drops from hi to trough by fallSteps, then rises back toward hi (the
// below-bar mirror of risefall).
func fallrise(h int, hi, trough float64, fallSteps int) []float64 {
	pts := make([]float64, h)
	for s := 0; s < h; s++ {
		if s <= fallSteps {
			pts[s] = hi - (hi-trough)*float64(s)/float64(fallSteps)
		} else {
			pts[s] = trough + (hi-trough)*float64(s-fallSteps)/float64(h-1-fallSteps)
		}
	}
	return pts
}

func nearmissScenarios() []nmScenario {
	above := wsTarget() // bar 486, direction above
	below := wsTarget()
	below.Direction = "below"
	below.BarValue = 100

	p := fp() // HorizonSteps 64, Quantiles {0.1,0.5,0.9}, MaxBandRatio 0.5
	pr := nmParams{MaxBandRatio: p.MaxBandRatio, HorizonSteps: p.HorizonSteps, Quantiles: p.Quantiles}

	nan := make([]float64, 64)
	for i := range nan {
		nan[i] = math.NaN()
	}

	dip := make([]float64, 64)
	for s := 0; s < 64; s++ {
		if s <= 4 {
			dip[s] = 484 - 4*float64(s)/4 // dips 484 → 480
		} else {
			dip[s] = 480 + (540-480)*float64(s-4)/float64(63-4) // rises 480 → 540 (crosses 486 ~step 10)
		}
	}

	return []nmScenario{
		{
			lbl: nmLabel{Scenario: "recede-above", Kind: "recede", ExpectWarning: false, ExpectSilence: SilenceNoCrossing, Params: pr,
				Note: "point climbs from 470 toward the 486 bar (peak ~485) then RECEDES back to 470 — never crosses. The decision layer MUST stay silent (the false-warning the live corpus can never produce)."},
			target: above, fc: pointForecast(risefall(64, 470, 485, 24), 8),
		},
		{
			lbl: nmLabel{Scenario: "plateau-below", Kind: "plateau", ExpectWarning: false, ExpectSilence: SilenceNoCrossing, Params: pr,
				Note: "point PLATEAUS at 480 (band [477,483]) for the whole horizon, below the 486 bar → SilenceNoCrossing. A maxed-but-not-crossing series is not a forecast warning."},
			target: above, fc: scriptedForecast(64, 480, 0, 3),
		},
		{
			lbl: nmLabel{Scenario: "seductive-wide-upper", Kind: "seductive", ExpectWarning: false, ExpectSilence: SilenceNoCrossing, Params: pr,
				Note: "THE SEDUCTIVE FP: point flat at 480 (below 486) but the q90 UPPER edge pokes to 510 (above the bar). Project gates on the POINT trajectory (project.go:95), so it MUST silence — a wide band merely touching the bar is never a warning."},
			target: above, fc: scriptedForecast(64, 480, 0, 30),
		},
		{
			lbl: nmLabel{Scenario: "band-too-wide", Kind: "too-wide", ExpectWarning: false, ExpectSilence: SilenceBandTooWide, Params: pr,
				Note: "point GENUINELY crosses (~step 40) but the actionable near cone earliest→point spans >0.5×horizon ('could cross anytime') → SilenceBandTooWide. The point crosses so the width guard is actually reached."},
			target: above, fc: scriptedForecast(64, 480, 0.15, 10),
		},
		{
			lbl: nmLabel{Scenario: "imminent-crossing-above", Kind: "crossing", ExpectWarning: true, Params: pr,
				Note: "CONTROL (recall): point 480 +5/step crosses 486 at step 1, tight band → a real PROJECTED warning must EMIT. Recall is not collateral damage of the FP defence."},
			target: above, fc: scriptedForecast(64, 480, 5, 20),
		},
		{
			lbl: nmLabel{Scenario: "open-tail-crossing", Kind: "crossing", ExpectWarning: true, Params: pr,
				Note: "CONTROL: imminent crossing with an OPEN far tail (lower edge never crosses) → EMIT with LatestBeyondHorizon stated; the open tail must not silence a tight imminent crossing (the imminence-bias regression)."},
			target: above, fc: scriptedForecast(64, 480, 1, 200),
		},
		{
			lbl: nmLabel{Scenario: "recede-below", Kind: "recede", ExpectWarning: false, ExpectSilence: SilenceNoCrossing, Params: pr,
				Note: "BELOW-bar mirror: point drops from 130 toward the 100 bar (trough ~106) then rises back — never crosses below → SilenceNoCrossing."},
			target: below, fc: pointForecast(fallrise(64, 130, 106, 24), 6),
		},
		{
			lbl: nmLabel{Scenario: "crossing-below", Kind: "crossing", ExpectWarning: true, Params: pr,
				Note: "CONTROL (below): point 130 −5/step crosses the 100 bar from above → a 'below' PROJECTED warning must EMIT."},
			target: below, fc: scriptedForecast(64, 130, -5, 10),
		},
		{
			lbl: nmLabel{Scenario: "nan-point", Kind: "robustness", ExpectWarning: false, ExpectSilence: SilenceNoCrossing, Params: pr,
				Note: "ROBUSTNESS: a forecast of all-NaN points (NaN ≥ bar is false) must produce a DEFINED silence (SilenceNoCrossing), never a panic — the model-adjacent ill-formed-value guard."},
			target: above, fc: pointForecast(nan, 5),
		},
		{
			lbl: nmLabel{Scenario: "dip-then-cross", Kind: "nonmonotone", ExpectWarning: true, Params: pr,
				Note: "NON-MONOTONE: point dips (480→472) then rises through the 486 bar to 500. crossIndex returns the FIRST crossing, so it EMITS — a dip before a real crossing does not suppress the warning."},
			target: above, fc: pointForecast(dip, 15),
		},
	}
}

func runProject(sc nmScenario) nmResult {
	cand, reason := Project(sc.target, sc.fc, t0, t0, 15*time.Second, 240, "sha256:test", fp())
	return nmResult{Emitted: cand != nil, Silence: reason, Candidate: cand}
}

func marshalResult(r nmResult) []byte {
	b, _ := json.MarshalIndent(r, "", "  ")
	return append(b, '\n')
}

func TestRegenNearmissCorpus(t *testing.T) {
	if os.Getenv("REGEN_NEARMISS_CORPUS") != "1" {
		t.Skip("set REGEN_NEARMISS_CORPUS=1 to regenerate corpus/forecast-nearmiss")
	}
	if err := os.MkdirAll(nearmissCorpusDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, sc := range nearmissScenarios() {
		if err := os.WriteFile(filepath.Join(nearmissCorpusDir, "result-"+sc.lbl.Scenario+".json"), marshalResult(runProject(sc)), 0o644); err != nil {
			t.Fatal(err)
		}
		lbl := sc.lbl
		lbl.Bundle = "forecast-nearmiss-" + lbl.Scenario
		b, _ := json.MarshalIndent(lbl, "", "  ")
		if err := os.WriteFile(filepath.Join(nearmissCorpusDir, "label-"+sc.lbl.Scenario+".json"), append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("regenerated %d near-miss scenarios into %s", len(nearmissScenarios()), nearmissCorpusDir)
}

// TestNearmissCorpusFrozenConsistent is the always-on determinism drift guard: Project's
// real output must reproduce the frozen corpus byte-identically.
func TestNearmissCorpusFrozenConsistent(t *testing.T) {
	for _, sc := range nearmissScenarios() {
		got := marshalResult(runProject(sc))
		want, err := os.ReadFile(filepath.Join(nearmissCorpusDir, "result-"+sc.lbl.Scenario+".json"))
		if err != nil {
			t.Fatalf("frozen corpus missing for %s: %v (REGEN_NEARMISS_CORPUS=1 to create)", sc.lbl.Scenario, err)
		}
		if string(got) != string(want) {
			t.Errorf("near-miss corpus drift for %s — Project output differs from frozen (REGEN to update)", sc.lbl.Scenario)
		}
	}
}

// TestNearmissOracleHoldsAgainstProject is the INDEPENDENT-oracle teeth: the human label
// (expectWarning / expectSilence) must agree with the REAL Project verdict. Code and
// assertion can genuinely disagree — the property a vacuous gate lacks. A regression that
// weakened the recede defence (e.g. gating on the upper quantile instead of the point)
// flips seductive-wide-upper to emit and FAILS here.
func TestNearmissOracleHoldsAgainstProject(t *testing.T) {
	for _, sc := range nearmissScenarios() {
		r := runProject(sc)
		if r.Emitted != sc.lbl.ExpectWarning {
			t.Errorf("%s: emitted=%v but oracle expectWarning=%v (silence=%q)", sc.lbl.Scenario, r.Emitted, sc.lbl.ExpectWarning, r.Silence)
			continue
		}
		if !sc.lbl.ExpectWarning {
			if r.Silence != sc.lbl.ExpectSilence {
				t.Errorf("%s: silenced with %q but oracle expected %q (silence for the WRONG reason)", sc.lbl.Scenario, r.Silence, sc.lbl.ExpectSilence)
			}
			continue
		}
		// emit controls: the mandatory PROJECTED marks must ride every candidate.
		if r.Candidate == nil || r.Candidate.Class != ClassProjected || !r.Candidate.IsProjection {
			t.Errorf("%s: an emitted candidate must carry Class=PROJECTED + IsProjection=true", sc.lbl.Scenario)
		}
	}
}
