// Package params owns the single, versioned parameters file: every tunable
// constant the runtime uses, loaded once and threaded through the components.
//
// Owning doc: 14-implementation-clarifications.md §5. The blueprint's discipline
// is that no constant is hard-coded in logic and none is learned per-customer —
// they all live here, are revised only by harness evidence (doc 11), and each
// carries its source citation. No time.Now and no learned values ever enter this
// package; it is pure declared configuration.
package params

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// devDefaults is the committed dev-profile parameters file, embedded so the
// binary is self-contained and `Default()` never touches the filesystem.
//
//go:embed defaults.dev.yaml
var devDefaults []byte

// Duration is a time.Duration that marshals to/from Go duration strings
// ("15s", "5m", "168h"). Note: Go's duration grammar has no "d" unit, so days
// are written as hours (7d -> "168h") — kept explicit on purpose.
type Duration time.Duration

// Duration returns the underlying time.Duration.
func (d Duration) Duration() time.Duration { return time.Duration(d) }

// String renders the canonical Go duration form.
func (d Duration) String() string { return time.Duration(d).String() }

// UnmarshalYAML parses a duration string, rejecting anything time.ParseDuration
// rejects (including the unsupported "d" unit) with a contextual error.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return fmt.Errorf("duration must be a string: %w", err)
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

// MarshalYAML renders the duration as its canonical string for round-tripping.
func (d Duration) MarshalYAML() (any, error) { return time.Duration(d).String(), nil }

// Params is the whole parameters file. Field groups mirror the owning component
// docs; every field cites the doc/section that fixes its initial value.
type Params struct {
	Version     string            `yaml:"version"`
	Profile     string            `yaml:"profile"` // dev | prod
	Scrape      ScrapeParams      `yaml:"scrape"`
	Identity    IdentityParams    `yaml:"identity"`
	Observation ObservationParams `yaml:"observation"`
	Detection   DetectionParams   `yaml:"detection"`
	Store       StoreParams       `yaml:"store"`
	Binding     BindingParams     `yaml:"binding"`
	Selection   SelectionParams   `yaml:"selection"`
	Forecast    ForecastParams    `yaml:"forecast"`
	Incident    IncidentParams    `yaml:"incident"`
}

// IncidentParams — v3 T-B (doc 03 + 14 §1.4): the durable cross-run phenomenon
// memory. Both are off the deterministic path (surfacing-only). ResolveGap MUST
// exceed the evaluation tick so a continuous condition's consecutive ticks are not
// counted as recurrences; WindowBucket is the week-scale baseline window the
// incident key buckets on.
type IncidentParams struct {
	ResolveGap   Duration `yaml:"resolve_gap"`
	WindowBucket Duration `yaml:"window_bucket"`
}

// ScrapeParams — doc 14 §5/A1.
type ScrapeParams struct {
	Interval Duration `yaml:"interval"`
}

// IdentityParams — doc 03 + doc 14 §1.
type IdentityParams struct {
	Reconciliation       Duration            `yaml:"reconciliation"`
	EdgeBudgets          map[string]Duration `yaml:"edge_budgets"`
	RetractedEdgeHorizon Duration            `yaml:"retracted_edge_horizon"`
	Tombstone            TombstoneParams     `yaml:"tombstone"`
}

// TombstoneParams — doc 14 §1.4.
type TombstoneParams struct {
	FullRetention Duration `yaml:"full_retention"`
	StubRetention Duration `yaml:"stub_retention"`
	MaxEntries    int      `yaml:"max_entries"`
}

// ObservationParams — doc 05 + doc 14 §A3,A5,A6.
type ObservationParams struct {
	EvaluationTick            Duration `yaml:"evaluation_tick"`
	Watermark                 Duration `yaml:"watermark"`
	RateWindow                Duration `yaml:"rate_window"`
	DefaultCooccurrenceWindow Duration `yaml:"default_cooccurrence_window"`
	WellAboveFactor           float64  `yaml:"well_above_factor"`
	AtThresholdBand           float64  `yaml:"at_threshold_band"`
}

