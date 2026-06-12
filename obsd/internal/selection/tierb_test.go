package selection

import (
	"testing"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/forecast"
)

func tbTarget(cei, metric string, precursor bool) forecast.Target {
	t := forecast.Target{CEIKey: cei, Metric: metric, SeriesKind: "gauge", BarValue: 1, Direction: "above"}
	if precursor {
		t.PrecursorPhenomena = []string{"PHEN_X"}
	}
	return t
}

// The ceiling is respected, precursors outrank non-precursors (v1 stated
// policy pre-06-M4), and everything past the ceiling is PUBLISHED, never
// silently dropped (doc 06 §3.3/M5).
func TestSelectTierBBudget(t *testing.T) {
	targets := []forecast.Target{
		tbTarget("i|cl|a|Pod|z|uz", "m", false),
		tbTarget("i|cl|a|Pod|a|ua", "m", true),
		tbTarget("i|cl|a|Pod|b|ub", "m", true),
		tbTarget("i|cl|a|Pod|c|uc", "m", false),
	}
	sel := SelectTierB(targets, 2)
	if len(sel.Budgeted) != 2 || len(sel.Unbudgeted) != 2 {
		t.Fatalf("ceiling 2 must split 2/2: %d/%d", len(sel.Budgeted), len(sel.Unbudgeted))
	}
	if !sel.Budgeted[0].Target.Precursor() || !sel.Budgeted[1].Target.Precursor() {
		t.Error("precursor targets must rank inside the budget first")
	}
	if sel.Budgeted[0].Target.CEIKey != "i|cl|a|Pod|a|ua" || sel.Budgeted[1].Target.CEIKey != "i|cl|a|Pod|b|ub" {
		t.Errorf("canonical tiebreak violated: %+v", sel.Budgeted)
	}
	if sel.Unbudgeted[0].Rank != 3 || !sel.Unbudgeted[0].Budgeted == false {
		// the unbudgeted are ranked, listed, and marked — the audit surface
		t.Errorf("unbudgeted must stay ranked + visible: %+v", sel.Unbudgeted[0])
	}
	for _, r := range sel.Budgeted {
		if r.Reason != "precursor" {
			t.Errorf("reason code must say WHY: %+v", r)
		}
	}
}

func TestSelectTierBDeterministic(t *testing.T) {
	targets := []forecast.Target{
		tbTarget("i|cl|a|Pod|b|ub", "m2", false),
		tbTarget("i|cl|a|Pod|b|ub", "m1", false),
		tbTarget("i|cl|a|Pod|a|ua", "m", false),
	}
	a, b := SelectTierB(targets, 2), SelectTierB(targets, 2)
	for i := range a.Budgeted {
		if a.Budgeted[i].Target.CEIKey != b.Budgeted[i].Target.CEIKey || a.Budgeted[i].Target.Metric != b.Budgeted[i].Target.Metric {
			t.Fatal("selection must be deterministic")
		}
	}
	if a.Budgeted[0].Target.CEIKey != "i|cl|a|Pod|a|ua" || a.Budgeted[1].Target.Metric != "m1" {
		t.Errorf("canonical (CEI, metric) order violated: %+v", a.Budgeted)
	}
}

// Ceiling 0 or negative = no ceiling configured at THIS call site (the params
// validator enforces a positive budget for the live path; the zero behaviour
// keeps replay/backtest callers explicit).
func TestSelectTierBNoCeiling(t *testing.T) {
	sel := SelectTierB([]forecast.Target{tbTarget("i|cl|a|Pod|a|ua", "m", false)}, 0)
	if len(sel.Budgeted) != 1 || len(sel.Unbudgeted) != 0 {
		t.Fatalf("no ceiling must budget everything: %+v", sel)
	}
}
