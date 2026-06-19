package api

import "time"

// GovernanceClass labels /api/governance: these are CANDIDATE proposals (status orthogonal
// to the three provenance classes) awaiting a NAMED HUMAN's decision. The harness can
// block; it cannot approve (doc 12 §3.3). Promotion produces a committable AUTHORED overlay
// — it never auto-mutates the released graph (the candidate-store firewall holds).
const GovernanceClass = "CANDIDATE proposals — a named human promotes or rejects; the system never approves"

const governanceNote = "Governance review queue (doc 20 + doc 12 §3.3): every DGX proposal — stray->entity " +
	"mappings, agent associations, trace topology, audit hypotheses — is status=candidate and is firewalled from the " +
	"deterministic detection/forecast path. A candidate becomes authoritative ONLY when a named human promotes it here; " +
	"the human authors the note (the model's rationale is shown for context but discarded), and promotion emits a " +
	"committable AUTHORED overlay the operator commits through the release governance gate. Reject removes it from the queue."

const governanceGateNote = "The harness can BLOCK but never APPROVE (doc 12 §3.3): a promotion requires a named " +
	"human + an authored note. Promotion does NOT auto-load into the released graph — it produces a starting overlay " +
	"artifact the human commits deliberately, so the firewall between candidates and detection stays intact."

const governanceOffNote = "The Dynamic Graph eXtension lane is not enabled (--dgx-enabled): there is no candidate " +
	"store, so there is nothing to govern. Detection and forecasting are unaffected."

// GovernanceEvidence is one MEASURED fact a candidate cites — surfaced so the reviewer can
// make a falsifiable call ("review without evidence is rejection by default", doc 12 §3.3).
type GovernanceEvidence struct {
	Kind   string `json:"kind"`
	Ref    string `json:"ref"`
	Detail string `json:"detail,omitempty"`
}

// GovernanceSupport is a candidate's DETERMINISTIC support (doc 21 §4): a struct of integer
// COUNTS of agreed MEASURED facts, NEVER a model confidence, a weighted sum, or a 0-1 score.
// The review UI ranks by the lexicographic tuple of these counts and renders them verbatim
// ("3 evidence · captures 7 strays · seen 2×") — it must never display a percentage or a
// synthesized score. main computes it via candidate.Score (api never imports internal/candidate).
type GovernanceSupport struct {
	EvidenceCount    int   `json:"evidenceCount"`
	CaptureSample    int   `json:"captureSample"`
	Recurrence       int   `json:"recurrence"`
	DistinctEntities int   `json:"distinctEntities"`
	AgeSeconds       int64 `json:"ageSeconds"`
}

// GovernanceItem is one candidate as the review surface presents it — the full provenance
// the human needs to decide: what is proposed, by which producer, on what evidence, and (if
// decided) who decided + their authored note.
type GovernanceItem struct {
	ID           string               `json:"id"`
	Kind         string               `json:"kind"`
	Status       string               `json:"status"`
	Subject      string               `json:"subject"`
	Relation     string               `json:"relation,omitempty"`
	Source       string               `json:"source"` // cei-fallback | dgx-agent | audit | trace | assoc
	Method       string               `json:"method,omitempty"`
	GraphVersion string               `json:"graphVersion,omitempty"`
	Rationale    string               `json:"rationale,omitempty"` // the MODEL's proposed note (PROPOSED, for context; discarded at promotion)
	Evidence     []GovernanceEvidence `json:"evidence"`
	Support      GovernanceSupport    `json:"support"` // deterministic MEASURED support (doc 21 §4) — counts, never a confidence
	DecidedBy    string               `json:"decidedBy,omitempty"`
	Note         string               `json:"note,omitempty"`
	DecidedAt    *time.Time           `json:"decidedAt,omitempty"`
	CreatedAt    time.Time            `json:"createdAt"`
	// Actionable reports whether this candidate is worth a human mapping decision. A pending
	// candidate that is NOT actionable (a pure k8s object-metadata stray) is kept out of the
	// review queue but still counted — see GovernanceView.SuppressedMetadata. main sets this
	// (it owns the classification), so api never imports internal/candidate.
	Actionable bool `json:"actionable"`
}