// DetectionParams — doc 07 §3.6: the sensitivity parameters. How strict a match
// must be is tunable, never hard-coded; the shipped DEFAULTS are justified by
// precision/recall on the labeled replay corpus (07 M6 / 11 M4) — the evidence
// is corpus/labels/calibration-07M6.md. Both values are digest-bearing and ride
// the replay-bundle manifest (a capture replays under ITS regime, never the
// local file's).
type DetectionParams struct {
	// MinCompleteness is the span-completeness threshold for degraded surfacing
	// (doc 07 §3.6): a degraded match whose required-member completeness falls
	// below it does not surface. 0 = every anchored degraded match surfaces (the
	// anchor-evidence rule remains the structural noise gate).
	MinCompleteness float64 `yaml:"min_completeness"`
	// CascadeWindow is how far back a trigger finding may lie and still pair
	// with a downstream finding into one cascade (doc 07 §3.4). Per-relation
	// widths are future authoring; one global width for now, stated.
	CascadeWindow Duration `yaml:"cascade_window"`
}

// StoreParams — doc 05 §3.1 + doc 14 §2.3.
type StoreParams struct {
	HotRing         Duration `yaml:"hot_ring"`
	WarmRetention   Duration `yaml:"warm_retention"`
	SegmentDuration Duration `yaml:"segment_duration"`
	FsyncBatch      Duration `yaml:"fsync_batch"`
}

// BindingParams — doc 04 + doc 14 §A4.
type BindingParams struct {
	BarReResolution Duration `yaml:"bar_reresolution"`
}

// SelectionParams — doc 06 + doc 14 §5.
type SelectionParams struct {
	TierBBudgetPerCycle int `yaml:"tier_b_budget_per_cycle"`
}

// ForecastParams — doc 09 §3.3/§3.6/§3.7 + doc 14 A13/A15. The forecasting
// regime: cadence, horizon, quantile levels, and the v1 guardrail constants.
// Every silence threshold is a parameter — calibrated by the backtest gate
// (09 M3 / 11 M5), never a hard-coded judgement.
type ForecastParams struct {
	Enabled      bool      `yaml:"enabled"`        // the warm path is opt-in; no class surfaces before its gate
	ClockdTarget string    `yaml:"clockd_target"`  // host:port of the clock service
	Interval     Duration  `yaml:"interval"`       // forecast cycle cadence (parallel to, never gating, the tick)
	Deadline     Duration  `yaml:"deadline"`       // per-RPC clock deadline (degraded past it, doc 14 A13)
	HorizonSteps int       `yaml:"horizon_steps"`  // forecast steps per inference (× scrape interval = wall horizon)
	Quantiles    []float64 `yaml:"quantiles"`      // requested levels, ascending, within the model head [0.1, 0.9]
	MinContext   int       `yaml:"min_context"`    // minimum history points before a target may run
	FlatEpsilon  float64   `yaml:"flat_epsilon"`   // stddev/|level| below which the series is flat ⇒ silence (§3.6)
	MaxBandRatio float64   `yaml:"max_band_ratio"` // max near-cone width (earliest→point) as a FRACTION of the horizon ⇒ silence (§3.6; ABSOLUTE, never vs time-to-cross — so an imminent crossing is not suppressed)

	// BandCalibration (doc 20 P5) is a DECLARED, OFFLINE-derived, customer-INVARIANT
	// scale applied to the clock's band half-width around the point estimate, so the
	// nominal band matches the empirically-observed coverage from the backtest gate
	// (a p10/p90 band that actually contains ~80% of actuals). It is NOT online
	// learning — it is a versioned constant, the same class as MaxBandRatio. 1.0 is the
	// identity (the raw clock band, byte-identical to no calibration); it must be > 0
	// (a 0 would collapse the band to a line, which the charter forbids).
	BandCalibration float64 `yaml:"band_calibration"`

	// Decomposition (doc 09 §3.4 / M5, Phase 3): splice the forecast context at the
	// most recent KNOWN event boundary — an operator context window (10) or an
	// auto-detected gauge RESET (a container restart) — so the clock forecasts only
	// the clean post-event remainder, not a polluted sawtooth (ramp→kill→reset).
	Decompose            bool    `yaml:"decompose"`              // enable splice-point decomposition
	ResetDropFraction    float64 `yaml:"reset_drop_fraction"`    // a step drop below (1−this)×prior ⇒ a reset boundary
	MaxExplainedFraction float64 `yaml:"max_explained_fraction"` // if splicing removes more than this fraction of the window ⇒ abort (untrustworthy)

	// Regime-shift contamination flag (doc 09 M5 companion): a MEASURED honesty
	// signal that the forecast input still holds an UNDECLARED upward baseline shift
	// (a config/deploy that raised the level toward the bar with no operator context
	// window). It NEVER trims the input — an upward RAMP is the leak we forecast, so
	// auto-removing an upward move would blind the warning; it fires only on a STEP
	// that PLATEAUS, and only FLAGS (or, when the new regime is too short to forecast,
	// silences with SilenceRegimeShift). Constants are AUTHORED, never learned.
	RegimeShiftFlag       bool    `yaml:"regime_shift_flag"`        // enable the contamination flag
	RegimeShiftFraction   float64 `yaml:"regime_shift_fraction"`    // up-jump ≥ this × window-range to count (magnitude floor)
	RegimeShiftMinSegment int     `yaml:"regime_shift_min_segment"` // min points each side of the shift (a sustained regime, not a transient)
	RegimeShiftPlateau    float64 `yaml:"regime_shift_plateau"`     // pre/post net drift ≤ this × jump ⇒ a STEP that plateaus (not a ramp — the leak guard)
}

