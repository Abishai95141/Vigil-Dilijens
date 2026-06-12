package forecast

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/binding"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/clock"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/params"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/qss"
)

var t0 = time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)

const (
	podA = "i|cl|shop|Pod|web-a|uid-a"
	podB = "i|cl|shop|Pod|web-b|uid-b"
)

// testGraph builds a minimal in-memory graph: one gauge signal that is a T0-
// member of a phenomenon, one counter signal, one non-series signal, and the
// rules that bind them.
func testGraph() *graph.Graph {
	g := &graph.Graph{
		Version: "sha256:test",
		Signals: map[string]*graph.Signal{
			"SIG_WS":  {ID: "SIG_WS", Modality: "Metric", DataType: "Gauge"},
			"SIG_CTR": {ID: "SIG_CTR", Modality: "Metric", DataType: "Counter"},
			"SIG_EVT": {ID: "SIG_EVT", Modality: "Metric", DataType: "Event v1"},
		},
		Phenomena: map[string]*graph.Phenomenon{
			"PHEN_OOM": {ID: "PHEN_OOM", Members: []graph.Member{
				{SignalID: "SIG_WS", Role: "required", TemporalOrder: "T0-"},
				{SignalID: "SIG_CTR", Role: "required", TemporalOrder: "T0"},
			}},
		},
		Rules: []*graph.ThresholdRule{
			{ID: "R_WS", Signal: "SIG_WS", Metric: "container_memory_working_set_bytes"},
			{ID: "R_CTR", Signal: "SIG_CTR", Metric: "container_oom_events_total"},
			{ID: "R_EVT", Signal: "SIG_EVT", Metric: "weird_metric"},
		},
	}
	return g
}

func levelBar(v float64) *binding.ResolvedBar {
	return &binding.ResolvedBar{Kind: "config-relative", Source: binding.SourceConfig, Value: v, Unit: "bytes", Direction: "above"}
}

func boundB(cei, rule, metric string, bar *binding.ResolvedBar) binding.Binding {
	return binding.Binding{CEIKey: cei, Entity: "Pod", RuleID: rule, Metric: metric, State: binding.StateBound, Bar: bar}
}

func TestEligibilityFunnel(t *testing.T) {
	g := testGraph()
	res := &binding.Result{Bindings: []binding.Binding{
		boundB(podA, "R_WS", "container_memory_working_set_bytes", levelBar(486)),                              // eligible + precursor
		boundB(podB, "R_CTR", "container_oom_events_total", levelBar(1)),                                       // counter -> deferred
		boundB(podB, "R_EVT", "weird_metric", levelBar(1)),                                                     // non-series
		boundB(podB, "R_WS", "container_memory_working_set_bytes", nil),                                        // unbounded
		{CEIKey: podB, Entity: "Pod", RuleID: "R_WS", Metric: "m2", State: binding.StateOutOfScope},            // not bound
		boundB(podA, "R_WS", "m3", &binding.ResolvedBar{Kind: "rate-of-change", Value: 1, Direction: "above"}), // rate guard
	}}
	targets, rejected := EligibleTargets(res, g)
	if len(targets) != 1 {
		t.Fatalf("want exactly the gauge+bar target, got %+v", targets)
	}
	tg := targets[0]
	if tg.CEIKey != podA || tg.SeriesKind != "gauge" || !tg.Precursor() {
		t.Errorf("target wrong: %+v", tg)
	}
	if len(tg.PrecursorPhenomena) != 1 || tg.PrecursorPhenomena[0] != "PHEN_OOM" {
		t.Errorf("T0- membership must become an AUTHORED precursor reference: %+v", tg.PrecursorPhenomena)
	}
	wantReasons := map[string]string{
		podB + "\x1f" + "container_oom_events_total":         ReasonCounterDeferred,
		podB + "\x1f" + "weird_metric":                       ReasonNonSeries,
		podB + "\x1f" + "container_memory_working_set_bytes": ReasonUnbounded,
		podB + "\x1f" + "m2":                                 ReasonNotBound,
		podA + "\x1f" + "m3":                                 ReasonRateGuardBar,
	}
	if len(rejected) != len(wantReasons) {
		t.Fatalf("every rejected pair must be LISTED with a reason: %+v", rejected)
	}
	for _, r := range rejected {
		if wantReasons[r.CEIKey+"\x1f"+r.Metric] != r.Reason {
			t.Errorf("wrong reason for (%s, %s): %s", r.CEIKey, r.Metric, r.Reason)
		}
	}
}

