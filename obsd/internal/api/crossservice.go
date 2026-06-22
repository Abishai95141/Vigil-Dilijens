package api

import (
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/flow"
)

// CrossServiceView is the v2 cross-service cascade surface (doc 15 phase F): the
// warm-path chain that names the most-upstream degraded WORKLOAD and its impacted
// callers over OBSERVED-FLOW edges, with the ONE authored relation surfaced
// verbatim. It is a JOIN, never a fusion — the MEASURED degradation + MEASURED
// observed-flow edge + AUTHORED "why" + structural position sit side by side, each
// labelled (the underlying flow.Chain carries the per-part class tags; its charter-
// cleanliness is asserted by the flow tests and the backtest gate).
//
// Gate posture (doc 11 §3.5): this class is operator-visible because its backtest
// gate PASSED (root accuracy, caller recall/precision 1.000, zero false cascades on
// the live corpus — corpus/labels/flow-gate-crossservice.md). The surface still
// states its lane honestly: OFF when flow discovery is not running, quiet when no
// degraded callee has an impacted caller this tick.
type CrossServiceView struct {
	GeneratedAt time.Time `json:"generatedAt"`
	Class       string    `json:"class"`    // "MEASURED ⋈ AUTHORED (joined, never fused)"
	Enabled     bool      `json:"enabled"`  // flow discovery (doc 15) is running
	Active      bool      `json:"active"`   // a cross-service cascade is firing this tick
	Note        string    `json:"note"`     // honest lane state
	GateNote    string    `json:"gateNote"` // the doc 11 §3.5 gate posture

	// Chain is the joined, charter-clean cross-service chain when one is firing;
	// nil when the lane is OFF or quiet. Surfaced verbatim from the warm path so
	// the operator sees exactly what a replay of the captured topology reproduces.
	Chain *flow.Chain `json:"chain,omitempty"`

	// --- Phase E: the ANTICIPATORY (PROJECTED) lane (doc 15 phase E) ---
	// The cascade seeded from the gated early-warning lane: an upstream callee
	// FORECAST to cross soon, propagating a PROJECTED downstream-impact hypothesis
	// to its callers over the MEASURED flow edge + AUTHORED relation. A NEW PROJECTED
	// class, so it is NOT operator-visible until its own backtest gate passes (doc 11
	// §3.5): ProjectedActive stays false (and ProjectedChain nil) while gate-pending.
	ProjectedActive     bool        `json:"projectedActive"`
	ProjectedGatePassed bool        `json:"projectedGatePassed"` // the live gate has flipped (operator-visible)
	ProjectedNote       string      `json:"projectedNote"`
	ProjectedChain      *flow.Chain `json:"projectedChain,omitempty"`
}

const (
	crossServiceClass = "MEASURED ⋈ AUTHORED (joined, never fused)"
	// crossServiceGateNote states WHY this class may be surfaced (the gate rule).
	crossServiceGateNote = "Cross-service cascade (doc 15 phase D). Backtest gate PASSED " +
		"(doc 11 §3.5): root accuracy, caller recall + precision 1.000 with zero false " +
		"cascades over the live corpus. The chain JOINS a MEASURED observed-flow edge, the " +
		"MEASURED degradation finding, and the AUTHORED relation, each labelled — never " +
		"fused into one derived statement."
	crossServiceOffNote = "Flow discovery is OFF (obsd --flow-enabled not set): cross-service " +
		"dependency edges are not observed, so no cross-service cascade can be reported."
	crossServiceQuietNote = "Flow discovery ON; no cross-service cascade firing — no degraded " +
		"callee has an impacted caller over an observed-flow edge this tick."
	crossServiceActiveNote = "A cross-service cascade is firing: a degraded callee reaches " +
		"impacted callers over observed-flow edges. Each provenance class is shown side by " +
		"side, never fused into a single claim."

	// Phase E (anticipatory) lane notes.
	projectedOffNote = "Flow discovery is OFF: the anticipatory cross-service lane needs " +
		"observed-flow edges to propagate a forecast along."
	projectedGatePendingNote = "Anticipatory cross-service cascade (doc 15 phase E) is COMPUTED " +
		"every tick but NOT yet operator-visible: a new PROJECTED class ships only after its own " +
		"backtest gate (lead-time + confirm/refute calibration, doc 11 §3.5) passes."
	projectedQuietNote = "No anticipatory cross-service cascade: no upstream callee is currently " +
		"forecast to cross its bar with an impacted caller over an observed-flow edge."
	projectedActiveNote = "An anticipatory cross-service cascade is projected: an upstream callee " +
		"is FORECAST to cross its bar soon, so its callers are PROJECTED to be impacted soon — the " +
		"upstream forecast and downstream impact are PROJECTED (with bands), the flow edge MEASURED, " +
		"the why AUTHORED; each labelled, never fused, and no band collapses to a line."
)

// BuildCrossService renders the cross-service surface: the MEASURED lane (Phase D,
// gate-passed) from `chain`, and the PROJECTED anticipatory lane (Phase E) from
// `projected`. It never invents a cascade and never upgrades a class: OFF, quiet,
// gate-pending, and active are distinct, stated states. The projected chain is
// withheld (ProjectedActive=false, no ProjectedChain) until projectedGatePassed.
func BuildCrossService(chain *flow.Chain, projected *flow.Chain, enabled, projectedGatePassed bool, now time.Time) *CrossServiceView {
	v := &CrossServiceView{
		GeneratedAt: now, Class: crossServiceClass, Enabled: enabled,
		GateNote: crossServiceGateNote,
	}
	switch {
	case !enabled:
		v.Note = crossServiceOffNote
	case chain == nil:
		v.Note = crossServiceQuietNote
	default:
		v.Active = true
		v.Note = crossServiceActiveNote
		v.Chain = chain
	}
	// The anticipatory (PROJECTED) lane — gated separately from the MEASURED lane.
	v.ProjectedGatePassed = projectedGatePassed
	switch {
	case !enabled:
		v.ProjectedNote = projectedOffNote
	case !projectedGatePassed:
		// Gate-pending: the lane is computed but withheld from the operator (the
		// chain content is NOT surfaced — a new class is not visible before its gate).
		v.ProjectedNote = projectedGatePendingNote
	case projected == nil:
		v.ProjectedNote = projectedQuietNote
	default:
		v.ProjectedActive = true
		v.ProjectedNote = projectedActiveNote
		v.ProjectedChain = projected
	}
	return v
}
