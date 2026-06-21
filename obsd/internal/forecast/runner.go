package forecast

import (
	"context"
	"math"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/clock"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/params"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/qss"
)

// ClockCaller is the model boundary the runner speaks through — satisfied by
// *clock.Client; tests script it. Bare floats in, trajectories out.
type ClockCaller interface {
	Forecast(ctx context.Context, series []float64, horizon int, quantiles []float64) (*clock.Forecast, error)
}

// StreamReader is the slice of the observation read side the runner needs
// (the live Ingestor and the replay bundleReader both satisfy observe's
// superset of it).
type StreamReader interface {
	StreamsFor(uid, metric string) []string
	LastN(streamID string, n int) []qss.Sample
	StreamType(streamID string) (string, bool)
}

// CycleInput is one forecast cycle's frozen inputs. Targets arrive ALREADY
// budgeted (selection owns the ceiling, doc 06 M5); the runner enforces
// nothing about attention, only the per-target guardrails.
type CycleInput struct {
	Now          time.Time
	Targets      []Target
	Reader       StreamReader
	Cadence      time.Duration // wall-clock per forecast step (the scrape interval)
	GraphVersion string
	P            params.ForecastParams
	// SplicePoints are operator context-window boundaries (deploy/config, 10 M6)
	// that decomposition (09 M5 §3.4) may cut the context at. Auto-detected gauge
	// resets need no input; this carries the operator-declared boundaries. Empty on
	// the backtest path (the corpus has no operator windows).
	SplicePoints []time.Time
	// Trace records the raw clock trajectories per invocation — the backtest
	// substrate (11 M5: band coverage needs the quantile VALUES, not just the
	// crossing times). Off by default; the live path never pays for it.
	Trace bool
}

// InvocationTrace is one clock invocation's raw material for backtesting:
// what went in (target, basis), what came out (trajectories), and how the
// pipeline judged it (candidate or silence reason).
type InvocationTrace struct {
	EntityCEI     string      `json:"entityCei"`
	Metric        string      `json:"metric"`
	StreamUID     string      `json:"streamUid"` // the (uid, metric) join key into the Parquet export
	BasisAt       time.Time   `json:"basisAt"`
	ContextPoints int         `json:"contextPoints"`
	BarValue      float64     `json:"barValue"`
	Direction     string      `json:"direction"`
	Quantiles     []float64   `json:"quantiles"`
	Point         []float64   `json:"point"`
	Bands         [][]float64 `json:"bands"` // [level][step], request order
	Silence       string      `json:"silence,omitempty"`
	Candidate     *Candidate  `json:"candidate,omitempty"`
	// Decomposition records the splice decisions (09 M5) — present whenever the
	// runner ran decomposition, so the backtest can see whether a verdict came from
	// a spliced or full window.
	Decomposition *DecompositionRecord `json:"decomposition,omitempty"`
	// RegimeShift records an undeclared upward baseline shift detected in the (post-
	// decompose) input (09 M5 companion) — present so the backtest can audit both the
	// flag and the severe-silence path. nil when the input was single-regime.
	RegimeShift *RegimeShift `json:"regimeShift,omitempty"`
}

// CycleResult is one cycle's honest accounting: candidates, every silence
// with its reason, the degraded mark for the Tier-B panel (doc 14 A13), and
// the invocation count the budget audit reads.
type CycleResult struct {
	Candidates  []Candidate
	Silences    []Silence
	Degraded    bool              // the clock errored/timed out at least once this cycle
	Invocations int               // clock calls actually made (≤ budget by construction)
	Traces      []InvocationTrace // populated only when CycleInput.Trace
}

