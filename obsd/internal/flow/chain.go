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

	// Path is the ORDERED transitive root-cause chain (doc 15 cap. B): the one-hop
	// fan-in (Links) made transitive. Each PathStep is one impact edge from a more-
	// upstream degraded node to a more-downstream degraded node, oriented ONLY by the
	// authored relation (never by timing). Empty for the one-hop CrossServiceChain (so
	// its output stays byte-identical) — populated only by TransitiveChains.
	Path []PathStep `json:"path,omitempty"`
	// Gaps are the honest breaks in the asserted chain (doc 15 cap. B §1-2): a silent
	// intermediate (a flow-adjacent node carrying no measured degradation — traversed for
	// connectivity but NEVER bridged, the weakest-input rule) or a hop ceiling. Stated,
	// never hidden. Empty for CrossServiceChain.
	Gaps []ChainGap `json:"gaps,omitempty"`
}

// PathStep is one ORDERED impact edge in a transitive root-cause chain (doc 15 cap.
// B): the AUTHORED relation declares that an upstream degraded node's trouble
// propagates to its downstream caller over the MEASURED observed-flow edge. Each clause
// is labelled and never fused into a causal sentence — the upstream and downstream each
// carry their OWN measured phenomenon; the only "why" is the verbatim authored note.
type PathStep struct {
	Hop                  int    `json:"hop"`                   // 1-based BFS depth from the chain root
	Upstream             string `json:"upstream"`              // ns/workload — the root-ward degraded node (the callee)
	UpstreamPhenomenon   string `json:"upstream_phenomenon"`   // its OWN MEASURED finding
	Downstream           string `json:"downstream"`            // ns/workload — the impacted degraded node (the caller)
	DownstreamPhenomenon string `json:"downstream_phenomenon"` // its OWN MEASURED finding
	EdgeClass            string `json:"edge_class"`            // "MEASURED observed flow"
	EdgeTraversal        string `json:"edge_traversal"`        // "valid" | "suspect" (the validity contract)
	Why                  string `json:"why"`                   // verbatim AUTHORED relation note
	WhyClass             string `json:"why_class"`             // "AUTHORED"
	Temporal             string `json:"temporal"`
	Author               string `json:"author"`
	Version              string `json:"version"`
}

// ChainGap is a stated break in the asserted chain (doc 15 cap. B §2): the chain is
// NEVER bridged across an unmeasured node or a hop ceiling — the silence is surfaced
// as a first-class gap on a chain that has at least one asserted step, never silently
// spanned (the weakest-input rule). (A purely-silent component — no asserted step at all
// — currently reports as no-chain rather than a lone gap; surfacing that is a follow-up.)
type ChainGap struct {
	From   string `json:"from"`   // ns/workload — the degraded node whose asserted chain ends here
	To     string `json:"to"`     // ns/workload — the SILENT flow-adjacent node, or "" for a hop ceiling
	Reason string `json:"reason"` // the honest explanation
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

// forbiddenTokens are causal-claim words the SYSTEM-GENERATED scaffolding must never
// emit on its own (the charter ban on the system fabricating "X caused Y").
var forbiddenTokens = []string{"cause", "caused", "causes", "causing", "root cause", "because of", "due to"}

// HasForbiddenToken reports whether a string contains any causal-claim token. It is a
// generic scanner over arbitrary text; the charter contract is that callers scan only
// the SYSTEM-GENERATED scaffolding — the AUTHORED `why` is curated, governance-reviewed
// text surfaced VERBATIM and is NOT subject to this guard (the guard catches the system
// fabricating a causal sentence, never censors the curator). Use ScaffoldingForCharter to
// strip the authored notes before scanning a Chain.
func HasForbiddenToken(s string) (string, bool) {
	low := strings.ToLower(s)
	for _, t := range forbiddenTokens {
		if strings.Contains(low, t) {
			return t, true
		}
	}
	return "", false
}

// ScaffoldingForCharter returns a copy of the chain with every AUTHORED verbatim note
// (`Why`) blanked, so a charter scan (HasForbiddenToken) sees ONLY the system-generated
// scaffolding. A curator's governed relation note is surfaced verbatim and must not be
// censored by the generated-text guard; this is the helper the comment on
// HasForbiddenToken refers to. The copy is shallow-but-safe: the slices are rebuilt so
// the original chain is never mutated.
func ScaffoldingForCharter(c Chain) Chain {
	out := c
	if len(c.Links) > 0 {
		out.Links = make([]Link, len(c.Links))
		copy(out.Links, c.Links)
		for i := range out.Links {
			out.Links[i].Why = ""
		}
	}
	if len(c.Path) > 0 {
		out.Path = make([]PathStep, len(c.Path))
		copy(out.Path, c.Path)
		for i := range out.Path {
			out.Path[i].Why = ""
		}
	}
	return out
}