// scriptedForecast builds a clock.Forecast: point ramps from start by slope;
// band = point ± width (lower, upper trajectories).
func scriptedForecast(h int, start, slope, width float64) *clock.Forecast {
	fc := &clock.Forecast{Point: make([]float64, h), Quantiles: make([][]float64, 3)}
	for i := range fc.Quantiles {
		fc.Quantiles[i] = make([]float64, h)
	}
	for s := 0; s < h; s++ {
		v := start + slope*float64(s+1)
		fc.Point[s] = v
		fc.Quantiles[0][s] = v - width
		fc.Quantiles[1][s] = v
		fc.Quantiles[2][s] = v + width
	}
	return fc
}

func fp() params.ForecastParams {
	return params.ForecastParams{
		HorizonSteps: 64, Quantiles: []float64{0.1, 0.5, 0.9},
		MinContext: 8, FlatEpsilon: 0.005, MaxBandRatio: 2.0,
	}
}

func wsTarget() Target {
	return Target{
		CEIKey: podA, Entity: "Pod", Metric: "container_memory_working_set_bytes",
		StreamUID: "uid-a", SeriesKind: "gauge",
		BarValue: 486, BarUnit: "bytes", BarSource: "config", Direction: "above",
		PrecursorPhenomena: []string{"PHEN_OOM"},
	}
}

func TestProjectCrossingWithBand(t *testing.T) {
	// point starts at 420, +5/step: crosses 486 at step 13 (420+5*14=490 ≥ 486
	// at s index 13). upper crosses earlier, lower later.
	fc := scriptedForecast(64, 420, 5, 20)
	cadence := 15 * time.Second
	cand, reason := Project(wsTarget(), fc, t0, t0, cadence, 240, "sha256:test", fp())
	if cand == nil {
		t.Fatalf("expected a candidate, silenced: %s", reason)
	}
	if !cand.IsProjection || cand.Class != ClassProjected {
		t.Error("the mandatory projection mark is missing")
	}
	wantCross := t0.Add(14 * cadence) // index 13 ⇒ (13+1)×cadence after basis
	if !cand.CrossAt.Equal(wantCross) {
		t.Errorf("CrossAt = %v, want %v", cand.CrossAt, wantCross)
	}
	if !cand.EarliestAt.Before(cand.CrossAt) || cand.LatestBeyondHorizon || !cand.LatestAt.After(cand.CrossAt) {
		t.Errorf("band must straddle the point estimate: earliest=%v cross=%v latest=%v beyond=%v",
			cand.EarliestAt, cand.CrossAt, cand.LatestAt, cand.LatestBeyondHorizon)
	}
	if cand.EarliestAt.Equal(cand.LatestAt) {
		t.Error("the band collapsed to a line (doc 01 §3)")
	}
	if len(cand.PrecursorPhenomena) != 1 || cand.PrecursorPhenomena[0] != "PHEN_OOM" {
		t.Error("the AUTHORED precursor reference must ride the candidate")
	}
}

func TestProjectSilences(t *testing.T) {
	cadence := 15 * time.Second
	p := fp()

	// No crossing within horizon ⇒ silence.
	flatFc := scriptedForecast(64, 420, 0, 5)
	if cand, reason := Project(wsTarget(), flatFc, t0, t0, cadence, 240, "v", p); cand != nil || reason != SilenceNoCrossing {
		t.Errorf("no-crossing must silence, got cand=%v reason=%s", cand, reason)
	}

	// Band too wide ⇒ silence: a fast point crossing (step 5) under a huge band
	// whose lower edge never crosses — effective width spans the horizon, so
	// width/ttc ≈ 63/6 ≫ MaxBandRatio.
	wideFc := scriptedForecast(64, 480, 1, 200)
	if cand, reason := Project(wsTarget(), wideFc, t0, t0, cadence, 240, "v", p); cand != nil || reason != SilenceBandTooWide {
		t.Errorf("too-wide band must silence, got cand=%v reason=%s", cand, reason)
	}

	// Below-direction bar: mirrored crossing.
	below := wsTarget()
	below.Direction = "below"
	below.BarValue = 100
	fc := scriptedForecast(64, 130, -5, 10)
	cand, reason := Project(below, fc, t0, t0, cadence, 240, "v", p)
	if cand == nil {
		t.Fatalf("below-bar crossing must project: %s", reason)
	}
	if cand.Direction != "below" {
		t.Error("direction must ride the candidate")
	}
}

