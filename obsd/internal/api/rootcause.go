package api

import (
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/flow"
)

// RootCauseChainView is the transitive root-cause chain surface (doc 15 cap. B): the
// one-hop cross-service cascade made TRANSITIVE. It names, per connected degraded
// component, the most-upstream degraded WORKLOAD and the ORDERED chain of impacted
// downstream workloads, walked over OBSERVED-FLOW edges and oriented ONLY by the
// AUTHORED relation. Each node carries its OWN measured phenomenon; the only "why" is
// the verbatim authored note. A JOIN, never a fusion — and never a "X caused Y".
//
// Honest lane state, exactly like the cross-service surface: OFF when flow discovery is
// not running; quiet when no transitive chain forms this tick; active otherwise. Silent
// intermediates and hop ceilings are surfaced as the chains' own Gaps (never hidden).
type RootCauseChainView struct {
	GeneratedAt time.Time    `json:"generatedAt"`
	Class       string       `json:"class"`   // "MEASURED ⋈ AUTHORED (joined, never fused)"
	Enabled     bool         `json:"enabled"` // flow discovery (doc 15) is running
	Active      bool         `json:"active"`  // at least one transitive chain is firing this tick
	Note        string       `json:"note"`    // honest lane state
	Chains      []flow.Chain `json:"chains,omitempty"`

	// --- doc 15 cap. D: the multi-hop PROJECTED cascade (forecast the ripple) ---
	// A forecast root whose ripple is PROJECTED to its transitive callers, each carrying
	// the root's band INHERITED and WIDENED per hop. A NEW PROJECTED class, so it is NOT
	// operator-visible until its own gate is satisfied by a REAL 2-hop lead+confirm capture
	// (doc 11 §3.5): ProjectedActive stays false (and ProjectedChains nil) while gate-pending,
	// exactly the Phase-E posture. The lane still COMPUTES every tick (off-digest).
	ProjectedActive     bool         `json:"projectedActive"`
	ProjectedGatePassed bool         `json:"projectedGatePassed"` // the live 2-hop gate has flipped (operator-visible)
	ProjectedNote       string       `json:"projectedNote"`
	ProjectedChains     []flow.Chain `json:"projectedChains,omitempty"`
}

const (
	rootCauseClass   = "MEASURED ⋈ AUTHORED (joined, never fused)"
	rootCauseOffNote = "Flow discovery is OFF (obsd --flow-enabled not set): dependency edges " +
		"are not observed, so no transitive dependency chain can be reconstructed."
	rootCauseQuietNote = "Flow discovery ON; no transitive dependency chain this tick — no run of " +
		"flow-adjacent degraded workloads forms a multi-node chain (independent faults stay separate)."
	rootCauseActiveNote = "A transitive dependency chain is reconstructed: MEASURED-degraded workloads " +
		"stitched into an ORDERED chain over observed-flow edges, oriented only by the authored relation. " +
		"Each node carries its own measured phenomenon; the why is authored; silent intermediates are " +
		"stated gaps, never bridged. Each provenance class sits side by side, never fused into one claim."

	// doc 15 cap. D — the multi-hop projected lane's honest states.
	rootCauseProjOffNote = "Flow discovery is OFF: the multi-hop projected cascade needs observed-flow " +
		"edges to ripple a forecast along."
	rootCauseProjPendingNote = "Multi-hop projected cascade (doc 15 cap. D) is COMPUTED every tick but NOT " +
		"yet operator-visible: a new PROJECTED class ships only after a real 2-hop lead+confirm capture " +
		"validates it on the cluster (doc 11 §3.5). The deterministic producer gate (band-widening, one " +
		"forecast root) already passes."
	rootCauseProjQuietNote = "No multi-hop projected cascade: no forecast root has a caller over an " +
		"observed-flow edge this tick."
	rootCauseProjActiveNote = "A multi-hop projected cascade is forecast: a single forecast root's ripple " +
		"reaches its transitive callers over MEASURED flow edges, each carrying the root's band INHERITED " +
		"and WIDENED per hop. The forecast root and every downstream impact are PROJECTED (with bands that " +
		"never collapse); the edge is MEASURED, the why AUTHORED; each labelled, never fused."
)

// BuildRootCauseChain renders the transitive root-cause surface: the MEASURED lane (cap.
// B) from `chains`, and the multi-hop PROJECTED lane (cap. D) from `projected`. It never
// invents a chain and never upgrades a class: OFF, quiet, active (MEASURED) and OFF,
// gate-pending, quiet, active (PROJECTED) are distinct, stated states. The projected
// chains are WITHHELD (ProjectedActive=false, no ProjectedChains) until projectedGatePassed.
func BuildRootCauseChain(chains []flow.Chain, projected []flow.Chain, enabled, projectedGatePassed bool, now time.Time) *RootCauseChainView {
	v := &RootCauseChainView{GeneratedAt: now, Class: rootCauseClass, Enabled: enabled}
	switch {
	case !enabled:
		v.Note = rootCauseOffNote
	case len(chains) == 0:
		v.Note = rootCauseQuietNote
	default:
		v.Active = true
		v.Note = rootCauseActiveNote
		v.Chains = chains
	}
	// The multi-hop PROJECTED lane — gated separately from the MEASURED lane.
	v.ProjectedGatePassed = projectedGatePassed
	switch {
	case !enabled:
		v.ProjectedNote = rootCauseProjOffNote
	case !projectedGatePassed:
		// Gate-pending: computed but withheld (a new class is not visible before its gate).
		v.ProjectedNote = rootCauseProjPendingNote
	case len(projected) == 0:
		v.ProjectedNote = rootCauseProjQuietNote
	default:
		v.ProjectedActive = true
		v.ProjectedNote = rootCauseProjActiveNote
		v.ProjectedChains = projected
	}
	return v
}
