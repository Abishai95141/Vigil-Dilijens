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
)

// BuildRootCauseChain renders the transitive root-cause surface from the warm-path
// chains. It never invents a chain and never upgrades a class: OFF, quiet, and active
// are distinct, stated states.
func BuildRootCauseChain(chains []flow.Chain, enabled bool, now time.Time) *RootCauseChainView {
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
	return v
}
