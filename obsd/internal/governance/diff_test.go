package governance

import (
	"strings"
	"testing"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/binding"
)

func bar(v float64, src binding.BarSource, flagged bool) *binding.ResolvedBar {
	return &binding.ResolvedBar{Value: v, Source: src, Flagged: flagged, Direction: "above", Unit: "bytes"}
}

func TestBindingDiffBarReResolution(t *testing.T) {
	from := &binding.Result{
		GraphVersion: "sha256:from",
		Bindings: []binding.Binding{
			{CEIKey: "i|c|ns|d|cont|uid1", Entity: "Container", Container: "cont", RuleID: "THR_MEM", Metric: "working_set",
				State: binding.StateBound, Validation: binding.ValidationVerified, Bar: bar(100, binding.SourceConfig, false)},
		},
	}
	to := &binding.Result{
		GraphVersion: "sha256:to",
		Bindings: []binding.Binding{
			// Same binding, bar lowered + validation moved to suspect.
			{CEIKey: "i|c|ns|d|cont|uid1", Entity: "Container", Container: "cont", RuleID: "THR_MEM", Metric: "working_set",
				State: binding.StateBound, Validation: binding.ValidationSuspect, Bar: bar(80, binding.SourceConfig, false)},
			// A gained binding (new rule fanned out a new variable).
			{CEIKey: "i|c|ns|d|cont|uid1", Entity: "Container", Container: "cont", RuleID: "THR_CPU", Metric: "cpu",
				State: binding.StateBound, Validation: binding.ValidationVerified, Bar: bar(500, binding.SourceConfig, false)},
		},
	}
	d := BindingDiff("boutique", from, to)
	if !d.HasEffect() {
		t.Fatal("expected an operator-visible effect")
	}
	if len(d.GainedBindings) != 1 {
		t.Errorf("want 1 gained binding, got %d", len(d.GainedBindings))
	}
	if len(d.BarChanges) != 1 || d.BarChanges[0].FromValue != 100 || d.BarChanges[0].ToValue != 80 {
		t.Errorf("want bar 100→80, got %+v", d.BarChanges)
	}
	if len(d.Validation) != 1 || d.Validation[0].To != "suspect" {
		t.Errorf("want validation→suspect, got %+v", d.Validation)
	}
	h := d.Human()
	if !strings.Contains(h, "100") || !strings.Contains(h, "80") {
		t.Errorf("Human() should show the bar move: %s", h)
	}
}

func TestBindingDiffNoEffect(t *testing.T) {
	b := binding.Binding{CEIKey: "i|x", RuleID: "R", Metric: "m", State: binding.StateBound,
		Validation: binding.ValidationVerified, Bar: bar(10, binding.SourceConfig, false)}
	from := &binding.Result{GraphVersion: "a", Bindings: []binding.Binding{b}}
	to := &binding.Result{GraphVersion: "b", Bindings: []binding.Binding{b}}
	d := BindingDiff("c", from, to)
	if d.HasEffect() {
		t.Fatalf("binding-neutral upgrade should report no effect: %+v", d)
	}
	if !strings.Contains(d.Human(), "no operator-visible effect") {
		t.Errorf("Human() should state no effect: %s", d.Human())
	}
}

func TestBindingDiffLostBinding(t *testing.T) {
	b := binding.Binding{CEIKey: "i|x", RuleID: "R", Metric: "m", State: binding.StateBound, Bar: bar(10, binding.SourceConfig, false)}
	from := &binding.Result{GraphVersion: "a", Bindings: []binding.Binding{b}}
	to := &binding.Result{GraphVersion: "b", Bindings: nil}
	d := BindingDiff("c", from, to)
	if len(d.LostBindings) != 1 {
		t.Fatalf("want 1 lost binding, got %d", len(d.LostBindings))
	}
}

// TestBindingDiffMultiContainerNoCollision pins the container-key fix: two containers
// in ONE pod (same CEIKey) with different bars must each be diffed, not collapsed.
func TestBindingDiffMultiContainerNoCollision(t *testing.T) {
	cei := "i|c|ns|d|pod|uid1"
	from := &binding.Result{GraphVersion: "a", Bindings: []binding.Binding{
		{CEIKey: cei, Entity: "Container", Container: "app", RuleID: "THR_MEM", Metric: "ws", State: binding.StateBound, Bar: bar(100, binding.SourceConfig, false)},
		{CEIKey: cei, Entity: "Container", Container: "sidecar", RuleID: "THR_MEM", Metric: "ws", State: binding.StateBound, Bar: bar(50, binding.SourceConfig, false)},
	}}
	to := &binding.Result{GraphVersion: "b", Bindings: []binding.Binding{
		{CEIKey: cei, Entity: "Container", Container: "app", RuleID: "THR_MEM", Metric: "ws", State: binding.StateBound, Bar: bar(100, binding.SourceConfig, false)},    // unchanged
		{CEIKey: cei, Entity: "Container", Container: "sidecar", RuleID: "THR_MEM", Metric: "ws", State: binding.StateBound, Bar: bar(40, binding.SourceConfig, false)}, // sidecar bar moved
	}}
	d := BindingDiff("c", from, to)
	if len(d.BarChanges) != 1 || d.BarChanges[0].FromValue != 50 {
		t.Fatalf("the sidecar bar change must be detected (not collapsed with app): %+v", d.BarChanges)
	}
}

// TestObservabilityShiftRemoval pins the removed-phenomenon fix.
func TestObservabilityShiftRemoval(t *testing.T) {
	from := &binding.ObservabilityReport{PerPhenomenon: []binding.PhenomenonCoverage{
		{PhenomenonID: "PHEN_GONE", Observability: "full"},
	}}
	to := &binding.ObservabilityReport{PerPhenomenon: []binding.PhenomenonCoverage{}}
	d := &Diff{Customer: "c"}
	d.AddObservabilityShift(from, to)
	if len(d.Observability) != 1 || d.Observability[0].To != "absent" {
		t.Fatalf("a removed phenomenon must shift to absent, got %+v", d.Observability)
	}
}

func TestObservabilityShift(t *testing.T) {
	from := &binding.ObservabilityReport{PerPhenomenon: []binding.PhenomenonCoverage{
		{PhenomenonID: "PHEN_A", Observability: "partial"},
		{PhenomenonID: "PHEN_B", Observability: "full"},
	}}
	to := &binding.ObservabilityReport{PerPhenomenon: []binding.PhenomenonCoverage{
		{PhenomenonID: "PHEN_A", Observability: "full"}, // improved
		{PhenomenonID: "PHEN_B", Observability: "full"}, // unchanged
		{PhenomenonID: "PHEN_C", Observability: "none"}, // appeared
	}}
	d := &Diff{Customer: "c", FromVersion: "a", ToVersion: "b"}
	d.AddObservabilityShift(from, to)
	// PHEN_A shifted partial→full; PHEN_C appeared (absent→none). PHEN_B unchanged.
	if len(d.Observability) != 2 {
		t.Fatalf("want 2 observability shifts, got %d: %+v", len(d.Observability), d.Observability)
	}
}