// RunCycle runs the per-target pipeline (doc 09 §3.3, v1: fetch → dynamics
// guard → forecast → project → guardrails; decomposition and covariates are
// Phase 3/4). Order is the targets' canonical order; the result is
// deterministic given a deterministic ClockCaller.
//
// NON-GATING: this runs on the warm path. Errors degrade; nothing here can
// touch the deterministic tick.
func RunCycle(ctx context.Context, cc ClockCaller, in CycleInput) CycleResult {
	var res CycleResult
	silence := func(t Target, reason string) {
		res.Silences = append(res.Silences, Silence{EntityCEI: t.CEIKey, Metric: t.Metric, Reason: reason})
	}
	for _, t := range in.Targets {
		ids := in.Reader.StreamsFor(t.StreamUID, t.Metric)
		if len(ids) == 0 {
			silence(t, SilenceNoStream)
			continue
		}
		if len(ids) > 1 {
			// Multi-series families (sub-variable streams) are the doc 02
			// data_type queue; projecting an arbitrary one would be a guess.
			silence(t, SilenceAmbiguousStream)
			continue
		}
		// The static funnel sees the SIGNAL's shape; family signals bundle
		// many concrete metrics, so the stream's own exposition type is the
		// runtime truth (the same character source observe.evalThresholdVar
		// reads). A counter stream's cumulative level is not a projectable
		// gauge — deferred here exactly as the funnel defers counter signals.
		if expoType, ok := in.Reader.StreamType(ids[0]); ok && expoType == "counter" {
			silence(t, SilenceCounterStream)
			continue
		}
		samples := in.Reader.LastN(ids[0], maxContextFetch)
		if len(samples) < in.P.MinContext {
			silence(t, SilenceShortContext)
			continue
		}
		// Decomposition (09 M5 §3.4): splice the context at the most recent known
		// event boundary (an operator window or an auto-detected reset) so the clock
		// forecasts only the clean post-event remainder. No-op on a clean series; an
		// abort (too little clean data) silences this target with its reason. The
		// basis stamp is the LAST sample either way (splicing only trims the head).
		series, decomp := Decompose(samples, in.SplicePoints, in.P)
		if decomp.Aborted {
			silence(t, SilenceDecomposeAbort)
			continue
		}
		last := series[len(series)-1]

		// Already past the bar: that is a NOW, detection's jurisdiction (07);
		// forecasting "it will cross" after it crossed would be a false SOON.
		if crossed(last, t.BarValue, t.Direction) {
			silence(t, SilenceAlreadyCrossed)
			continue
		}
		// Dynamics guard (§3.2 "showing real dynamics" / §3.6 flat silence). flat()'s
		// coefficient-of-variation test alone falsely silences a low-variance series whose
		// recent points have BEGUN a sustained directional drift — a late-onset leak (60 quiet
		// points dilute a 4-point creep), the earliest and most valuable moment to warn.
		// RESCUE it with the EWMA-residual CUSUM (trend.go): silence as flat only if it is
		// BOTH low-variance AND has no recent sustained onset. Off-default ⇒ flat()-only.
		var trend TrendResult
		if in.P.TrendRescue {
			trend = Trend(series, trendParamsFrom(in.P))
		}
		if flat(series, in.P.FlatEpsilon) && !trend.Tripped {
			silence(t, SilenceFlat)
			continue
		}

		// Regime-shift contamination flag (09 M5 companion): an UNDECLARED upward
		// baseline shift left IN the (post-decompose) model input — a config/deploy
		// that raised the level toward the bar with no operator window. We never trim
		// it (an upward ramp IS the leak; the detector fires only on a STEP that
		// PLATEAUS). When the new regime is too short to forecast (post < MinContext)
		// the projection would reflect the STALE baseline (falsely reassuring), so we
		// silence; otherwise the projection is emitted but FLAGGED on the candidate.
		var shift *RegimeShift
		if rs, ok := DetectRegimeShift(series, in.P); ok {
			s := rs
			shift = &s
			if rs.Severe {
				silence(t, SilenceRegimeShift)
				continue
			}
		}

		res.Invocations++
		fc, err := forecastOnce(ctx, cc, series, in.P)
		if err != nil {
			// Degraded, visible (A13), and silent for this target — never a
			// fabricated candidate and never a retry storm inside the cycle.
			res.Degraded = true
			silence(t, SilenceClockDegraded)
			continue
		}
		// Band calibration (doc 20 P5): scale the clock band by the DECLARED, offline-
		// derived constant before judging — no-op at the default 1.0 (byte-identical).
		fc = CalibrateBand(fc, in.P.BandCalibration)
		basis := samples[len(samples)-1].At
		cand, reason := Project(t, fc, basis, in.Now, in.Cadence, len(series), in.GraphVersion, in.P)
		if cand != nil {
			if decomp.Spliced() {
				d := decomp
				cand.Decomp = &d
			}
			if shift != nil {
				cand.RegimeShift = shift
			}
			// Cold-start confidence: a candidate rescued by a drift whose new slope is still
			// short is flagged early-onset (low confidence) rather than silenced — the band
			// firms as the slope establishes. Flag, never fabricate the band (charter).
			if trend.Tripped && in.P.TrendMinOnsetPoints > 0 && trend.NewSlope < in.P.TrendMinOnsetPoints {
				cand.EarlyOnset = &EarlyOnset{
					Direction: trend.Direction, NewSlopePoints: trend.NewSlope, StepZ: trend.StepZ,
					Note: "the directional drift only just began (new slope below the configured floor); this projection is EARLY and low-confidence — the band firms as the slope establishes",
				}
			}
		}
		if in.Trace {
			tr := InvocationTrace{
				EntityCEI: t.CEIKey, Metric: t.Metric, StreamUID: t.StreamUID, BasisAt: basis.UTC(),
				ContextPoints: len(series), BarValue: t.BarValue, Direction: t.Direction,
				Quantiles: append([]float64{}, in.P.Quantiles...),
				Point:     fc.Point, Bands: fc.Quantiles,
				Silence: reason, Candidate: cand, Decomposition: &decomp, RegimeShift: shift,
			}
			res.Traces = append(res.Traces, tr)
		}
		if cand == nil {
			silence(t, reason)
			continue
		}
		res.Candidates = append(res.Candidates, *cand)
	}
	return res
}

