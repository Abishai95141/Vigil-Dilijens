package api

import "time"

// CandidateClass labels the /api/candidates payload: these are PROPOSED/CANDIDATE
// data (doc 20), never authoritative, never read by the deterministic detection or
// forecast path. The lifecycle status is ORTHOGONAL to the three provenance classes.
const CandidateClass = "CANDIDATE — staged proposals, never authoritative; never read by detection"

const candidateNote = "Dynamic Graph eXtension staging store (doc 20): nodes/edges/members an agent or " +
	"deterministic discovery pass PROPOSED for a named human to promote through the governance gate. These are NOT " +
	"authored facts and NOT causes — a candidate reaches the authoritative graph only by human promotion, where the " +
	"human authors the note, name, and version. Read-only here; the deterministic path never reads candidates."

const candidateOffNote = "The Dynamic Graph eXtension lane is not enabled (--dgx-enabled): the candidate staging " +
	"store is not open. Detection and forecasting are unaffected — the firewall holds whether or not this lane runs."

// CandidateRow is one staged proposal, surfaced read-only. The surfacing layer maps
// the candidate store's rows into these — api never imports internal/candidate, so the
// read-firewall (the deterministic-or-surfacing layer stays decoupled from the
// quarantined store) is kept by construction.
type CandidateRow struct {
	ID            string    `json:"id"`
	Kind          string    `json:"kind"`
	Status        string    `json:"status"`
	Subject       string    `json:"subject"`
	Relation      string    `json:"relation,omitempty"`
	Source        string    `json:"source"`
	Method        string    `json:"method,omitempty"`
	GraphVersion  string    `json:"graphVersion,omitempty"`
	EvidenceCount int       `json:"evidenceCount"`
	Reason        string    `json:"reason,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// CandidatesView is the /api/candidates payload: the DGX candidate staging store,
// surfaced read-only with a clear "not authoritative" label so a candidate is never
// mistaken for an authored cause.
type CandidatesView struct {
	Class       string         `json:"class"`
	Available   bool           `json:"available"` // false ⇒ --dgx-enabled is off
	GeneratedAt time.Time      `json:"generatedAt"`
	Counts      map[string]int `json:"counts"` // by lifecycle status
	Candidates  []CandidateRow `json:"candidates"`
	Note        string         `json:"note"`
}

// NewCandidatesView builds the available view from already-mapped rows.
func NewCandidatesView(now time.Time, rows []CandidateRow) *CandidatesView {
	if rows == nil {
		rows = []CandidateRow{}
	}
	counts := map[string]int{}
	for _, r := range rows {
		counts[r.Status]++
	}
	return &CandidatesView{
		Class: CandidateClass, Available: true, GeneratedAt: now,
		Counts: counts, Candidates: rows, Note: candidateNote,
	}
}

// unavailableCandidates is the honest OFF state (the lane is not enabled).
func unavailableCandidates(now time.Time) *CandidatesView {
	return &CandidatesView{
		Class: CandidateClass, Available: false, GeneratedAt: now,
		Counts: map[string]int{}, Candidates: []CandidateRow{}, Note: candidateOffNote,
	}
}