// Default returns the embedded dev-profile parameters, validated.
func Default() (Params, error) {
	var p Params
	if err := yaml.Unmarshal(devDefaults, &p); err != nil {
		return Params{}, fmt.Errorf("parse embedded defaults: %w", err)
	}
	if err := p.Validate(); err != nil {
		return Params{}, fmt.Errorf("embedded defaults invalid: %w", err)
	}
	return p, nil
}

// Load returns the embedded defaults overlaid with the file at path (if non-empty).
// Overlay semantics: the file need only specify the keys it changes; everything
// else retains its default. This keeps operator override files small and auditable.
func Load(path string) (Params, error) {
	var p Params
	if err := yaml.Unmarshal(devDefaults, &p); err != nil {
		return Params{}, fmt.Errorf("parse embedded defaults: %w", err)
	}
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return Params{}, fmt.Errorf("read params file %q: %w", path, err)
		}
		if err := yaml.Unmarshal(raw, &p); err != nil {
			return Params{}, fmt.Errorf("parse params file %q: %w", path, err)
		}
	}
	if err := p.Validate(); err != nil {
		return Params{}, fmt.Errorf("params invalid: %w", err)
	}
	return p, nil
}

// requiredEdgeBudgets are the edge types the detection/selection layers must have
// a staleness budget for (doc 14 §1.2). Missing any is a configuration error,
// not a silent default — stale-edge handling is trust-critical (doc 07 §3.2).
var requiredEdgeBudgets = []string{"runs-on", "selects", "mounts", "owns", "node-lease"}