// GovernanceView is the /api/governance payload: the pending review queue + the decided
// audit trail + counts + the governance discipline.
type GovernanceView struct {
	Class        string           `json:"class"`
	Available    bool             `json:"available"`
	GeneratedAt  time.Time        `json:"generatedAt"`
	GraphVersion string           `json:"graphVersion,omitempty"`
	Pending      []GovernanceItem `json:"pending"` // status=candidate AND actionable — the review queue
	Decided      []GovernanceItem `json:"decided"` // promoted | rejected | shadow — the audit trail
	Counts       map[string]int   `json:"counts"`  // by status (all candidates, honest total)
	// SuppressedMetadata is the count of status=candidate proposals NOT enqueued for review
	// because they map a pure k8s object-metadata stray (KSM object inventory — a ReplicaSet's
	// generation, an Endpoints' addresses, a ConfigMap's info). They remain counted (Counts +
	// /api/provisional-coverage) so coverage stays honest; they are simply not actionable.
	SuppressedMetadata int    `json:"suppressedMetadata"`
	SuppressedNote     string `json:"suppressedNote,omitempty"`
	GateNote           string `json:"gateNote"`
	Note               string `json:"note"`
}

// NewGovernanceView splits already-mapped items into the pending queue + the decided trail
// and tallies the counts.
func NewGovernanceView(now time.Time, graphVersion string, items []GovernanceItem) *GovernanceView {
	v := &GovernanceView{
		Class: GovernanceClass, Available: true, GeneratedAt: now, GraphVersion: graphVersion,
		Pending: []GovernanceItem{}, Decided: []GovernanceItem{}, Counts: map[string]int{},
		GateNote: governanceGateNote, Note: governanceNote,
	}
	for _, it := range items {
		v.Counts[it.Status]++
		if it.Status == "candidate" {
			// Keep the human queue to the NECESSARY decisions: a non-actionable candidate (a
			// pure k8s object-metadata stray) is counted but not enqueued. The deterministic
			// classification lives in main (api never imports internal/candidate).
			if it.Actionable {
				v.Pending = append(v.Pending, it)
			} else {
				v.SuppressedMetadata++
			}
		} else {
			v.Decided = append(v.Decided, it)
		}
	}
	if v.SuppressedMetadata > 0 {
		v.SuppressedNote = "Classified as k8s object-metadata (KSM object inventory — e.g. ReplicaSet generation, " +
			"Endpoints addresses, ConfigMap/Secret info): non-actionable as an operational signal, so NOT enqueued for " +
			"review. They stay counted here and in /api/provisional-coverage (coverage stays honest); promote a stray " +
			"only when it is a real operational series worth binding to an entity."
	}
	return v
}

// UnavailableGovernance is the honest OFF state (the DGX lane is not enabled).
func UnavailableGovernance(now time.Time) *GovernanceView {
	return &GovernanceView{
		Class: GovernanceClass, Available: false, GeneratedAt: now,
		Pending: []GovernanceItem{}, Decided: []GovernanceItem{}, Counts: map[string]int{},
		GateNote: governanceGateNote, Note: governanceOffNote,
	}
}

// GovernanceDecisionRequest is the POST /api/governance/decide body: a NAMED human's call.
type GovernanceDecisionRequest struct {
	CandidateID string `json:"candidateId"`
	Decision    string `json:"decision"`  // "promote" | "reject"
	DecidedBy   string `json:"decidedBy"` // the named human (mandatory)
	Note        string `json:"note"`      // the human's authored note
}

// GovernanceDecisionResult is the decide response. On a promote it carries the committable
// AUTHORED overlay artifact (the human commits it through release governance).
type GovernanceDecisionResult struct {
	OK          bool   `json:"ok"`
	CandidateID string `json:"candidateId"`
	Status      string `json:"status,omitempty"` // promoted | rejected
	OverlayYAML string `json:"overlayYaml,omitempty"`
	Message     string `json:"message"`
}
