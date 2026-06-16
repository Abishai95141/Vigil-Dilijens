package api

import (
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/departure"
)

// DepartureView is the band-departure anomaly surface (doc 15 cap. C): a MEASURED sample
// that left its own PROJECTED forecast band — the series did something its own recent
// forecast did not anticipate. A JOIN, never a fusion: the band is PROJECTED (the clock's
// forecast), the realized sample is MEASURED, and the departure is classed PROJECTED
// (weakest-input). It is NEVER a MEASURED "anomaly score" and carries no causal claim.
//
// Gate posture (doc 11 §3.5): the DETERMINISTIC producer gate (`just departure-gate`) PASSES
// — a measured sample leaving its band fires, and a noisy-but-stationary series (whose clock
// band is WIDE) never false-fires (the structural FP defense). But this is a new model-
// derived PROJECTED class, so it is NOT operator-visible until a real step is captured live:
// Active stays false (and Departures nil) while gate-pending. The lane still COMPUTES every
// tick (off the digest), withheld from the surface — exactly the Phase-E / cap-D posture.
type DepartureView struct {
	GeneratedAt time.Time             `json:"generatedAt"`
	Class       string                `json:"class"`   // "PROJECTED band ⋈ MEASURED sample (joined, never fused)"
	Enabled     bool                  `json:"enabled"` // --departure-enabled is set
	Active      bool                  `json:"active"`  // a departure is surfaced this tick (only once gate-passed)
	Note        string                `json:"note"`    // honest lane state
	Departures  []departure.Departure `json:"departures,omitempty"`
}

const (
	departureClass   = "PROJECTED band ⋈ MEASURED sample (joined, never fused)"
	departureOffNote = "The band-departure anomaly lane is OFF (obsd --departure-enabled not set): " +
		"a series leaving its own forecast band is not being watched."
	departurePendingNote = "Band-departure anomaly (doc 15 cap. C) is COMPUTED every tick but NOT yet " +
		"operator-visible: a new model-derived PROJECTED class ships only after a real step is captured " +
		"live (doc 11 §3.5). The deterministic producer gate (anti-false-warning on the near-miss/decoy " +
		"corpus, recall on the true step) already passes. The lane writes zero bytes to the replay digest."
	departureQuietNote = "Band-departure lane ON; no series left its own forecast band this tick — every " +
		"measured sample is inside its projected band (or absorbed by a wide band on a noisy series)."
	departureActiveNote = "A measured sample left its own PROJECTED forecast band: the series did something " +
		"its own recent forecast did not anticipate. The band is PROJECTED, the sample MEASURED, the departure " +
		"PROJECTED (weakest-input) — never upgraded to a measured figure, never a fabricated reason; each class shown side by side."
)

// BuildDepartures renders the band-departure surface. It never invents a departure and never
// upgrades a class: OFF, gate-pending, quiet, and active are distinct, stated states. The
// departures are WITHHELD (Active=false, no Departures) until gatePassed.
func BuildDepartures(deps []departure.Departure, enabled, gatePassed bool, now time.Time) *DepartureView {
	v := &DepartureView{GeneratedAt: now, Class: departureClass, Enabled: enabled}
	switch {
	case !enabled:
		v.Note = departureOffNote
	case !gatePassed:
		v.Note = departurePendingNote // computed but withheld (a new class is not visible before its gate)
	case len(deps) == 0:
		v.Note = departureQuietNote
	default:
		v.Active = true
		v.Note = departureActiveNote
		v.Departures = deps
	}
	return v
}
