package api

import "time"

// CausalHypothesesView is the direction-free causal-hypothesis surface (doc 22 C3): pairs of
// series that are ASSOCIATED (a MEASURED associated-with edge) and both stepped (a C2 onset)
// within a window — surfaced as PROJECTIONS + ASSOCIATIONS, never as a cause. The observed
// temporal order is shown as a MEASURED fact for the operator to weigh; the system asserts NO
// direction. A named operator authors the causal direction (or marks it not-causal) — that
// human decision is the only thing that ever becomes AUTHORED. This is the charter-clean form
// of the competitor's correlation-plus-precedence root cause: Vigil surfaces the lead and
// refuses to draw the arrow (whose auto-inference E3b/E4b showed invents edges).
type CausalHypothesesView struct {
	GeneratedAt time.Time             `json:"generatedAt"`
	Class       string                `json:"class"`
	Enabled     bool                  `json:"enabled"`
	Note        string                `json:"note"`
	Hypotheses  []CausalHypothesisRow `json:"hypotheses,omitempty"`
}

// CausalHypothesisRow is one staged direction-free hypothesis. A/B are the coupled series;
// ObservedFirst is the MEASURED order (which onset was earlier) — explicitly NOT a cause.
type CausalHypothesisRow struct {
	ID            string  `json:"id"`
	Subject       string  `json:"subject"`  // "A ~ B" (direction-free)
	Relation      string  `json:"relation"` // "co-occurrence" | "observed-adjacency"
	Source        string  `json:"source"`   // "co-onset" | "audit"
	A             string  `json:"a,omitempty"`
	B             string  `json:"b,omitempty"`
	ADirection    string  `json:"aDirection,omitempty"` // the onset direction (up|down) of A
	BDirection    string  `json:"bDirection,omitempty"`
	ObservedFirst string  `json:"observedFirst,omitempty"` // MEASURED order (onset witness), NOT a cause
	DeltaSeconds  int64   `json:"deltaSeconds,omitempty"`
	Coefficient   float64 `json:"coefficient,omitempty"` // lag-0 LEVEL Pearson r (distinct from the detrended-lag r)
	// The SECOND, independent witness (docs/31 §5): the detrended lagged cross-correlation.
	// LagPeakSeconds is sign-carrying MEASURED EVIDENCE (positive = A appeared to lead B), NOT a
	// cause; present only when a significant, nonzero, detrended lead survived (else omitted —
	// honest silence). LagConsistentWithOnset reports whether this witness AGREES with the onset
	// order; nil = no lead-lag witness for this pair.
	LagPeakSeconds         int64         `json:"lagPeakSeconds,omitempty"`
	LagPeakRDetrended      float64       `json:"lagPeakRDetrended,omitempty"`
	LagP                   float64       `json:"lagP,omitempty"`
	EffectiveN             int64         `json:"effectiveN,omitempty"`
	LagConsistentWithOnset *bool         `json:"lagConsistentWithOnset,omitempty"`
	Evidence               []EvidenceRow `json:"evidence,omitempty"`
	CreatedAt              time.Time     `json:"createdAt"`
	// doc 33 P4: the agent's SUGGESTED direction (a PROJECTED hint, admitted only when the
	// dual-witness agreed). Shown beside the witnesses for a NAMED human to author or reject;
	// it is NOT the authored arrow and is discarded at promotion.
	SuggestedDirection string `json:"suggestedDirection,omitempty"` // "a-to-b" | "b-to-a"
	SuggestedRationale string `json:"suggestedRationale,omitempty"`
	SuggestedBy        string `json:"suggestedBy,omitempty"` // the provider that proposed it (provenance)
}

// EvidenceRow is one MEASURED fact a hypothesis cites (no score, no causal claim).
type EvidenceRow struct {
	Kind   string `json:"kind"`
	Ref    string `json:"ref"`
	Detail string `json:"detail,omitempty"`
}

const causalHypothesisClass = "PROJECTION + ASSOCIATION — direction-free co-occurrence; a named operator authors the direction (never the system)"

const (
	causalHypothesisOffNote = "The direction-free causal-hypothesis lane is OFF (needs --cohypothesis-enabled " +
		"with --onset-enabled + --assoc-enabled + --dgx-enabled): coupled series that step together are not being staged."
	causalHypothesisQuietNote = "Causal-hypothesis lane ON; no coupled series co-stepped within the window — nothing to " +
		"propose. (A hypothesis needs BOTH an associated-with edge AND a co-onset; neither alone qualifies.)"
	causalHypothesisActiveNote = "These coupled series stepped together. Each row is a DIRECTION-FREE co-occurrence: the " +
		"association (level Pearson r) and the two onset times are MEASURED; the observed order is shown but is NOT a cause. " +
		"TWO INDEPENDENT lead witnesses may be present — the onset ORDER (observedFirst) and a detrended lagged " +
		"cross-correlation (lagPeakSeconds, sign-carrying). When lagConsistentWithOnset is true the two AGREE (a stronger lead " +
		"to investigate); when false or the lead-lag witness is absent, treat it as 'co-moved, order unclear'. Neither witness " +
		"is a direction. Author the causal direction from your knowledge of the system — or mark it not-causal. Only your decision becomes AUTHORED."
)

// BuildCausalHypotheses renders the surface from pre-mapped rows (main does the candidate
// mapping so the api never imports internal/candidate). OFF / quiet / active are distinct.
func BuildCausalHypotheses(rows []CausalHypothesisRow, enabled bool, now time.Time) *CausalHypothesesView {
	v := &CausalHypothesesView{GeneratedAt: now, Class: causalHypothesisClass, Enabled: enabled}
	switch {
	case !enabled:
		v.Note = causalHypothesisOffNote
	case len(rows) == 0:
		v.Note = causalHypothesisQuietNote
	default:
		v.Note = causalHypothesisActiveNote
		v.Hypotheses = rows
	}
	return v
}

// CausalDirectionRequest is the operator's authored direction for a hypothesis. Direction is
// "a-to-b" | "b-to-a" | "not-causal"; DecidedBy is the named human (mandatory).
type CausalDirectionRequest struct {
	CandidateID string `json:"candidateId"`
	Direction   string `json:"direction"`
	DecidedBy   string `json:"decidedBy"`
	Note        string `json:"note"`
}

// CausalDirectionResult is the outcome: on an authored direction, the committable AUTHORED
// overlay YAML (the human's directed note); on not-causal, the rejection.
type CausalDirectionResult struct {
	OK          bool   `json:"ok"`
	CandidateID string `json:"candidateId"`
	Status      string `json:"status"` // "promoted" | "rejected"
	OverlayYAML string `json:"overlayYaml,omitempty"`
	Message     string `json:"message,omitempty"`
}
