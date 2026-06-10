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
	Store       StoreParams       `yaml:"store"`
	Binding     BindingParams     `yaml:"binding"`
	Selection   SelectionParams   `yaml:"selection"`
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
	if p.Selection.TierBBudgetPerCycle <= 0 {
		errs = append(errs, fmt.Errorf("selection.tier_b_budget_per_cycle must be > 0, got %d", p.Selection.TierBBudgetPerCycle))
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