// maxContextFetch bounds the per-target history read (the hot ring holds ~240
// points at dev cadence; the clock adapter truncates to ITS max context).
const maxContextFetch = 1024

// forecastOnce makes one deadline-bounded clock call (its own function so the
// per-call cancel releases promptly, not at cycle end).
func forecastOnce(ctx context.Context, cc ClockCaller, series []float64, p params.ForecastParams) (*clock.Forecast, error) {
	if p.Deadline.Duration() > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.Deadline.Duration())
		defer cancel()
	}
	return cc.Forecast(ctx, series, p.HorizonSteps, p.Quantiles)
}

func crossed(v, bar float64, direction string) bool {
	if direction == "below" {
		return v <= bar
	}
	return v >= bar
}

// trendParamsFrom builds the CUSUM trend-rescue params from the forecast params. Alpha
// (the EWMA baseline smoothing) is pinned to the onset/C2 detector's value; H<=0 disables.
func trendParamsFrom(p params.ForecastParams) TrendParams {
	return TrendParams{
		Alpha: 0.10, K: p.TrendCusumK, H: p.TrendCusumH,
		Warmup: p.TrendWarmup, MinZ: p.TrendMinZ, RecentWindow: p.TrendRecentWindow,
	}
}

// flat is the low-variance silence (§3.6): step-to-step volatility relative
// to the current level. Uses the most recent 64 steps (the rate window scale)
// so an old burst cannot make a now-flat series look dynamic.
func flat(series []float64, epsilon float64) bool {
	if epsilon <= 0 {
		return false
	}
	tail := series
	if len(tail) > 64 {
		tail = tail[len(tail)-64:]
	}
	var mean float64
	for _, v := range tail {
		mean += v
	}
	mean /= float64(len(tail))
	var varsum float64
	for _, v := range tail {
		varsum += (v - mean) * (v - mean)
	}
	std := math.Sqrt(varsum / float64(len(tail)))
	level := math.Abs(tail[len(tail)-1])
	if level < 1e-12 {
		level = 1e-12
	}
	return std/level < epsilon
}
