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
)

// BuildCrossService renders the cross-service surface from the warm-path chain
// pointer (nil = quiet) and the flow-enabled flag. It never invents a cascade: an
// OFF lane and a quiet lane are distinct, stated states.
func BuildCrossService(chain *flow.Chain, enabled bool, now time.Time) *CrossServiceView {
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
	return v
}
