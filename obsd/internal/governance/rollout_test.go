package governance

import "testing"

func TestHealthSignalAbsoluteMax(t *testing.T) {
	ok := HealthSignal{Name: "mis-join-rate", Kind: AbsoluteMax, Candidate: 0, Limit: 0}
	if !ok.Healthy() {
		t.Error("0 mis-joins should be healthy")
	}
	bad := HealthSignal{Name: "mis-join-rate", Kind: AbsoluteMax, Candidate: 3, Limit: 0}
	if bad.Healthy() {
		t.Error("3 mis-joins should trip")
	}
}

func TestHealthSignalRatio(t *testing.T) {
	ok := HealthSignal{Name: "finding-volume", Kind: RatioVsBaseline, Baseline: 10, Candidate: 15, Limit: 2.0}
	if !ok.Healthy() {
		t.Error("1.5× should be within a 2× tolerance")
	}
	explosion := HealthSignal{Name: "finding-volume", Kind: RatioVsBaseline, Baseline: 10, Candidate: 80, Limit: 2.0}
	if explosion.Healthy() {
		t.Error("8× finding-volume explosion should trip")
	}
}

func TestEvaluateStageTrips(t *testing.T) {
	rep := EvaluateStage(StageCanary, []HealthSignal{
		{Name: "mis-join-rate", Kind: AbsoluteMax, Candidate: 0, Limit: 0},
		{Name: "finding-volume", Kind: RatioVsBaseline, Baseline: 10, Candidate: 90, Limit: 2.0},
	})
	if rep.Healthy {
		t.Fatal("stage should be unhealthy with a tripped signal")
	}
	if len(rep.Tripped) != 1 || rep.Tripped[0] != "finding-volume" {
		t.Fatalf("expected finding-volume tripped, got %v", rep.Tripped)
	}
}

// TestSeededBadReleaseRolledBack is the DETERMINISTIC CORE of the Phase-2 exit-gate
// exercise (doc 12 §7 M5 / doc 13): a seeded bad release (a normative default-bar
// change that explodes finding volume) advances through reference, is caught at
// canary by the finding-volume health signal, and is rolled back cleanly to the
// prior immutable release. The live exercise drives these numbers from real
// detection runs; this test pins the rollout machinery's decision.
func TestSeededBadReleaseRolledBack(t *testing.T) {
	roll := NewRollout("v0.99.0-seeded-bad", "v0.3.0")

	// Reference stage: the bad release happens to look fine on the reference fixture
	// (the seeded fault needs the canary's live workloads to manifest).
	refHealthy := EvaluateStage(StageReference, []HealthSignal{
		{Name: "mis-join-rate", Kind: AbsoluteMax, Candidate: 0, Limit: 0},
		{Name: "binding-qa-failed", Kind: AbsoluteMax, Candidate: 0, Limit: 0},
		{Name: "finding-volume", Kind: RatioVsBaseline, Baseline: 4, Candidate: 4, Limit: 2.0},
	})
	if !roll.Step(refHealthy) {
		t.Fatal("reference stage should advance (continue)")
	}
	if roll.Stage != StageCanary {
		t.Fatalf("should be at canary, at %s", roll.Stage)
	}

	// Canary stage: the lowered default bar makes nearly every workload cross →
	// finding-volume explodes → the canary health gate trips → rollback.
	canary := EvaluateStage(StageCanary, []HealthSignal{
		{Name: "mis-join-rate", Kind: AbsoluteMax, Candidate: 0, Limit: 0},
		{Name: "binding-qa-failed", Kind: AbsoluteMax, Candidate: 0, Limit: 0},
		{Name: "finding-volume", Kind: RatioVsBaseline, Baseline: 5, Candidate: 47, Limit: 2.0},
	})
	cont := roll.Step(canary)
	if cont {
		t.Fatal("rollout should stop after rollback")
	}
	if !roll.RolledBack {
		t.Fatal("seeded bad release should have rolled back")
	}
	if roll.Active != "v0.3.0" {
		t.Fatalf("active release should be the prior pin v0.3.0, got %s", roll.Active)
	}
	// The audit trail must record the held-and-rolled-back at canary.
	last := roll.History[len(roll.History)-1]
	if last.Stage != StageCanary || last.Action != "held-and-rolled-back" {
		t.Fatalf("audit trail wrong: %+v", last)
	}
}

// TestEvaluateStageNoSignals pins the fix: a stage with zero health signals is NOT
// vacuously healthy — "nothing checked" cannot advance a rollout.
func TestEvaluateStageNoSignals(t *testing.T) {
	rep := EvaluateStage(StageCanary, nil)
	if rep.Healthy {
		t.Fatal("a stage with no health signals must not be healthy")
	}
	if len(rep.Tripped) != 1 || rep.Tripped[0] != "no-health-signals" {
		t.Fatalf("expected no-health-signals tripped, got %v", rep.Tripped)
	}
}

// TestStepTerminalAfterRollback pins the fix: once rolled back, Step never advances.
func TestStepTerminalAfterRollback(t *testing.T) {
	roll := NewRollout("v0.99", "v0.3.0")
	roll.Step(EvaluateStage(StageReference, []HealthSignal{{Name: "fv", Kind: RatioVsBaseline, Baseline: 5, Candidate: 99, Limit: 2}}))
	if !roll.RolledBack {
		t.Fatal("should have rolled back at reference")
	}
	// A subsequent healthy report must NOT resurrect the rollout.
	cont := roll.Step(EvaluateStage(StageCanary, []HealthSignal{{Name: "fv", Kind: AbsoluteMax, Candidate: 0, Limit: 0}}))
	if cont || !roll.RolledBack || roll.Active != "v0.3.0" {
		t.Fatalf("a rolled-back rollout must stay terminal: cont=%v active=%s", cont, roll.Active)
	}
}

func TestCleanReleaseReachesFleet(t *testing.T) {
	roll := NewRollout("v0.4.0", "v0.3.0")
	good := func(stage Stage) StageReport {
		return EvaluateStage(stage, []HealthSignal{
			{Name: "mis-join-rate", Kind: AbsoluteMax, Candidate: 0, Limit: 0},
			{Name: "finding-volume", Kind: RatioVsBaseline, Baseline: 5, Candidate: 5, Limit: 2.0},
		})
	}
	for roll.Step(good(roll.Stage)) {
	}
	if roll.RolledBack {
		t.Fatal("clean release should not roll back")
	}
	if roll.Active != "v0.4.0" {
		t.Fatalf("clean release should stay active, got %s", roll.Active)
	}
	last := roll.History[len(roll.History)-1]
	if last.Action != "completed" {
		t.Fatalf("clean release should complete at fleet, got %+v", last)
	}
}
