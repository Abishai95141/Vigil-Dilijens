package replay

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/binding"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/detect"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/forecast"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/observe"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/params"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/qss"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/selection"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/unexplained"
)

// Options configures one replay run.
type Options struct {
	BundleDir string
	Graph     *graph.Graph // must match the manifest's pinned version
	OutDir    string       // when set, the canonical JSON of every tick is written here

	// Evaluate switches the engine into EVALUATION mode (doc 11 M4 / 07 M6
	// sensitivity sweeps — the manifest's long-stated "time-shifted evaluation"
	// absence): the recorded readings, bars, and topology are re-evaluated
	// under THESE parameters instead of the manifest's, and recorded digests
	// are NOT compared (they describe the capture regime, not this one — a
	// comparison would be a guaranteed, meaningless mismatch). The report says
	// EvaluationMode loudly; evaluation output must never be presented as
	// verification.
	Evaluate *observe.FPParams

	// Forecast switches on the forecast-evaluation pass (doc 11 §3.1 "time-
	// shiftable" / 11 M5 backtests): at every recorded tick the REAL funnel,
	// budget, and runner re-run against the reconstructed reader "as of" that
	// instant, tracing raw trajectories. PROJECTED output is never part of the
	// digest — this pass verifies nothing about determinism and says so.
	Forecast *ForecastEval
}

// ForecastEval configures the forecast-evaluation pass.
type ForecastEval struct {
	Clock  forecast.ClockCaller  // the clock to drive (stub or model — the swap contract)
	Params params.ForecastParams // the EXPLICIT evaluation regime (never implied from the manifest)
	Budget int                   // Tier-B ceiling per tick (doc 06 M5)
	Events func(TickForecast)    // sink for per-tick events (the backtest substrate)
}

// TickForecast is one tick's forecast-evaluation output: every invocation's
// raw trajectories + verdicts, plus the non-invocation silences.
type TickForecast struct {
	EvalNow    time.Time                  `json:"evalNow"`
	BarsEpoch  int                        `json:"barsEpoch"`
	Cadence    time.Duration              `json:"cadence"`
	Traces     []forecast.InvocationTrace `json:"traces"`
	Silences   []forecast.Silence         `json:"silences"`
	Unbudgeted int                        `json:"unbudgeted"`
	Degraded   bool                       `json:"degraded"`
}

// TickOutcome is one replayed tick's verdict.
type TickOutcome struct {
	EvalNow        time.Time
	BarsEpoch      int
	RecordedDigest string
	ReplayedDigest string
	Match          bool
	Fingerprints   int
	Findings       int
}

// Report is a replay run's honest accounting.
type Report struct {
	Manifest     Manifest
	Segments     int
	Runs         int  // process-run boundaries replayed (restarts into the same bundle)
	UnsealedTail bool // an .active segment existed and was ignored (capture not closed)
	Samples      int64
	Streams      int
	Ticks        []TickOutcome
	Mismatches   int
	// TopologyLess: the bundle predates topology recording (no edge-budget pin)
	// — first-order matching was skipped, exactly the regime that captured it.
	TopologyLess bool
	// EvaluationMode: the run re-evaluated under OVERRIDDEN parameters (doc 11
	// M4 sweeps); tick outcomes carry findings/cascades counts but NO digest
	// verdicts — this run verifies nothing.
	EvaluationMode bool
}