// Validate enforces the invariants a malformed or partial file could violate.
// It is deliberately strict: a bad parameter silently mis-grades every downstream
// check, so we fail loudly at load time instead.
func (p Params) Validate() error {
	var errs []error

	if p.Version == "" {
		errs = append(errs, errors.New("version must be set"))
	}
	switch p.Profile {
	case "dev", "prod":
	default:
		errs = append(errs, fmt.Errorf("profile must be dev or prod, got %q", p.Profile))
	}

	positive := func(name string, d Duration) {
		if d.Duration() <= 0 {
			errs = append(errs, fmt.Errorf("%s must be > 0, got %s", name, d))
		}
	}
	positive("scrape.interval", p.Scrape.Interval)
	positive("identity.reconciliation", p.Identity.Reconciliation)
	positive("identity.retracted_edge_horizon", p.Identity.RetractedEdgeHorizon)
	positive("identity.tombstone.full_retention", p.Identity.Tombstone.FullRetention)
	positive("identity.tombstone.stub_retention", p.Identity.Tombstone.StubRetention)
	positive("observation.evaluation_tick", p.Observation.EvaluationTick)
	positive("observation.watermark", p.Observation.Watermark)
	positive("observation.rate_window", p.Observation.RateWindow)
	positive("observation.default_cooccurrence_window", p.Observation.DefaultCooccurrenceWindow)
	positive("store.hot_ring", p.Store.HotRing)
	positive("store.warm_retention", p.Store.WarmRetention)
	positive("store.segment_duration", p.Store.SegmentDuration)
	positive("store.fsync_batch", p.Store.FsyncBatch)
	positive("binding.bar_reresolution", p.Binding.BarReResolution)
	positive("incident.resolve_gap", p.Incident.ResolveGap)
	positive("incident.window_bucket", p.Incident.WindowBucket)
	// The resolve gap must exceed the evaluation tick, or a continuous condition's
	// consecutive ticks would each be counted as a separate recurrence (doc 14 §1.4).
	if p.Incident.ResolveGap.Duration() > 0 && p.Incident.ResolveGap.Duration() <= p.Observation.EvaluationTick.Duration() {
		errs = append(errs, fmt.Errorf("incident.resolve_gap (%s) must exceed observation.evaluation_tick (%s)",
			p.Incident.ResolveGap, p.Observation.EvaluationTick))
	}

	for _, edge := range requiredEdgeBudgets {
		d, ok := p.Identity.EdgeBudgets[edge]
		if !ok {
			errs = append(errs, fmt.Errorf("identity.edge_budgets missing required edge type %q", edge))
			continue
		}
		positive("identity.edge_budgets."+edge, d)
	}

	if p.Identity.Tombstone.MaxEntries <= 0 {
		errs = append(errs, fmt.Errorf("identity.tombstone.max_entries must be > 0, got %d", p.Identity.Tombstone.MaxEntries))
	}
	if p.Observation.WellAboveFactor <= 1.0 {
		errs = append(errs, fmt.Errorf("observation.well_above_factor must be > 1.0, got %v", p.Observation.WellAboveFactor))
	}
	if p.Observation.AtThresholdBand < 0 || p.Observation.AtThresholdBand >= 1.0 {
		errs = append(errs, fmt.Errorf("observation.at_threshold_band must be in [0, 1.0), got %v", p.Observation.AtThresholdBand))
	}
	if p.Detection.MinCompleteness < 0 || p.Detection.MinCompleteness >= 1.0 {
		errs = append(errs, fmt.Errorf("detection.min_completeness must be in [0, 1.0), got %v", p.Detection.MinCompleteness))
	}
	positive("detection.cascade_window", p.Detection.CascadeWindow)
	if p.Selection.TierBBudgetPerCycle <= 0 {
		errs = append(errs, fmt.Errorf("selection.tier_b_budget_per_cycle must be > 0, got %d", p.Selection.TierBBudgetPerCycle))
	}

	// Forecast regime (doc 09): validated whenever set, enforced-required when
	// enabled — a bad guardrail constant silently mis-grades every emission.
	f := p.Forecast
	if f.Enabled && f.ClockdTarget == "" {
		errs = append(errs, errors.New("forecast.clockd_target must be set when forecast.enabled"))
	}
	if f.Enabled {
		positive("forecast.interval", f.Interval)
		positive("forecast.deadline", f.Deadline)
	}
	if f.HorizonSteps < 0 {
		errs = append(errs, fmt.Errorf("forecast.horizon_steps must be >= 0, got %d", f.HorizonSteps))
	}
	if f.Enabled && f.HorizonSteps == 0 {
		errs = append(errs, errors.New("forecast.horizon_steps must be > 0 when forecast.enabled"))
	}
	if f.Enabled && f.MinContext <= 1 {
		errs = append(errs, fmt.Errorf("forecast.min_context must be > 1 when enabled, got %d", f.MinContext))
	}
	if f.FlatEpsilon < 0 {
		errs = append(errs, fmt.Errorf("forecast.flat_epsilon must be >= 0, got %v", f.FlatEpsilon))
	}
	if f.MaxBandRatio < 0 {
		errs = append(errs, fmt.Errorf("forecast.max_band_ratio must be >= 0, got %v", f.MaxBandRatio))
	}
	// BandCalibration (doc 20 P5): a configured forecast must declare a POSITIVE scale
	// (1.0 = identity). A 0 would collapse the band to a line — the charter forbids it.
	if f.Enabled && f.BandCalibration <= 0 {
		errs = append(errs, fmt.Errorf("forecast.band_calibration must be > 0 when enabled (1.0 = identity), got %v", f.BandCalibration))
	}
	if f.Decompose && (f.ResetDropFraction <= 0 || f.ResetDropFraction >= 1) {
		errs = append(errs, fmt.Errorf("forecast.reset_drop_fraction must be in (0,1) when decompose is enabled, got %v", f.ResetDropFraction))
	}
	// MaxExplainedFraction in (0,1] when enabled: 0 would SILENTLY DISABLE the
	// "too much explained" abort (the abort is the safety valve, doc 09 §3.4) — a
	// footgun where the most-conservative-looking value turns the check off.
	if f.Decompose && (f.MaxExplainedFraction <= 0 || f.MaxExplainedFraction > 1) {
		errs = append(errs, fmt.Errorf("forecast.max_explained_fraction must be in (0,1] when decompose is enabled, got %v", f.MaxExplainedFraction))
	}
	if f.RegimeShiftFlag {
		if f.RegimeShiftFraction <= 0 || f.RegimeShiftFraction > 1 {
			errs = append(errs, fmt.Errorf("forecast.regime_shift_fraction must be in (0,1] when regime_shift_flag is enabled, got %v", f.RegimeShiftFraction))
		}
		if f.RegimeShiftMinSegment < 2 {
			errs = append(errs, fmt.Errorf("forecast.regime_shift_min_segment must be >= 2 when regime_shift_flag is enabled, got %d", f.RegimeShiftMinSegment))
		}
		if f.RegimeShiftPlateau <= 0 || f.RegimeShiftPlateau >= 1 {
			errs = append(errs, fmt.Errorf("forecast.regime_shift_plateau must be in (0,1) when regime_shift_flag is enabled, got %v", f.RegimeShiftPlateau))
		}
	}
	for i, q := range f.Quantiles {
		if q <= 0 || q >= 1 {
			errs = append(errs, fmt.Errorf("forecast.quantiles[%d] must be in (0,1), got %v", i, q))
		}
		if i > 0 && q <= f.Quantiles[i-1] {
			errs = append(errs, fmt.Errorf("forecast.quantiles must be strictly ascending"))
		}
	}
	if f.Enabled && len(f.Quantiles) < 2 {
		errs = append(errs, errors.New("forecast.quantiles needs at least a lower and upper level when enabled (the band must exist, doc 01 §3)"))
	}

	// Cross-field invariant (doc 14 §1.3): retracted edges must stay queryable for
	// at least the longest evaluation window any walk can reach back through — the
	// default co-occurrence window plus the late-sample watermark.
	minHorizon := p.Observation.DefaultCooccurrenceWindow.Duration() + p.Observation.Watermark.Duration()
	if p.Identity.RetractedEdgeHorizon.Duration() < minHorizon {
		errs = append(errs, fmt.Errorf(
			"identity.retracted_edge_horizon (%s) must be >= default_cooccurrence_window + watermark (%s)",
			p.Identity.RetractedEdgeHorizon, minHorizon))
	}
	// The full-tombstone window must cover the late-sample watermark (doc 14 §1.4).
	if p.Identity.Tombstone.FullRetention.Duration() < p.Observation.Watermark.Duration() {
		errs = append(errs, fmt.Errorf(
			"identity.tombstone.full_retention (%s) must be >= observation.watermark (%s)",
			p.Identity.Tombstone.FullRetention, p.Observation.Watermark))
	}

	return errors.Join(errs...)
}
