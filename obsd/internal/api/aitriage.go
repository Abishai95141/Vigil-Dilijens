package api

import "time"

// AI-assisted governance triage surface (docs/33 build 4). An operator authorizes the agent to
// review staged candidate(s) and recommend a verdict; the verdict is applied (promote/reject),
// logged to the AI audit trail, and reversible. These are the request/response + audit-view
// shapes; main.go runs the agent and maps the candidate-store types onto them (the api package
// stays free of the candidate store, like every other view).

// AITriageRequest asks the agent to triage either ONE candidate (CandidateID set) or ALL pending
// ACTIONABLE candidates (All=true). AuthorizedBy is the operator who clicked — mandatory: the
// agent never acts unauthorized. The verdict is applied unless DryRun is set (recommend-only).
type AITriageRequest struct {
	CandidateID  string `json:"candidateId,omitempty"`
	All          bool   `json:"all,omitempty"`
	AuthorizedBy string `json:"authorizedBy"`
	DryRun       bool   `json:"dryRun,omitempty"`
}

// AITriageOutcome is the agent's verdict on one candidate + what was done with it.
type AITriageOutcome struct {
	CandidateID string `json:"candidateId"`
	Subject     string `json:"subject,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Verdict     string `json:"verdict"` // promote | reject | hold
	Confidence  string `json:"confidence,omitempty"`
	Rationale   string `json:"rationale,omitempty"`
	Direction   string `json:"direction,omitempty"`
	Applied     bool   `json:"applied"`          // the verdict changed the candidate's status
	Status      string `json:"status,omitempty"` // resulting candidate status
	Error       string `json:"error,omitempty"`  // a per-candidate failure (provider/parse) — never aborts the batch
}

// AITriageResult is the triage response.
type AITriageResult struct {
	OK        bool              `json:"ok"`
	Message   string            `json:"message"`
	Model     string            `json:"model,omitempty"`
	Reviewed  int               `json:"reviewed"`
	Promoted  int               `json:"promoted"`
	Rejected  int               `json:"rejected"`
	Held      int               `json:"held"`
	Outcomes  []AITriageOutcome `json:"outcomes,omitempty"`
	DryRun    bool              `json:"dryRun,omitempty"`
	Disclaimer string           `json:"disclaimer"`
}

// AIAuditEntry is one row of the AI audit log (a plain mirror of candidate.AgentDecision).
type AIAuditEntry struct {
	CandidateID  string    `json:"candidateId"`
	Subject      string    `json:"subject,omitempty"`
	Verdict      string    `json:"verdict"`
	Applied      bool      `json:"applied"`
	Confidence   string    `json:"confidence,omitempty"`
	Rationale    string    `json:"rationale,omitempty"`
	Direction    string    `json:"direction,omitempty"`
	Model        string    `json:"model,omitempty"`
	AuthorizedBy string    `json:"authorizedBy"`
	DecidedAt    time.Time `json:"decidedAt"`
	Reverted     bool      `json:"reverted"`
	RevertedBy   string    `json:"revertedBy,omitempty"`
	RevertedAt   time.Time `json:"revertedAt,omitempty"`
	CanRevert    bool      `json:"canRevert"` // an applied promotion not yet reverted
}

// AIAuditView is the AI audit log surface.
type AIAuditView struct {
	GeneratedAt time.Time      `json:"generatedAt"`
	Disclaimer  string         `json:"disclaimer"`
	Entries     []AIAuditEntry `json:"entries"`
}

// RevertRequest undoes an agent promotion. RevertedBy is the operator (mandatory).
type RevertRequest struct {
	CandidateID string `json:"candidateId"`
	RevertedBy  string `json:"revertedBy"`
}

// RevertResult is the revert response.
type RevertResult struct {
	OK          bool   `json:"ok"`
	CandidateID string `json:"candidateId"`
	Message     string `json:"message"`
}

// AITriageDisclaimer is the verbatim fallibility notice surfaced with every triage + the audit
// log — the system states plainly that the agent can be wrong and the operator is in control.
const AITriageDisclaimer = "AI-assisted triage: DeepSeek reviews each candidate's measured evidence and recommends promote / reject / hold. It can be wrong — it may promote a weak proposal or reject a useful one. Every decision is logged here, and any promotion can be reverted. You authorized this review; you remain in control."