// Run replays a bundle through the real observation and detection components,
// verifying every recorded tick's digest. It never mutates the bundle.
func Run(opts Options) (*Report, error) {
	var m Manifest
	raw, err := os.ReadFile(filepath.Join(opts.BundleDir, manifestName))
	if err != nil {
		return nil, fmt.Errorf("replay: read manifest: %w", err)
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("replay: parse manifest: %w", err)
	}
	if m.BundleVersion != bundleV1 {
		return nil, fmt.Errorf("replay: bundle version %d not supported (want %d)", m.BundleVersion, bundleV1)
	}
	if opts.Graph == nil {
		return nil, fmt.Errorf("replay: no graph provided")
	}
	if opts.Graph.Version != m.GraphVersion {
		// Pinned-version everything (doc 11 §3.1): replaying against a different
		// graph would change findings and silently invalidate the comparison.
		return nil, fmt.Errorf("replay: graph version mismatch: bundle pinned %s, loaded %s",
			m.GraphVersion, opts.Graph.Version)
	}
	// Manifest plausibility: a zeroed/garbage parameter set would replay with
	// silently different semantics (zero watermark = everything stale) and the
	// failure would masquerade as a determinism violation. Refuse instead.
	if m.FPParams.ScrapeInterval <= 0 || m.FPParams.RateWindow <= 0 || m.FPParams.Watermark <= 0 ||
		m.FPParams.CooccurrenceWindow <= 0 ||
		m.FPParams.Band < 0 || m.FPParams.Band >= 1 || m.FPParams.WellAboveFactor <= 0 ||
		m.FPParams.MinCompleteness < 0 || m.FPParams.MinCompleteness >= 1 || m.FPParams.CascadeWindow < 0 {
		return nil, fmt.Errorf("replay: manifest parameter set implausible (%+v) — refusing to replay with different semantics", m.FPParams)
	}
	// The hot-ring capacity is a digest-bearing constant (it bounds what a window
	// evaluation can see). A bundle captured under a different capacity cannot
	// replay byte-identically; refuse rather than mis-verify. Zero = an older
	// bundle that predates the pin (accepted; the current capacity applied then too).
	if m.HotRingCapacity != 0 && m.HotRingCapacity != qss.HotCapacity() {
		return nil, fmt.Errorf("replay: bundle captured with hot-ring capacity %d, this binary has %d — cannot replay byte-identically",
			m.HotRingCapacity, qss.HotCapacity())
	}

	rules := make(map[string]*graph.ThresholdRule, len(opts.Graph.Rules))
	for _, r := range opts.Graph.Rules {
		rules[r.ID] = r
	}
	matcher := detect.NewMatcher(opts.Graph)
	reader := newBundleReader()
	rep := &Report{Manifest: m, TopologyLess: m.EdgeBudgets == nil}
	bars := map[int]*barsEpoch{} // epoch -> resolved bars + Tier-A set (lazy, cached)
	budgets := make(map[identity.EdgeType]time.Duration, len(m.EdgeBudgets))
	for k, v := range m.EdgeBudgets {
		budgets[identity.EdgeType(k)] = v
	}
	// The evaluation regime: the manifest's pinned parameters for VERIFICATION;
	// the caller's overrides for EVALUATION (sensitivity sweeps). Verification
	// always replays under the capture's regime, never the local file's.
	regime := m.FPParams
	if opts.Evaluate != nil {
		e := *opts.Evaluate
		if e.ScrapeInterval <= 0 || e.RateWindow <= 0 || e.Watermark <= 0 ||
			e.CooccurrenceWindow <= 0 ||
			e.Band < 0 || e.Band >= 1 || e.WellAboveFactor <= 0 ||
			e.MinCompleteness < 0 || e.MinCompleteness >= 1 || e.CascadeWindow < 0 {
			return nil, fmt.Errorf("replay: evaluation parameter set implausible (%+v)", e)
		}
		regime = e
		rep.EvaluationMode = true
	}
	// Detection sensitivity from the regime (doc 07 §3.6): the matcher's
	// degraded-surfacing floor and the cascade window.
	matcher.MinCompleteness = regime.MinCompleteness
	// Cascade recognition is windowed over the tick SEQUENCE (doc 07 §3.4):
	// the tracker accumulates across ticks exactly as live did, and resets at
	// every run-start frame — the live process restart that emptied it.
	tracker := detect.NewCascadeTracker(regime.EffectiveCascadeWindow())
	// The unexplained channel (doc 08) ages cards across windows the same way
	// and resets at the same boundary; both are part of the digest.
	unexpTracker := unexplained.NewTracker(opts.Graph.Version)

	segs, err := qss.ListSegments(filepath.Join(opts.BundleDir, segmentsDir))
	if err != nil {
		return nil, err
	}
	if len(segs) == 0 {
		return nil, fmt.Errorf("replay: bundle has no segments")
	}
	if opts.OutDir != "" {
		if err := os.MkdirAll(opts.OutDir, 0o755); err != nil {
			return nil, fmt.Errorf("replay: %w", err)
		}
	}

	for _, seg := range segs {
		if !seg.Sealed {
			rep.UnsealedTail = true // stated in the report, never silently included
			continue
		}
		rep.Segments++
		idxDef := map[uint32]qss.StreamDef{} // per-segment stream dictionary
		err := qss.ReplayFrames(seg.Path, func(f qss.Frame) error {
			switch f.Kind {
			case qss.FrameRunStart:
				// A process-run boundary: live evaluation restarted with empty
				// rings, a fresh stream registry, and an empty cascade tracker;
				// the reconstruction must too, or replay would evaluate
				// pre-restart state live never saw.
				reader.reset()
				tracker.Reset()
				unexpTracker.Reset()
				rep.Runs++
			case qss.FrameDef:
				idxDef[f.Idx] = f.Def
				reader.register(f.Def)
			case qss.FrameSample:
				def, ok := idxDef[f.Idx]
				if !ok {
					return fmt.Errorf("sample references undefined stream index %d (corrupt segment)", f.Idx)
				}
				reader.hot.Append(def.ID, qss.Sample{At: f.At, Value: f.Value})
				rep.Samples++
			case qss.FrameTick:
				var rec TickRecord
				if err := json.Unmarshal(f.Payload, &rec); err != nil {
					return fmt.Errorf("tick frame: %w", err)
				}
				outcome, err := evalTick(rec, bars, opts, rules, matcher, reader, m, budgets, tracker, unexpTracker, regime, rep.EvaluationMode)
				if err != nil {
					return err
				}
				rep.Ticks = append(rep.Ticks, outcome)
				if !rep.EvaluationMode && !outcome.Match {
					rep.Mismatches++
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	rep.Streams = len(reader.meta)
	return rep, nil
}

// barsEpoch is one cached resolved-bar set plus the Tier-A selection derived
// from it (selection is a pure function of bars + graph, doc 06), and — when
// the forecast pass runs — the budgeted Tier-B targets (also pure of bars +
// graph + ceiling).
type barsEpoch struct {
	res        *binding.Result
	selected   map[string][]string
	tierB      []forecast.Target
	unbudgeted int
}

// evalTick re-runs one recorded evaluation tick: materialize fingerprints from
// the reconstructed rings against the recorded bars epoch, rebuild the recorded
// topology snapshot under the pinned edge budgets, match phenomena over the
// selected (Tier-A) entities, and compare digests.
func evalTick(rec TickRecord, bars map[int]*barsEpoch, opts Options,
	rules map[string]*graph.ThresholdRule, matcher *detect.Matcher, reader *bundleReader, m Manifest,
	budgets map[identity.EdgeType]time.Duration, tracker *detect.CascadeTracker, unexpTracker *unexplained.Tracker,
	regime observe.FPParams, evalMode bool) (TickOutcome, error) {

	var fps []observe.Fingerprint
	var findings []detect.Finding
	var cascades []detect.Cascade
	var unexp []unexplained.Finding
	if rec.BarsEpoch > 0 {
		ep, ok := bars[rec.BarsEpoch]
		if !ok {
			var bf BarsFile
			raw, err := os.ReadFile(filepath.Join(opts.BundleDir, barsName(rec.BarsEpoch)))
			if err != nil {
				return TickOutcome{}, fmt.Errorf("replay: tick references bars epoch %d: %w", rec.BarsEpoch, err)
			}
			if err := json.Unmarshal(raw, &bf); err != nil {
				return TickOutcome{}, fmt.Errorf("replay: bars epoch %d: %w", rec.BarsEpoch, err)
			}
			res := &binding.Result{Bindings: bf.Bindings}
			// The same selection live detection consumes (doc 06): the Tier-A set
			// is a pure function of (bindings, graph), both pinned in the bundle —
			// the funnel replays exactly. (For any phenomenon the matcher can fire,
			// the entity is by construction a participant, so the filter can only
			// drop fingerprints that would have produced no findings —
			// pre-selection bundles replay identically.)
			ep = &barsEpoch{res: res, selected: selection.TierASet(res, opts.Graph)}
			if opts.Forecast != nil {
				targets, _ := forecast.EligibleTargets(res, opts.Graph)
				sel := selection.SelectTierB(targets, opts.Forecast.Budget)
				ep.tierB = selection.BudgetedTargets(sel)
				ep.unbudgeted = len(sel.Unbudgeted)
			}
			bars[rec.BarsEpoch] = ep
		}
		fps = observe.Materialize(ep.res, rules, reader, regime, rec.EvalNow)

		// Topology (doc 07 §3.7): rebuild the recorded snapshot under the PINNED
		// budgets and walk it through the same Match path live used. A tick with
		// no recorded topology (pre-topology bundle, manifest budgets nil) runs
		// entity-local-only — the regime that recorded it, stated in the report.
		// A topology-recording bundle whose tick lacks the field is a corrupt or
		// hand-edited bundle: refuse rather than mis-verify.
		var topo detect.Topology
		if m.EdgeBudgets != nil {
			if rec.Topology == nil {
				return TickOutcome{}, fmt.Errorf("replay: tick %s carries no topology snapshot but the manifest pins edge budgets (corrupt bundle?)", rec.EvalNow.Format(time.RFC3339Nano))
			}
			store, err := identity.NewEdgeStoreFromSnapshot(rec.Topology, budgets)
			if err != nil {
				return TickOutcome{}, fmt.Errorf("replay: tick %s: %w", rec.EvalNow.Format(time.RFC3339Nano), err)
			}
			topo = store
		} else if rec.Topology != nil {
			return TickOutcome{}, fmt.Errorf("replay: tick %s records topology but the manifest pins no edge budgets — suspicion thresholds unknown; refusing to guess", rec.EvalNow.Format(time.RFC3339Nano))
		}
		w := identity.TimeWindow{Start: rec.EvalNow.Add(-regime.CooccurrenceWindow), End: rec.EvalNow}
		findings = matcher.Match(fps, ep.selected, topo, w)
		// Cascades (07 M5): recognize against the tracker's window, THEN
		// observe this tick — the same order live evaluation uses.
		cascades = matcher.Cascades(rec.EvalNow, findings, tracker, topo, w)
		tracker.Observe(rec.EvalNow, findings)
		// Unexplained channel (doc 08): loud-but-unmatched routing, after
		// detection so the coverage check sees this tick's matches.
		unexp = unexpTracker.Route(rec.EvalNow, fps, findings)

		// Forecast-evaluation pass (doc 11 §3.1 time-shift / M5 backtests):
		// the REAL pipeline "as of" this tick over the reconstructed reader.
		// PROJECTED output — never digest-bearing, never a verification claim.
		if opts.Forecast != nil {
			cyc := forecast.RunCycle(context.Background(), opts.Forecast.Clock, forecast.CycleInput{
				Now: rec.EvalNow, Targets: ep.tierB, Reader: reader,
				Cadence: m.ScrapeInterval, GraphVersion: m.GraphVersion,
				P: opts.Forecast.Params, Trace: true,
			})
			if opts.Forecast.Events != nil {
				opts.Forecast.Events(TickForecast{
					EvalNow: rec.EvalNow, BarsEpoch: rec.BarsEpoch, Cadence: m.ScrapeInterval,
					Traces: cyc.Traces, Silences: cyc.Silences,
					Unbudgeted: ep.unbudgeted, Degraded: cyc.Degraded,
				})
			}
		}
	}
	digest, canonical, err := Digest(rec.EvalNow, fps, findings, cascades, unexp)
	if err != nil {
		return TickOutcome{}, err
	}
	out := TickOutcome{
		EvalNow: rec.EvalNow, BarsEpoch: rec.BarsEpoch,
		RecordedDigest: rec.Digest, ReplayedDigest: digest,
		Match:        digest == rec.Digest,
		Fingerprints: len(fps), Findings: len(findings),
	}
	if evalMode {
		// Evaluation mode verifies nothing: the recorded digest describes the
		// CAPTURE regime; comparing it against an overridden regime would be a
		// guaranteed, meaningless mismatch dressed as a verdict.
		out.RecordedDigest, out.Match = "", true
	}
	if opts.OutDir != "" {
		name := fmt.Sprintf("tick-%s.json", rec.EvalNow.UTC().Format("20060102T150405.000000000Z"))
		if err := os.WriteFile(filepath.Join(opts.OutDir, name), append(canonical, '\n'), 0o644); err != nil {
			return TickOutcome{}, fmt.Errorf("replay: %w", err)
		}
	}
	return out, nil
}

// bundleReader is the engine's observe.StreamReader: the same read-side
// semantics as the live Ingestor (sorted (UID, metric) joins, hot-ring reads),
// reconstructed purely from the bundle.
type bundleReader struct {
	hot         *qss.HotStore
	meta        map[string]qss.StreamDef
	byUIDMetric map[string][]string // uid+"\x00"+metric -> sorted streamIDs
}

var _ observe.StreamReader = (*bundleReader)(nil)

func newBundleReader() *bundleReader {
	return &bundleReader{hot: qss.NewHotStore(), meta: map[string]qss.StreamDef{}, byUIDMetric: map[string][]string{}}
}

// reset clears all reconstructed state — a process-run boundary: the live side
// restarted with empty rings and a fresh stream registry.
func (r *bundleReader) reset() {
	r.hot = qss.NewHotStore()
	r.meta = map[string]qss.StreamDef{}
	r.byUIDMetric = map[string][]string{}
}

func (r *bundleReader) register(def qss.StreamDef) {
	if _, seen := r.meta[def.ID]; seen {
		return // defs re-emitted per segment; first registration wins
	}
	r.meta[def.ID] = def
	k := def.UID + "\x00" + def.Metric
	ids := append(r.byUIDMetric[k], def.ID)
	sort.Strings(ids) // mirror Ingestor.StreamsByUIDMetric's sorted contract
	r.byUIDMetric[k] = ids
}

func (r *bundleReader) StreamsFor(uid, metric string) []string {
	return r.byUIDMetric[uid+"\x00"+metric]
}

func (r *bundleReader) Latest(streamID string) (qss.Sample, bool) { return r.hot.Latest(streamID) }

func (r *bundleReader) LastN(streamID string, n int) []qss.Sample { return r.hot.LastN(streamID, n) }

func (r *bundleReader) StreamType(streamID string) (string, bool) {
	m, ok := r.meta[streamID]
	return m.Type, ok
}
