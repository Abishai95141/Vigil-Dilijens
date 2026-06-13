package governance

import (
	"fmt"
	"sort"
	"strings"
)

// Staged rollout + rollback (doc 12 §3.5). Releases roll out in stages — reference
// clusters, canary customers, fleet — with the harness's continuous regression and
// LIVE HEALTH SIGNALS (mis-join rate from 03, binding-QA statuses from 04, finding-
// volume diffs from 07) checked at each stage. Rollback is re-pinning to the prior
// immutable release plus re-binding; because findings stamp their graph version,
// post-rollback behaviour is cleanly attributable.
//
// The exit gate (doc 12 §7 M5 / doc 13 Phase-2): a seeded bad release is caught at
// canary and rolled back cleanly in exercise. The health-signal evaluation here is
// pure (operates on measured numbers), so the exercise is deterministic and
// repeatable; the numbers themselves come from real binding + detection runs.

// Stage is a rollout stage; rollout advances reference → canary → fleet.
type Stage string

const (
	StageReference Stage = "reference"
	StageCanary    Stage = "canary"
	StageFleet     Stage = "fleet"
)

func nextStage(s Stage) (Stage, bool) {
	switch s {
	case StageReference:
		return StageCanary, true
	case StageCanary:
		return StageFleet, true
	default:
		return StageFleet, false
	}
}

// SignalKind says how a health signal's candidate value is judged against baseline.
type SignalKind string

const (
	// AbsoluteMax — the candidate must be ≤ Limit (e.g. mis-join rate must be 0).
	AbsoluteMax SignalKind = "absolute-max"
	// RatioVsBaseline — the candidate must be ≤ baseline × Limit (e.g. finding
	// volume must not more than double; a too-low default bar explodes this).
	RatioVsBaseline SignalKind = "ratio-vs-baseline"
)

// HealthSignal is one continuous-regression / live-health measurement compared
// across the upgrade at a rollout stage (doc 12 §3.5).
type HealthSignal struct {
	Name      string     // mis-join-rate | binding-qa-failed | finding-volume | backtest-coverage
	Kind      SignalKind //
	Baseline  float64    // measured under the prior release
	Candidate float64    // measured under the candidate release (same inputs)
	Limit     float64    // AbsoluteMax: max candidate; RatioVsBaseline: max candidate/baseline
}

// Healthy reports whether the candidate measurement is within the signal's tolerance.
func (h HealthSignal) Healthy() bool {
	switch h.Kind {
	case AbsoluteMax:
		return h.Candidate <= h.Limit+1e-9
	case RatioVsBaseline:
		if h.Baseline <= 0 {
			// No baseline to compare against: any nonzero candidate is judged by an
			// absolute floor of Limit (so 0→many still trips when Limit < candidate).
			return h.Candidate <= h.Limit+1e-9
		}
		return h.Candidate <= h.Baseline*h.Limit+1e-9
	default:
		return false
	}
}

// Explain renders the signal's verdict in operator-readable terms.
func (h HealthSignal) Explain() string {
	verdict := "ok"
	if !h.Healthy() {
		verdict = "TRIPPED"
	}
	switch h.Kind {
	case AbsoluteMax:
		return fmt.Sprintf("%s: candidate %.4g (max %.4g) — %s", h.Name, h.Candidate, h.Limit, verdict)
	case RatioVsBaseline:
		ratio := 0.0
		if h.Baseline > 0 {
			ratio = h.Candidate / h.Baseline
		}
		return fmt.Sprintf("%s: baseline %.4g → candidate %.4g (×%.2f, max ×%.2f) — %s",
			h.Name, h.Baseline, h.Candidate, ratio, h.Limit, verdict)
	default:
		return h.Name + ": unknown signal kind"
	}
}

// StageReport is a stage gate's verdict over its health signals.
type StageReport struct {
	Stage   Stage
	Signals []HealthSignal
	Healthy bool
	Tripped []string // names of tripped signals
}

// EvaluateStage checks all health signals at a stage; a single tripped signal makes
// the stage unhealthy (doc 12 §3.5: continuous regression + live health checked AT
// EACH STAGE — a bad release does not advance).
func EvaluateStage(stage Stage, signals []HealthSignal) StageReport {
	rep := StageReport{Stage: stage, Signals: signals, Healthy: true}
	// A stage with NO health signals cannot be confirmed healthy — advancing on an
	// empty check is a silent gap (doc 12 §3.5: regression + live health are checked
	// AT EACH STAGE; "nothing checked" is not "healthy").
	if len(signals) == 0 {
		rep.Healthy = false
		rep.Tripped = []string{"no-health-signals"}
		return rep
	}
	for _, s := range signals {
		if !s.Healthy() {
			rep.Healthy = false
			rep.Tripped = append(rep.Tripped, s.Name)
		}
	}
	sort.Strings(rep.Tripped)
	return rep
}

// Transition is one recorded rollout decision (the audit trail).
type Transition struct {
	Stage  Stage
	Action string // advanced | held-and-rolled-back | completed
	Reason string
}

// RolloutState tracks a release's progression through the stages (doc 12 §3.7
// "rollout stage state"). It is the audit record of the staged rollout exercise.
type RolloutState struct {
	Release      string // the candidate release being rolled out
	PriorRelease string // the immutable release to roll back to
	Stage        Stage
	Active       string // the release currently pinned (flips to PriorRelease on rollback)
	RolledBack   bool
	History      []Transition
}

// NewRollout begins a rollout of `release`, with `prior` as the rollback target.
func NewRollout(release, prior string) *RolloutState {
	return &RolloutState{
		Release: release, PriorRelease: prior,
		Stage: StageReference, Active: release,
	}
}

// Step applies a stage report: advance to the next stage if healthy, or HOLD and
// roll back to the prior immutable release if any signal tripped (doc 12 §3.5).
// Returns true if the rollout should continue (more stages remain).
func (s *RolloutState) Step(rep StageReport) bool {
	// Terminal after a rollback: a rolled-back rollout never advances again (the
	// candidate is withdrawn; the prior release is active).
	if s.RolledBack {
		return false
	}
	if !rep.Healthy {
		s.RolledBack = true
		s.Active = s.PriorRelease
		s.History = append(s.History, Transition{
			Stage:  rep.Stage,
			Action: "held-and-rolled-back",
			Reason: fmt.Sprintf("health signals tripped at %s: %s — re-pinned to %s + re-bind",
				rep.Stage, strings.Join(rep.Tripped, ", "), s.PriorRelease),
		})
		return false
	}
	next, more := nextStage(s.Stage)
	if !more {
		s.History = append(s.History, Transition{Stage: s.Stage, Action: "completed",
			Reason: "all stages healthy; release is fleet-wide"})
		return false
	}
	s.History = append(s.History, Transition{Stage: s.Stage, Action: "advanced",
		Reason: "healthy; advancing to " + string(next)})
	s.Stage = next
	return true
}

// Summary renders the rollout audit trail.
func (s *RolloutState) Summary() string {
	var b strings.Builder
	status := "active=" + s.Active
	if s.RolledBack {
		status = "ROLLED BACK to " + s.PriorRelease
	}
	fmt.Fprintf(&b, "Rollout of %s (prior %s) — %s\n", s.Release, s.PriorRelease, status)
	for _, t := range s.History {
		fmt.Fprintf(&b, "  [%s] %s — %s\n", t.Stage, t.Action, t.Reason)
	}
	return b.String()
}
