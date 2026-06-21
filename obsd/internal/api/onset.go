package api

import (
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/onset"
)

// OnsetView is the changepoint-onset surface (doc 22 C2): the MEASURED times at which
// watched gauge series stepped (up|down), computed off-digest by EWMA-residual CUSUM with a
// sustained-shift confirmation. It gives a precise onset TIME to a level shift — including a
// SUB-THRESHOLD shift that crosses no bar and so is invisible to the three on-digest
// primitives — and it is the temporal-adjacency substrate C3 reads (which side moved first,
// surfaced direction-free for a human). It is NOT an anomaly score and carries no causal
// claim; StepZ is the measured size of the sustained shift in robust-sigma units.
//
// Off-digest, like departure: zero bytes into the replay tick digest. Its determinism is the
// honest, narrower one — "same samples + same params ⇒ same onsets". Surfaced when enabled
// (MEASURED + deterministic ⇒ no forecast-class gate); withheld with an honest note when off.
type OnsetView struct {
	GeneratedAt time.Time     `json:"generatedAt"`
	Class       string        `json:"class"`
	Enabled     bool          `json:"enabled"` // --onset-enabled is set
	Active      bool          `json:"active"`  // at least one onset this tick
	Streams     int           `json:"streams"` // gauge streams scanned
	Note        string        `json:"note"`
	Onsets      []onset.Onset `json:"onsets,omitempty"`
}

const (
	onsetClass   = "MEASURED changepoint (off-digest; a step's onset TIME, not an anomaly score)"
	onsetOffNote = "The changepoint-onset lane is OFF (obsd --onset-enabled not set): the onset " +
		"TIME of a level step is not being computed. Detection/forecasting are unaffected (off-digest)."
	onsetQuietNote = "Onset lane ON; no watched gauge series stepped this tick — every series is within " +
		"its own recent level (no sustained changepoint cleared the floor)."
	onsetActiveNote = "One or more gauge series stepped (a sustained level change). The onset TIME is " +
		"MEASURED arithmetic (off-digest); it is not an anomaly score and asserts no cause — it marks WHEN " +
		"the step began, and feeds the direction-free temporal adjacency a human judges in the hypotheses tab."
)

// BuildOnsets renders the onset surface. OFF / quiet / active are distinct, stated states; it
// never invents an onset and never upgrades the class.
func BuildOnsets(onsets []onset.Onset, enabled bool, streams int, now time.Time) *OnsetView {
	v := &OnsetView{GeneratedAt: now, Class: onsetClass, Enabled: enabled, Streams: streams}
	switch {
	case !enabled:
		v.Note = onsetOffNote
	case len(onsets) == 0:
		v.Note = onsetQuietNote
	default:
		v.Active = true
		v.Note = onsetActiveNote
		v.Onsets = onsets
	}
	return v
}
