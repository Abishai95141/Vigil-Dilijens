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
	// PROJECTED agent enrichment (doc 21 Phase 4 §C): a human-readable label + non-causal
	// description the model SUGGESTS for a recurring-anomaly phenomenon candidate, to help the
	// reviewer. It is a HINT — discarded at promotion (the human authors the real label), it
	// asserts no cause and drives no detection. Empty when there is no suggestion.
	SuggestedLabel       string     `json:"suggestedLabel,omitempty"`
	SuggestedDescription string     `json:"suggestedDescription,omitempty"`
	SuggestedSeverity    string     `json:"suggestedSeverity,omitempty"`  // a SUGGESTED harm level (hint, discarded at promotion)
	SuggestedDirection   string     `json:"suggestedDirection,omitempty"` // doc 33 P4: agent's suggested causal direction (a-to-b|b-to-a|not-causal); a hint, the human authors
	SuggestedBy          string     `json:"suggestedBy,omitempty"`        // the model that produced the hint (provenance)
	DecidedBy            string     `json:"decidedBy,omitempty"`
	Note                 string     `json:"note,omitempty"`
	DecidedAt            *time.Time `json:"decidedAt,omitempty"`
	CreatedAt            time.Time  `json:"createdAt"`
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
	// SuppressedNonOperational counts stray series CLASSIFIED non-operational (the exporter's
	// own Go/process runtime, client-library plumbing, or kube control-plane component
	// internals) and therefore NOT STAGED as candidates at all — they can never bind to a
	// workload/node/storage entity, so there is no mapping decision to make. Unlike
	// SuppressedMetadata (staged but not enqueued), these were never saved, so they do NOT
	// appear in Counts; they are surfaced here so the operator sees exactly what was excluded
	// and why. Distinct series since process start. main sets these from the deterministic
	// classifier (api never imports internal/candidate).
	SuppressedNonOperational        int            `json:"suppressedNonOperational"`
	SuppressedNonOperationalByClass map[string]int `json:"suppressedNonOperationalByClass,omitempty"`
	SuppressedNonOperationalNote    string         `json:"suppressedNonOperationalNote,omitempty"`
	GateNote                        string         `json:"gateNote"`
	Note                            string         `json:"note"`
}

// SetNonOperationalExcluded records the count of stray series classified non-operational and
// excluded from staging (not saved). main calls this with the deterministic classifier's tally
// so the governance surface stays honest about what it chose not to keep. A no-op at zero.
func (v *GovernanceView) SetNonOperationalExcluded(total int, byClass map[string]int) {
	if v == nil || total <= 0 {
		return
	}
	v.SuppressedNonOperational = total
	v.SuppressedNonOperationalByClass = byClass
	v.SuppressedNonOperationalNote = "Classified non-operational and NOT saved as mapping candidates: the " +
		"exporter's own Go/process runtime (go_*/process_*), client-library plumbing (rest_client_*/workqueue_*), " +
		"and kube control-plane component internals (apiserver_*/etcd_*/scheduler_*). None of these can bind to a " +
		"workload/node/storage entity, so there is no mapping decision to make — the queue is kept to real, " +
		"map-able signals. They are excluded at the candidate seam (off-digest; detection is unaffected). Control-plane " +
		"HEALTH is a separate modality Vigil does not model yet, so these are reclaimable. Distinct series since process start."
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
		v.SuppressedNote = "Non-actionable proposals kept out of the review queue but still counted (and still fed to " +
			"the agent): k8s object-metadata strays (KSM inventory — ReplicaSet generation, Endpoints, ConfigMap/Secret " +
			"info) AND bare cei-fallback stray-NODE placeholders — for a stray the actionable decision is the " +
			"associated-with EDGE or the equiv_group mapping (the agent proposes those), not the node itself. They stay " +
			"counted here and in /api/provisional-coverage so coverage stays honest."
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

// GovernanceEquivPreview is the deterministic stray→group RESOLUTION delta a (still pending)
// equivalence-group promotion WOULD produce — computed read-only on a scratch graph (doc 21
// §4-5, Phase 3 slice 2). Every list is a set of MEASURED metric names: a COUNT of facts,
// never a confidence. The equiv_group promotion is the one that moves MEASURED coverage, so
// the operator sees the move (which strays the pattern absorbs) BEFORE committing.
type GovernanceEquivPreview struct {
	GroupID         string   `json:"groupId"`             // the group the pattern lands in (existing or new)
	DefinesNewGroup bool     `json:"definesNewGroup"`     // true ⇒ a brand-new group is defined
	Canonical       string   `json:"canonical,omitempty"` // canonical OTel variable (for a new group)
	Pattern         string   `json:"pattern"`             // the proposed dialect regex
	NewlyResolved   []string `json:"newlyResolved"`       // strays UNRESOLVED now → RESOLVED after (the coverage move)
	AlreadyResolved []string `json:"alreadyResolved"`     // scope metrics that already resolve (pattern redundant for them)
	StillUnresolved []string `json:"stillUnresolved"`     // scope metrics the pattern still would not match (honest residue)
}

// GovernancePreviewResult is the GET /api/governance/preview response: a READ-ONLY look at
// what promoting a candidate would author (the overlay) and, for an equivalence-group
// candidate, the stray→group resolution delta. It NEVER mutates the candidate store or the
// graph. The overlay's author/note are PLACEHOLDERS the named human fills at promotion.
type GovernancePreviewResult struct {
	OK            bool                    `json:"ok"`
	CandidateID   string                  `json:"candidateId"`
	Kind          string                  `json:"kind,omitempty"`
	MovesCoverage bool                    `json:"movesCoverage"`         // true ONLY for equiv_group — the one promotion that moves MEASURED coverage
	OverlayYAML   string                  `json:"overlayYaml,omitempty"` // the exact overlay a promotion would author (author/note are placeholders)
	Equiv         *GovernanceEquivPreview `json:"equiv,omitempty"`       // the resolution delta (equiv_group only)
	Caveat        string                  `json:"caveat,omitempty"`      // honest note on what this promotion does NOT do
	Message       string                  `json:"message,omitempty"`
}
