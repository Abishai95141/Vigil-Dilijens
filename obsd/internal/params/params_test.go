package params

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestDefaultLoadsAndValidates(t *testing.T) {
	p, err := Default()
	if err != nil {
		t.Fatalf("Default() error: %v", err)
	}

	// Spot-check values against doc 14 §5 — these are the contract with the blueprint.
	checks := []struct {
		name string
		got  time.Duration
		want time.Duration
	}{
		{"scrape.interval", p.Scrape.Interval.Duration(), 15 * time.Second},
		{"identity.reconciliation", p.Identity.Reconciliation.Duration(), 30 * time.Second},
		{"identity.retracted_edge_horizon", p.Identity.RetractedEdgeHorizon.Duration(), 30 * time.Minute},
		{"identity.tombstone.full_retention", p.Identity.Tombstone.FullRetention.Duration(), 15 * time.Minute},
		{"identity.tombstone.stub_retention", p.Identity.Tombstone.StubRetention.Duration(), 24 * time.Hour},
		{"observation.evaluation_tick", p.Observation.EvaluationTick.Duration(), 15 * time.Second},
		{"observation.watermark", p.Observation.Watermark.Duration(), 30 * time.Second},
		{"observation.rate_window", p.Observation.RateWindow.Duration(), 5 * time.Minute},
		{"observation.default_cooccurrence_window", p.Observation.DefaultCooccurrenceWindow.Duration(), 10 * time.Minute},
		{"store.hot_ring", p.Store.HotRing.Duration(), 60 * time.Minute},
		{"store.warm_retention", p.Store.WarmRetention.Duration(), 168 * time.Hour},
		{"store.segment_duration", p.Store.SegmentDuration.Duration(), 2 * time.Hour},
		{"store.fsync_batch", p.Store.FsyncBatch.Duration(), 1 * time.Second},
		{"binding.bar_reresolution", p.Binding.BarReResolution.Duration(), 30 * time.Second},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %s, want %s", c.name, c.got, c.want)
		}
	}

	if p.Version != "0" {
		t.Errorf("version = %q, want %q", p.Version, "0")
	}
	if p.Profile != "dev" {
		t.Errorf("profile = %q, want dev", p.Profile)
	}
	if p.Identity.Tombstone.MaxEntries != 250000 {
		t.Errorf("tombstone.max_entries = %d, want 250000", p.Identity.Tombstone.MaxEntries)
	}
	if p.Selection.TierBBudgetPerCycle != 50 {
		t.Errorf("tier_b_budget_per_cycle = %d, want 50", p.Selection.TierBBudgetPerCycle)
	}
	if p.Observation.WellAboveFactor != 1.10 {
		t.Errorf("well_above_factor = %v, want 1.10", p.Observation.WellAboveFactor)
	}
}

func TestEdgeBudgets(t *testing.T) {
	p, err := Default()
	if err != nil {
		t.Fatalf("Default() error: %v", err)
	}
	want := map[string]time.Duration{
		"runs-on":    90 * time.Second,
		"selects":    90 * time.Second,
		"mounts":     120 * time.Second,
		"owns":       600 * time.Second,
		"node-lease": 40 * time.Second,
	}
	for edge, w := range want {
		got, ok := p.Identity.EdgeBudgets[edge]
		if !ok {
			t.Errorf("edge_budgets missing %q", edge)
			continue
		}
		if got.Duration() != w {
			t.Errorf("edge_budgets[%q] = %s, want %s", edge, got, w)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	p, err := Default()
	if err != nil {
		t.Fatalf("Default() error: %v", err)
	}
	out, err := yaml.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Params
	if err := yaml.Unmarshal(out, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := back.Validate(); err != nil {
		t.Fatalf("round-tripped params invalid: %v", err)
	}
	if back.Scrape.Interval != p.Scrape.Interval ||
		back.Identity.Tombstone.MaxEntries != p.Identity.Tombstone.MaxEntries ||
		back.Selection.TierBBudgetPerCycle != p.Selection.TierBBudgetPerCycle {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", back, p)
	}
}

func TestLoadOverlay(t *testing.T) {
	dir := t.TempDir()
	override := filepath.Join(dir, "override.yaml")
	// Only override two values; everything else must retain its default.
	content := "" +
		"scrape:\n" +
		"  interval: 30s\n" +
		"selection:\n" +
		"  tier_b_budget_per_cycle: 25\n"
	if err := os.WriteFile(override, []byte(content), 0o644); err != nil {
		t.Fatalf("write override: %v", err)
	}

	p, err := Load(override)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if p.Scrape.Interval.Duration() != 30*time.Second {
		t.Errorf("overlaid scrape.interval = %s, want 30s", p.Scrape.Interval)
	}
	if p.Selection.TierBBudgetPerCycle != 25 {
		t.Errorf("overlaid tier_b_budget_per_cycle = %d, want 25", p.Selection.TierBBudgetPerCycle)
	}
	// Untouched defaults survive the overlay.
	if p.Observation.RateWindow.Duration() != 5*time.Minute {
		t.Errorf("rate_window = %s, want default 5m after overlay", p.Observation.RateWindow)
	}
	if _, ok := p.Identity.EdgeBudgets["runs-on"]; !ok {
		t.Error("edge_budgets lost during overlay")
	}
}

func TestLoadEmptyPathEqualsDefault(t *testing.T) {
	d, err := Default()
	if err != nil {
		t.Fatalf("Default(): %v", err)
	}
	l, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\"): %v", err)
	}
	if l.Scrape.Interval != d.Scrape.Interval || l.Profile != d.Profile {
		t.Errorf("Load(\"\") != Default()")
	}
}

func TestInvalidDurationRejected(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.yaml")
	// "7d" is intentionally invalid: Go's duration grammar has no day unit.
	if err := os.WriteFile(bad, []byte("store:\n  warm_retention: 7d\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Load(bad); err == nil {
		t.Fatal("expected error for invalid duration '7d', got nil")
	}
}

func TestValidateCatchesBadValues(t *testing.T) {
	cases := map[string]func(*Params){
		"zero max_entries":         func(p *Params) { p.Identity.Tombstone.MaxEntries = 0 },
		"missing edge budget":      func(p *Params) { delete(p.Identity.EdgeBudgets, "runs-on") },
		"bad profile":              func(p *Params) { p.Profile = "staging" },
		"well_above <= 1":          func(p *Params) { p.Observation.WellAboveFactor = 1.0 },
		"horizon below min":        func(p *Params) { p.Identity.RetractedEdgeHorizon = Duration(time.Second) },
		"full_retention too small": func(p *Params) { p.Identity.Tombstone.FullRetention = Duration(time.Second) },
		"zero tier_b budget":       func(p *Params) { p.Selection.TierBBudgetPerCycle = 0 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p, err := Default()
			if err != nil {
				t.Fatalf("Default(): %v", err)
			}
			mutate(&p)
			if err := p.Validate(); err == nil {
				t.Errorf("Validate() accepted invalid params (%s)", name)
			}
		})
	}
}
