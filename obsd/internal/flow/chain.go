package flow

import (
	"encoding/json"
	"strings"
	"time"
)

// Chain is the deterministic, charter-clean output of the reverse walk. It JOINS
// three labelled parts and never fuses them into a causal sentence: the MEASURED
// observed-flow edge, the AUTHORED "why", and the structural position. The tokens
// "cause"/"caused"/"root cause" appear NOWHERE in it (asserted by the test).
type Chain struct {
	// MostUpstreamDegradedNode is a STRUCTURAL position, not a causal claim: the
	// degraded node reaching the most impacted callers with no inbound flow-symptom
	// edge of its own.
	MostUpstreamDegradedNode string `json:"most_upstream_degraded_node"`
	NodeClass                string `json:"node_class"` // "MEASURED (structural fan-in over observed-flow edges)"
	NodeBasis                string `json:"node_basis"`

	Links        []Link       `json:"chain"`
	Symptoms     []SymptomOut `json:"symptoms"`
	CoverageGaps CoverageOut  `json:"coverage_gaps"`
	GeneratedAt  time.Time    `json:"generated_at"`
}

// Link is one impacted-caller ← degraded-callee pairing over an observed-flow edge.
type Link struct {
	Impacted      string `json:"impacted"`       // namespace/workload of the caller
	Degraded      string `json:"degraded"`       // namespace/workload of the degraded callee
	EdgeClass     string `json:"edge_class"`     // "MEASURED observed flow"
	ServicePorts  []int  `json:"service_ports"`  // the dialed service ports
	ConnDepth     int    `json:"conn_depth"`     // MEASURED conntrack depth on this edge (caller-side)
	EdgeTraversal string `json:"edge_traversal"` // "valid" | "suspect" (the validity contract)
	Why           string `json:"why"`            // verbatim AUTHORED note
	WhyClass      string `json:"why_class"`      // "AUTHORED"
	Temporal      string `json:"temporal"`       // "T0->T0+"
	Author        string `json:"author"`
	Version       string `json:"version"`
}

// SymptomOut is one MEASURED finding fed to the walk, with its provenance class.
type SymptomOut struct {
	Workload   string `json:"workload"`
	Phenomenon string `json:"phenomenon"`
	Class      string `json:"class"`  // "MEASURED" (or "SYNTHETIC" for a unit-test seed)
	Detail     string `json:"detail"` // the measured basis, e.g. "cpu throttle ratio 0.42 > 0.25 bar"
}

// CoverageOut is the honest recovery accounting, surfaced as first-class gaps.
type CoverageOut struct {
	ResolvableFlows int `json:"resolvable_flows"`
	SnatMaskedFlows int `json:"snat_masked_flows"`
	UnresolvedFlows int `json:"unresolved_flows"`
	InfraFlows      int `json:"infra_flows"`
	UnrepliedFlows  int `json:"unreplied_flows"`
	Snapshots       int `json:"snapshots"`
}

// JSON renders the chain deterministically (indented, stable key order via structs).
func (c Chain) JSON() ([]byte, error) { return json.MarshalIndent(c, "", "  ") }

// forbiddenTokens are causal-claim words the surface must never emit on its own.
var forbiddenTokens = []string{"cause", "caused", "causes", "causing", "root cause", "because of", "due to"}

// HasForbiddenToken reports whether a rendered chain contains any causal-claim
// token (the AUTHORED why is curated text and is excluded from this check by the
// caller, which scans only the generated scaffolding).
func HasForbiddenToken(s string) (string, bool) {
	low := strings.ToLower(s)
	for _, t := range forbiddenTokens {
		if strings.Contains(low, t) {
			return t, true
		}
	}
	return "", false
}