func TestProjectLatestBeyondHorizonIsStated(t *testing.T) {
	// Point crosses, upper crosses, lower NEVER crosses inside the horizon:
	// the latest edge is open — stated, not clamped; confidence capped wide.
	// width 130: lower = point-130 stays under 486 for 64 steps (420+5*64=740-130=610 — crosses!)
	// use slope so lower stays below: slope 2 → point max 548, lower max 418 (width 130) never crosses;
	// point crosses 486 at step (486-420)/2-1 = 32; upper crosses earlier.
	fc := scriptedForecast(64, 420, 2, 130)
	p := fp()
	p.MaxBandRatio = 10 // keep the width guard out of the way; we are testing the open edge
	cand, reason := Project(wsTarget(), fc, t0, t0, 15*time.Second, 240, "v", p)
	if cand == nil {
		t.Fatalf("expected candidate, silenced: %s", reason)
	}
	if !cand.LatestBeyondHorizon || !cand.LatestAt.IsZero() {
		t.Errorf("open latest edge must be STATED (beyond=%v latest=%v)", cand.LatestBeyondHorizon, cand.LatestAt)
	}
	if cand.Confidence != "wide" {
		t.Errorf("an open band edge cannot claim confidence: %s", cand.Confidence)
	}
}

// --- the runner -----------------------------------------------------------

type fakeReader struct {
	streams map[string][]string // uid\x1fmetric -> ids
	samples map[string][]qss.Sample
	types   map[string]string // nil = everything is a gauge
}

func (f *fakeReader) StreamsFor(uid, metric string) []string {
	return f.streams[uid+"\x1f"+metric]
}
func (f *fakeReader) LastN(id string, n int) []qss.Sample {
	s := f.samples[id]
	if len(s) > n {
		s = s[len(s)-n:]
	}
	return s
}

func (f *fakeReader) StreamType(id string) (string, bool) {
	if f.types == nil {
		return "gauge", true
	}
	t, ok := f.types[id]
	return t, ok
}

type scriptedClock struct {
	fc    *clock.Forecast
	err   error
	calls int
}

func (s *scriptedClock) Forecast(_ context.Context, series []float64, h int, q []float64) (*clock.Forecast, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.fc, nil
}

func ramp(n int, start, step float64) []qss.Sample {
	out := make([]qss.Sample, n)
	for i := range out {
		out[i] = qss.Sample{At: t0.Add(time.Duration(i-n+1) * 15 * time.Second), Value: start + step*float64(i)}
	}
	return out
}

func cycleInput(r *fakeReader, targets ...Target) CycleInput {
	return CycleInput{
		Now: t0, Targets: targets, Reader: r,
		Cadence: 15 * time.Second, GraphVersion: "v", P: fp(),
	}
}

