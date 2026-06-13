package api

import (
	"testing"
	"time"
)

func warnView(enabled bool, warns []WarningCard, sil []SilenceRow) *WarningsView {
	return &WarningsView{Enabled: enabled, Warnings: warns, Silences: sil}
}

func card(cei, metric string, basis time.Time) WarningCard {
	return WarningCard{EntityCEI: cei, Metric: metric, BasisAt: basis, CrossAt: basis.Add(10 * time.Minute)}
}

// The core property: a real, approaching entity whose projection is MARGINAL
// (present one cycle, silenced the next) must NOT flicker on the surface — it is
// held as `aging` between refreshes and only clears after clearAfter of quiet.
func TestDebouncerStabilisesFlicker(t *testing.T) {
	d := NewWarningDebouncer(90 * time.Second)
	t0 := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	cei, m := "i|c|ns|Pod|leaky|uid", "container_memory_working_set_bytes"

	// cycle 1 (t0): fresh warning
	v1 := warnView(true, []WarningCard{card(cei, m, t0)}, nil)
	d.Step(v1, t0)
	if len(v1.Warnings) != 1 || v1.Warnings[0].Aging {
		t.Fatalf("cycle1: want 1 fresh (non-aging) warning, got %+v", v1.Warnings)
	}

	// cycle 2 (t0+30s): the projection went marginal — NO fresh warning, but a
	// silence for the same entity. It must be HELD as aging, and NOT appear in
	// the silence list (no double-presentation).
	t1 := t0.Add(30 * time.Second)
	v2 := warnView(true, nil, []SilenceRow{{EntityCEI: cei, Metric: m, Reason: "band-too-wide"}})
	d.Step(v2, t1)
	if len(v2.Warnings) != 1 || !v2.Warnings[0].Aging {
		t.Fatalf("cycle2: want 1 AGING warning (no flicker), got %+v", v2.Warnings)
	}
	if len(v2.Silences) != 0 {
		t.Fatalf("cycle2: aging entity must not also be a silence, got %+v", v2.Silences)
	}

	// cycle 3 (t0+60s): fresh again → back to non-aging, FirstSeenAt preserved.
	t2 := t0.Add(60 * time.Second)
	v3 := warnView(true, []WarningCard{card(cei, m, t2)}, nil)
	d.Step(v3, t2)
	if len(v3.Warnings) != 1 || v3.Warnings[0].Aging {
		t.Fatalf("cycle3: want fresh non-aging, got %+v", v3.Warnings)
	}
	if !v3.Warnings[0].FirstSeenAt.Equal(t0) {
		t.Fatalf("cycle3: FirstSeenAt must persist from cycle1 (%s), got %s", t0, v3.Warnings[0].FirstSeenAt)
	}
}

// After clearAfter of marginal silence (the approach receded but never crossed),
// the warning is EVICTED — it does not linger forever.
func TestDebouncerEvictsAfterQuiet(t *testing.T) {
	d := NewWarningDebouncer(90 * time.Second)
	t0 := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	cei, m := "i|c|ns|Pod|leaky|uid", "container_memory_working_set_bytes"
	marginal := []SilenceRow{{EntityCEI: cei, Metric: m, Reason: "band-too-wide"}}

	d.Step(warnView(true, []WarningCard{card(cei, m, t0)}, nil), t0)
	// marginal-silence cycle within the window → still held (aging)
	v := warnView(true, nil, marginal)
	d.Step(v, t0.Add(60*time.Second))
	if len(v.Warnings) != 1 {
		t.Fatalf("within window: want held, got %+v", v.Warnings)
	}
	// past clearAfter → evicted
	v = warnView(true, nil, marginal)
	d.Step(v, t0.Add(91*time.Second))
	if len(v.Warnings) != 0 {
		t.Fatalf("past clearAfter: want evicted, got %+v", v.Warnings)
	}
}

// A confirmed crossing is NOT flicker: a warning silenced this cycle with
// `already-crossed` must be dropped IMMEDIATELY (released to the MEASURED lane),
// never held as a future-tense projection — otherwise PROJECTED contradicts
// MEASURED at the handoff (the audit's finding).
func TestDebouncerDropsAlreadyCrossed(t *testing.T) {
	d := NewWarningDebouncer(90 * time.Second)
	t0 := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	cei, m := "i|c|ns|Pod|leaky|uid", "container_memory_working_set_bytes"

	d.Step(warnView(true, []WarningCard{card(cei, m, t0)}, nil), t0)
	// next cycle: the bar is crossed NOW → already-crossed silence (within window)
	v := warnView(true, nil, []SilenceRow{{EntityCEI: cei, Metric: m, Reason: "already-crossed"}})
	d.Step(v, t0.Add(30*time.Second))
	if len(v.Warnings) != 0 {
		t.Fatalf("already-crossed must drop the warning immediately, got %+v", v.Warnings)
	}
	// the already-crossed silence stays visible (it is a real state, not a held warning)
	if len(v.Silences) != 1 || v.Silences[0].Reason != "already-crossed" {
		t.Fatalf("already-crossed silence should remain in the accounting, got %+v", v.Silences)
	}
}

// A disabled lane (the gate-off state) is never touched — no held warnings leak.
func TestDebouncerNoOpWhenDisabled(t *testing.T) {
	d := NewWarningDebouncer(90 * time.Second)
	t0 := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	v := warnView(false, nil, []SilenceRow{{EntityCEI: "x", Metric: "y", Reason: "z"}})
	d.Step(v, t0)
	if len(v.Warnings) != 0 || len(v.Silences) != 1 {
		t.Fatalf("disabled view must be untouched, got warnings=%v silences=%v", v.Warnings, v.Silences)
	}
}
