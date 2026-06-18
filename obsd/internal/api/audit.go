package api

import "time"

// AuditChangeClass labels the /api/audit-changes payload: MEASURED change records read
// from the Kubernetes audit log (doc 20 P4 AUDIT lane). A change is a FACT (who did what,
// when), the same class as a threshold state — never a cause. "This change might relate
// to that incident" is a separate PROPOSED candidate (a direction-free causal hypothesis),
// surfaced at /api/candidates, never as a fact here.
const AuditChangeClass = "MEASURED change records (audit log; who/what/when — not a cause)"

const auditChangeNote = "Kubernetes audit-log changes (doc 20 P4): completed mutating API calls " +
	"(create/update/patch/delete), MEASURED — the same class as a fingerprint. The arrow-of-time prune (a change " +
	"at or after an incident's onset cannot precede it) and the change→incident co-occurrence are surfaced as " +
	"direction-free PROPOSED hypotheses at /api/candidates, never as a cause. Off the deterministic digest; never " +
	"feeds detection or replay."

const auditChangeOffNote = "The audit lane is not enabled (--audit-enabled with an --audit-log-path). " +
	"Detection and forecasting are unaffected."

const auditChangeWarmingNote = "The audit lane is enabled and awaiting its first read cycle. Detection and forecasting are unaffected."

// AuditChangeRow is one MEASURED change record, surfaced read-only. Timestamp is the
// audit record's SOURCE completion time (UTC), never receipt time.
type AuditChangeRow struct {
	AuditID        string    `json:"auditId"`
	Verb           string    `json:"verb"`
	Resource       string    `json:"resource"`
	Namespace      string    `json:"namespace"`
	Name           string    `json:"name"`
	User           string    `json:"user"`
	RoleCEI        string    `json:"roleCei"`
	RoleUnresolved bool      `json:"roleUnresolved"`
	Timestamp      time.Time `json:"timestamp"`
}

// AuditView is the /api/audit-changes payload.
type AuditView struct {
	Class            string           `json:"class"`
	Available        bool             `json:"available"`
	GeneratedAt      time.Time        `json:"generatedAt"`
	ChangesObserved  int              `json:"changesObserved"`
	HypothesesStaged int              `json:"hypothesesStaged"` // co-occurrence candidates staged this cycle (surfaced at /api/candidates)
	LinesRead        int              `json:"linesRead"`        // raw audit-log lines read this cycle (after the tail cap)
	SourceTruncated  bool             `json:"sourceTruncated"`  // the audit log exceeded the read cap, so older lines were dropped (honest partial coverage)
	Changes          []AuditChangeRow `json:"changes"`
	Note             string           `json:"note"`
}

// NewAuditView builds the available view from already-mapped rows. hypothesesStaged is
// the count of direction-free candidates the cycle staged (the actual candidates live in
// the firewalled store, surfaced at /api/candidates). linesRead/sourceTruncated state how
// much of the audit log the cycle saw — truncation is surfaced, never hidden.
func NewAuditView(now time.Time, hypothesesStaged, linesRead int, sourceTruncated bool, rows []AuditChangeRow) *AuditView {
	if rows == nil {
		rows = []AuditChangeRow{}
	}
	note := auditChangeNote
	if sourceTruncated {
		note += " NOTE: the audit log exceeded the read cap; older lines were dropped this cycle (partial coverage)."
	}
	return &AuditView{
		Class: AuditChangeClass, Available: true, GeneratedAt: now,
		ChangesObserved: len(rows), HypothesesStaged: hypothesesStaged,
		LinesRead: linesRead, SourceTruncated: sourceTruncated,
		Changes: rows, Note: note,
	}
}

// UnavailableAudit is the honest OFF state.
func UnavailableAudit(now time.Time) *AuditView {
	return &AuditView{
		Class: AuditChangeClass, Available: false, GeneratedAt: now,
		Changes: []AuditChangeRow{}, Note: auditChangeOffNote,
	}
}

// WarmingAudit is the honest state for an ENABLED lane that has not completed its first
// read cycle — distinct from the OFF state (which would falsely say "not enabled").
func WarmingAudit(now time.Time) *AuditView {
	return &AuditView{
		Class: AuditChangeClass, Available: false, GeneratedAt: now,
		Changes: []AuditChangeRow{}, Note: auditChangeWarmingNote,
	}
}