func TestRunCycleSilenceLadder(t *testing.T) {
	tWS := wsTarget()
	noStream := tWS
	noStream.CEIKey, noStream.StreamUID = podB, "uid-none"

	ambiguous := tWS
	ambiguous.CEIKey, ambiguous.StreamUID = "i|cl|shop|Pod|amb|uid-amb", "uid-amb"

	short := tWS
	short.CEIKey, short.StreamUID = "i|cl|shop|Pod|sh|uid-sh", "uid-sh"

	crossed := tWS
	crossed.CEIKey, crossed.StreamUID = "i|cl|shop|Pod|cr|uid-cr", "uid-cr"

	flatT := tWS
	flatT.CEIKey, flatT.StreamUID = "i|cl|shop|Pod|fl|uid-fl", "uid-fl"

	m := tWS.Metric
	r := &fakeReader{
		streams: map[string][]string{
			"uid-a\x1f" + m:   {"s-a"},
			"uid-amb\x1f" + m: {"s-1", "s-2"},
			"uid-sh\x1f" + m:  {"s-sh"},
			"uid-cr\x1f" + m:  {"s-cr"},
			"uid-fl\x1f" + m:  {"s-fl"},
		},
		samples: map[string][]qss.Sample{
			"s-a":  ramp(120, 300, 1), // rising toward the bar, well-formed
			"s-sh": ramp(3, 300, 1),   // too little history
			"s-cr": ramp(120, 480, 1), // last = 599 ≥ 486: already crossed
			"s-fl": ramp(120, 400, 0), // flat
		},
	}
	sc := &scriptedClock{fc: scriptedForecast(64, 419, 5, 20)}
	res := RunCycle(context.Background(), sc, cycleInput(r, tWS, noStream, ambiguous, short, crossed, flatT))

	if len(res.Candidates) != 1 || res.Candidates[0].EntityCEI != podA {
		t.Fatalf("exactly the healthy ramp target must emit: %+v", res.Candidates)
	}
	want := map[string]string{
		podB:                        SilenceNoStream,
		"i|cl|shop|Pod|amb|uid-amb": SilenceAmbiguousStream,
		"i|cl|shop|Pod|sh|uid-sh":   SilenceShortContext,
		"i|cl|shop|Pod|cr|uid-cr":   SilenceAlreadyCrossed,
		"i|cl|shop|Pod|fl|uid-fl":   SilenceFlat,
	}
	if len(res.Silences) != len(want) {
		t.Fatalf("every quiet target must carry its reason: %+v", res.Silences)
	}
	for _, s := range res.Silences {
		if want[s.EntityCEI] != s.Reason {
			t.Errorf("silence for %s: got %s want %s", s.EntityCEI, s.Reason, want[s.EntityCEI])
		}
	}
	if sc.calls != 1 || res.Invocations != 1 {
		t.Errorf("only guard-passing targets may spend the budget: calls=%d invocations=%d", sc.calls, res.Invocations)
	}
	if res.Degraded {
		t.Error("nothing degraded this cycle")
	}
}

func TestRunCycleClockDegradedIsVisibleNeverFabricated(t *testing.T) {
	m := wsTarget().Metric
	r := &fakeReader{
		streams: map[string][]string{"uid-a\x1f" + m: {"s-a"}},
		samples: map[string][]qss.Sample{"s-a": ramp(120, 300, 1)},
	}
	sc := &scriptedClock{err: errors.New("unavailable")}
	res := RunCycle(context.Background(), sc, cycleInput(r, wsTarget()))
	if !res.Degraded {
		t.Error("a clock failure must surface as DEGRADED (doc 14 A13)")
	}
	if len(res.Candidates) != 0 {
		t.Error("a degraded clock must never fabricate a candidate")
	}
	if len(res.Silences) != 1 || res.Silences[0].Reason != SilenceClockDegraded {
		t.Errorf("the degraded target must be listed: %+v", res.Silences)
	}
}

func TestRunCycleDeterministic(t *testing.T) {
	m := wsTarget().Metric
	r := &fakeReader{
		streams: map[string][]string{"uid-a\x1f" + m: {"s-a"}},
		samples: map[string][]qss.Sample{"s-a": ramp(120, 300, 1)},
	}
	mk := func() CycleResult {
		return RunCycle(context.Background(), &scriptedClock{fc: scriptedForecast(64, 419, 5, 20)}, cycleInput(r, wsTarget()))
	}
	a, b := mk(), mk()
	if len(a.Candidates) != 1 || len(b.Candidates) != 1 {
		t.Fatal("setup: both runs must emit")
	}
	if a.Candidates[0].CrossAt != b.Candidates[0].CrossAt || a.Candidates[0].Confidence != b.Candidates[0].Confidence {
		t.Error("same inputs + same clock answer must project identically")
	}
}
