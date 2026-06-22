package api

import (
	"strings"
	"time"
)

// ProvisionalCoverageClass labels /api/provisional-coverage: every count here is
// status=CANDIDATE — proposed mappings the Dynamic Graph eXtension lane staged for a
// named human to promote, NOT MEASURED coverage and NOT authored facts. It is JOINED to
// /api/coverage at the surface (each labelled), never fused: MEASURED coverage only
// changes when a human PROMOTES a candidate into the released graph.
const ProvisionalCoverageClass = "PROVISIONAL (status=candidate) — DGX-proposed mappings awaiting human promotion; NOT MEASURED coverage, NOT authored"

const provisionalCoverageNote = "What the Dynamic Graph eXtension lane has PROVISIONALLY mapped (doc 20): how many " +
	"quarantined stray metrics now have a provisional node, how many are mapped into a group via an associated-with edge " +
	"(the >=2-shared-coordinate identification floor met), how many remain unresolved (below the floor — surfaced alone, " +
	"ambiguity stated), plus the agent/modality proposals (associated-with edges, direction-free co-occurrence " +
	"hypotheses, audit change->incident hypotheses, trace topology edges). Every count is status=candidate — the " +
	"deterministic detection/forecast path never reads these, and MEASURED coverage improves ONLY when a human promotes " +
	"a candidate through the governance gate."

const provisionalCoverageOffNote = "The Dynamic Graph eXtension lane is not enabled (--dgx-enabled): there is no " +
	"candidate store, so nothing is provisionally mapped. MEASURED coverage and detection are unaffected."

// ProvisionalCoverageView reconciles the candidate store into a coverage picture: how
// much of the previously-unmapped/dark surface the DGX lane has now PROVISIONALLY
// classified. Built from already-mapped CandidateRows (api never imports internal/
// candidate — main passes the rows), so the read-firewall holds.
type ProvisionalCoverageView struct {
	Class       string    `json:"class"`
	Available   bool      `json:"available"`
	GeneratedAt time.Time `json:"generatedAt"`

	// Stray-metric reconciliation (the CEI-fallback ER, doc 20 P1). By construction
	// classified == mappedToGroup + unresolved.
	StraysClassified    int `json:"straysClassified"`    // quarantined strays given a provisional node
	StraysMappedToGroup int `json:"straysMappedToGroup"` // strays linked to a real entity (>=2-coordinate floor met)
	StraysUnresolved    int `json:"straysUnresolved"`    // strays surfaced alone (below the identification floor)

	// Agent + modality proposals (doc 20 P3/P4).
	AgentEdges         int `json:"agentEdges"`         // associated-with edges the LLM agent staged (raw count)
	AgentEdgesDistinct int `json:"agentEdgesDistinct"` // distinct edge subjects (the same edge re-proposed across cycles is one promotion item)
	AgentHypotheses    int `json:"agentHypotheses"`    // direction-free co-occurrence hypotheses the agent proposed
	AuditHypotheses    int `json:"auditHypotheses"`    // change->incident co-occurrence hypotheses (audit lane)
	TraceTopology      int `json:"traceTopology"`      // observed service-call topology edges (trace lane)

	// Non-operational strays EXCLUDED at the candidate seam (not saved). These never enter the
	// counts above (they were never staged) — surfaced separately so the reconciliation
	// classified == mappedToGroup + unresolved still holds over what WAS staged, while the
	// operator still sees what was filtered out and why. Distinct series since process start.
	StraysExcludedNonOperational int            `json:"straysExcludedNonOperational"`
	ExcludedByClass              map[string]int `json:"excludedByClass,omitempty"`
	ExcludedNote                 string         `json:"excludedNote,omitempty"`

	TotalCandidates int            `json:"totalCandidates"`
	ByStatus        map[string]int `json:"byStatus"` // candidate | promoted | rejected | shadow
	BySource        map[string]int `json:"bySource"`
	Note            string         `json:"note"`
}

// SetNonOperationalExcluded records the strays classified non-operational and excluded from
// staging (the exporter's own runtime, client-library plumbing, control-plane component
// internals). main calls this from the deterministic classifier's tally. A no-op at zero.
func (v *ProvisionalCoverageView) SetNonOperationalExcluded(total int, byClass map[string]int) {
	if v == nil || total <= 0 {
		return
	}
	v.StraysExcludedNonOperational = total
	v.ExcludedByClass = byClass
	v.ExcludedNote = "Stray series classified non-operational (runtime/process introspection, client-library " +
		"plumbing, control-plane component internals) and excluded from staging — they can never bind to a workload/" +
		"node/storage entity. NOT included in the candidate counts above (never saved); reclaimable if a control-plane " +
		"modality is later authored. Distinct series since process start."
}

// BuildProvisionalCoverage reconciles the candidate rows into the provisional-coverage
// picture. Pure + deterministic. A stray (a cei-fallback provisional NODE) counts as
// "mapped to a group" when an associated-with EDGE exists for it (its subject is the
// node subject + " ~> <entity>"); otherwise it is "unresolved" (the identification floor
// was not met — surfaced alone, never guessed).
func BuildProvisionalCoverage(now time.Time, rows []CandidateRow) *ProvisionalCoverageView {
	v := &ProvisionalCoverageView{
		Class: ProvisionalCoverageClass, Available: true, GeneratedAt: now,
		ByStatus: map[string]int{}, BySource: map[string]int{},
		TotalCandidates: len(rows), Note: provisionalCoverageNote,
	}
	mappedStray := map[string]bool{}
	agentEdgeSubjects := map[string]bool{}
	for _, r := range rows {
		if r.Source == "cei-fallback" && r.Kind == "edge" {
			if i := strings.Index(r.Subject, " ~> "); i > 0 {
				mappedStray[r.Subject[:i]] = true
			}
		}
		if r.Source == "dgx-agent" && r.Kind == "edge" {
			agentEdgeSubjects[r.Subject] = true
		}
	}
	v.AgentEdgesDistinct = len(agentEdgeSubjects)
	for _, r := range rows {
		v.ByStatus[r.Status]++
		v.BySource[r.Source]++
		switch {
		case r.Source == "cei-fallback" && r.Kind == "node":
			v.StraysClassified++
			if mappedStray[r.Subject] {
				v.StraysMappedToGroup++
			} else {
				v.StraysUnresolved++
			}
		case r.Source == "dgx-agent" && r.Kind == "edge":
			v.AgentEdges++
		case r.Source == "dgx-agent" && r.Kind == "causal_hypothesis":
			v.AgentHypotheses++
		case r.Source == "audit":
			v.AuditHypotheses++
		case r.Source == "trace" && r.Relation == "topology":
			v.TraceTopology++
		}
	}
	return v
}

// UnavailableProvisionalCoverage is the honest OFF state (the DGX lane is not enabled).
func UnavailableProvisionalCoverage(now time.Time) *ProvisionalCoverageView {
	return &ProvisionalCoverageView{
		Class: ProvisionalCoverageClass, Available: false, GeneratedAt: now,
		ByStatus: map[string]int{}, BySource: map[string]int{}, Note: provisionalCoverageOffNote,
	}
}
