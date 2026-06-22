package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"

	vapi "github.com/Abishai95141/Vigil-Dilijens/obsd/internal/api"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/candidate"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/dgx"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/flow"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
)

// dgx tool adapters (doc 21 Phase 2). The dgx package defines the read-only Tool CONTRACT;
// the IMPLEMENTATIONS that read the api views / stores live HERE in package main so the
// import firewall holds — internal/dgx never imports internal/api. Each tool renders a
// read-only snapshot into []dgx.Observation, every row carrying a STABLE ref the agent may
// then cite (the ref enters the agent's accumulating grounding index). No tool ever writes.

// dgxToolRowCap bounds how many rows one tool returns into the prompt (keeps the running
// multi-turn context under the model's TPM limit; truncation is the caller's concern).
const dgxToolRowCap = 40

// Ledger bucket caps — the most-recent N of each, so the prompt memory stays bounded.
const (
	dgxLedgerPromotedCap = 20
	dgxLedgerRejectedCap = 30
	dgxLedgerPendingCap  = 20
)

var dgxEmptyToolParams = json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)

// dgxTool is a thin func-backed dgx.Tool. Parameters is the empty object schema (these tools
// take no arguments — they return a bounded read-only snapshot).
type dgxTool struct {
	name string
	desc string
	call func(ctx context.Context, args json.RawMessage) ([]dgx.Observation, error)
}

func (t dgxTool) Name() string                { return t.name }
func (t dgxTool) Description() string         { return t.desc }
func (t dgxTool) Parameters() json.RawMessage { return dgxEmptyToolParams }
func (t dgxTool) Call(ctx context.Context, a json.RawMessage) ([]dgx.Observation, error) {
	return t.call(ctx, a)
}

// newDGXToolRegistry builds the read-only retrieval surface (doc 21 §2.2b). Each tool closes
// over an immutable snapshot func (the SAME atomic views the api/MCP serve), so there is ONE
// source of truth and no writer in scope. Nil views/stores degrade to an honest empty result.
func newDGXToolRegistry(
	cs *candidate.Store,
	g *graph.Graph,
	coverage *atomic.Pointer[vapi.CoverageView],
	silence *atomic.Pointer[vapi.SilenceLedgerView],
	topo *atomic.Pointer[vapi.TopologyView],
	unexp *atomic.Pointer[vapi.UnexplainedView],
	dep *atomic.Pointer[vapi.DependencyView],
	rightsizing *atomic.Pointer[vapi.RightSizingView],
	crossSvc *atomic.Pointer[flow.Chain],
	transitive *atomic.Pointer[[]flow.Chain],
) *dgx.ToolRegistry {
	return dgx.NewToolRegistry(
		dgxTool{
			name: "get_strays",
			desc: "MEASURED. Unmapped OPERATIONAL stray metrics the deterministic resolver could not join (their names did not reach the identity floor). Each is a candidate to map into an equivalence group (kind equiv_group) or to an entity (associated-with edge). Refs: stray:<metric>/<hash>.",
			call: func(context.Context, json.RawMessage) ([]dgx.Observation, error) {
				return unmappedStrayObservations(cs, dgxAgentStrayCap), nil
			},
		},
		dgxTool{
			name: "search_equivalence_groups",
			desc: "AUTHORED. The equivalence-group catalog: each group's id, canonical OTel name, and an example dialect pattern. Use it to pick the group an operational stray belongs to (equiv_group), or to confirm none fits before proposing a new EQG_ id. Refs: group:<id>.",
			call: func(context.Context, json.RawMessage) ([]dgx.Observation, error) {
				return equivGroupObservations(g), nil
			},
		},
		dgxTool{
			name: "get_topology",
			desc: "MEASURED. The bound cluster graph: workloads/nodes/services (with health marks) and their edges. Use it to find the REAL entity key a stray belongs to (then cite entity:<key> in an associated-with edge) and to reason about dependencies. Refs: entity:<ceiKey>, edge:<from>~<to>.",
			call: func(context.Context, json.RawMessage) ([]dgx.Observation, error) {
				return topologyObservations(loadView(topo)), nil
			},
		},
		dgxTool{
			name: "get_silence_ledger",
			desc: "MEASURED (own coverage). The deterministic absence ledger: (entity,variable) pairs that are NOT watched, each with an exact reason (unbounded / no-stream-key / unresolved / out-of-scope). The primary coverage-gap finder. Refs: silence:<metric>.",
			call: func(context.Context, json.RawMessage) ([]dgx.Observation, error) {
				return silenceObservations(loadView(silence)), nil
			},
		},
		dgxTool{
			name: "get_coverage",
			desc: "MEASURED (own coverage). Per-phenomenon observability (full/partial/none) with the missing-signal reasons, plus the headline rollup. See which phenomena are partial/none before proposing. Refs: phenomenon:<id>, coverage:summary.",
			call: func(context.Context, json.RawMessage) ([]dgx.Observation, error) {
				return coverageObservations(loadView(coverage)), nil
			},
		},
		dgxTool{
			name: "get_unexplained",
			desc: "MEASURED. The loud-but-unmatched channel: signals anomalous against a resolved bar that match NO known phenomenon, plus recurring candidate patterns for curation. Read as 'there is more here than the known patterns explain' — co-occurrences, never causes. Refs: unexplained:<scope>, uxcand:<metrics>.",
			call: func(context.Context, json.RawMessage) ([]dgx.Observation, error) {
				return unexplainedObservations(loadView(unexp)), nil
			},
		},
		dgxTool{
			name: "get_dependency",
			desc: "MEASURED. The metric-dependency graph (doc 20 P2): pairs of series that move together over the window, as undirected Pearson associations — NOT causal, the candidate INPUT for authoring a causal direction. Ranked by |coefficient|. Refs: dep:<a>~<b>.",
			call: func(context.Context, json.RawMessage) ([]dgx.Observation, error) {
				return dependencyObservations(loadView(dep)), nil
			},
		},
		dgxTool{
			name: "get_causal_hypotheses",
			desc: "PROJECTION ⋈ ASSOCIATION (direction-free). Cross-workload pairs that BOTH stepped (a co-onset) AND are associated, each with the MEASURED onset order and, when significant, the detrended lead-lag witness (lagPeakSeconds, lagConsistentWithOnset). The strongest evidence for a directed hypothesis — but the system NEVER infers the arrow; a named operator authors it. Refs: cohyp:<id>.",
			call: func(context.Context, json.RawMessage) ([]dgx.Observation, error) {
				return causalHypothesisObservations(cs), nil
			},
		},
		dgxTool{
			name: "get_rightsizing",
			desc: "MEASURED advisory (docs/31 §6). Per-(workload,resource) right-sizing: sustained p95 usage vs the workload's OWN declared request/limit, with the recommendation (reclaim | resize-up). Stability- and QoS-gated; unstable/churny series yield no number. Refs: rightsizing:<name>/<resource>.",
			call: func(context.Context, json.RawMessage) ([]dgx.Observation, error) {
				return rightsizingObservations(loadView(rightsizing)), nil
			},
		},
		dgxTool{
			name: "get_cross_service",
			desc: "MEASURED ⋈ AUTHORED (joined, never fused). The cross-service / transitive cascade: degraded callees reaching impacted callers over observed-flow edges, oriented only by the authored relation — independent faults stay separate. Use it to see a fault's blast radius before proposing an attribution. Refs: xsvc:<degraded>~<impacted>, chain:<root>.",
			call: func(context.Context, json.RawMessage) ([]dgx.Observation, error) {
				return crossServiceObservations(loadView(crossSvc), loadView(transitive)), nil
			},
		},
	)
}

// loadView reads an atomic view pointer, tolerating a nil pointer (lane not wired).
func loadView[T any](p *atomic.Pointer[T]) *T {
	if p == nil {
		return nil
	}
	return p.Load()
}

func equivGroupObservations(g *graph.Graph) []dgx.Observation {
	if g == nil || len(g.EquivalenceGroups) == 0 {
		return nil
	}
	ids := make([]string, 0, len(g.EquivalenceGroups))
	for id := range g.EquivalenceGroups {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) > dgxToolRowCap {
		ids = ids[:dgxToolRowCap]
	}
	out := make([]dgx.Observation, 0, len(ids))
	for _, id := range ids {
		eg := g.EquivalenceGroups[id]
		example := ""
		if len(eg.Patterns) > 0 {
			example = " e.g. " + eg.Patterns[0]
		}
		out = append(out, dgx.Observation{
			Ref:    "group:" + id,
			Kind:   "equivalence-group",
			Detail: eg.Label + " canonical=" + eg.CanonicalOTel + example,
		})
	}
	return out
}

func topologyObservations(v *vapi.TopologyView) []dgx.Observation {
	if v == nil {
		return nil
	}
	out := make([]dgx.Observation, 0, dgxToolRowCap)
	for _, n := range v.Nodes {
		if len(out) >= dgxToolRowCap {
			break
		}
		out = append(out, dgx.Observation{
			Ref:    "entity:" + n.CEIKey,
			Kind:   "topology-node",
			Detail: n.Kind + " " + n.Namespace + "/" + n.Name + topoMarks(n),
		})
	}
	for _, e := range v.Edges {
		if len(out) >= dgxToolRowCap*2 {
			break
		}
		out = append(out, dgx.Observation{
			Ref:    "edge:" + e.From + "~" + e.To,
			Kind:   "topology-edge",
			Detail: e.Type + " " + e.From + " -> " + e.To + " (" + e.Status + ")",
		})
	}
	sortObsByRef(out) // deterministic tool-result order regardless of the view's node ordering
	return out
}

// sortObsByRef gives a tool's observations a stable, view-order-independent order.
func sortObsByRef(obs []dgx.Observation) {
	sort.Slice(obs, func(i, j int) bool { return obs[i].Ref < obs[j].Ref })
}

func topoMarks(n vapi.TopoNode) string {
	var m []string
	if n.Matched {
		m = append(m, "matched")
	}
	if n.Degraded {
		m = append(m, "degraded")
	}
	if n.Loud {
		m = append(m, "loud")
	}
	if n.Warned {
		m = append(m, "warned")
	}
	if len(m) == 0 {
		return ""
	}
	return " [" + strings.Join(m, ",") + "]"
}

func silenceObservations(v *vapi.SilenceLedgerView) []dgx.Observation {
	if v == nil {
		return nil
	}
	out := make([]dgx.Observation, 0, dgxToolRowCap)
	for _, row := range v.Silent {
		if len(out) >= dgxToolRowCap {
			break
		}
		out = append(out, dgx.Observation{
			Ref:    "silence:" + row.Metric,
			Kind:   "coverage-gap",
			Detail: row.Metric + " on " + row.Entity + ": " + row.Reason,
		})
	}
	return out
}

func coverageObservations(v *vapi.CoverageView) []dgx.Observation {
	if v == nil {
		return nil
	}
	out := make([]dgx.Observation, 0, dgxToolRowCap+1)
	s := v.Summary
	out = append(out, dgx.Observation{
		Ref:  "coverage:summary",
		Kind: "coverage-summary",
		Detail: fmt.Sprintf("phenomena full=%d partial=%d none=%d; resolvability=%.2f configBound=%d/%d",
			s.PhenomenaFull, s.PhenomenaPartial, s.PhenomenaNone, s.Resolvability, s.ConfigBound, s.ConfigEligible),
	})
	for _, row := range v.Phenomena {
		if len(out) > dgxToolRowCap {
			break
		}
		detail := fmt.Sprintf("%s: %s (%d/%d required observable)", row.Label, row.Observability, row.RequiredOk, row.RequiredTotal)
		if len(row.MissingReasons) > 0 {
			detail += " missing: " + strings.Join(row.MissingReasons, "; ")
		}
		out = append(out, dgx.Observation{Ref: "phenomenon:" + row.ID, Kind: "phenomenon-coverage", Detail: detail})
	}
	return out
}

func unexplainedObservations(v *vapi.UnexplainedView) []dgx.Observation {
	if v == nil {
		return nil
	}
	out := make([]dgx.Observation, 0, dgxToolRowCap)
	for _, card := range v.OpenCards {
		if len(out) >= dgxToolRowCap {
			break
		}
		out = append(out, dgx.Observation{
			Ref:    "unexplained:" + card.Scope,
			Kind:   "unexplained-card",
			Detail: card.Kind + " " + card.Namespace + "/" + card.Name + ": " + card.MatchCheck,
		})
	}
	for _, c := range v.Candidates {
		if len(out) >= dgxToolRowCap+15 {
			break
		}
		out = append(out, dgx.Observation{
			Ref:    "uxcand:" + strings.Join(c.Metrics, ","),
			Kind:   "unexplained-candidate",
			Detail: fmt.Sprintf("recurs on %s, %d entities: %s", c.EntityKind, len(c.Entities), c.Rationale),
		})
	}
	sortObsByRef(out) // deterministic tool-result order regardless of the view's card ordering
	return out
}

// --- P3 (doc 33 §2.2) inference-signal tools ----------------------------------
// These widen the agent's INPUT to the inference-grade lanes Vigil already computes:
// the association graph, the direction-free causal hypotheses (with the lead-lag witness),
// the right-sizing advisories, and the cross-service / transitive cascade. All read-only;
// each row carries a stable ref the agent may cite. None is causal — the agent may PROPOSE
// a direction (a named human authors it), it never asserts one here.

func absf(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// shortCEI renders a long CEI (i|cluster|ns|Kind|name|uid|/metric) as ns/name:metric for
// readable observation detail; the full key stays in the ref so grounding is exact.
func shortCEI(s string) string {
	parts := strings.Split(s, "|")
	if len(parts) < 3 {
		return s
	}
	ns, name, metric := parts[2], "", parts[len(parts)-1]
	if len(parts) >= 5 {
		name = parts[4]
	}
	return ns + "/" + name + ":" + metric
}

func dependencyObservations(v *vapi.DependencyView) []dgx.Observation {
	if v == nil || len(v.Edges) == 0 {
		return nil
	}
	edges := append([]vapi.DependencyEdge(nil), v.Edges...)
	sort.Slice(edges, func(i, j int) bool { return absf(edges[i].Coefficient) > absf(edges[j].Coefficient) })
	const depLimit = 25 // the strongest associations only — keeps the prompt bounded
	if len(edges) > depLimit {
		edges = edges[:depLimit]
	}
	out := make([]dgx.Observation, 0, len(edges))
	for _, e := range edges {
		out = append(out, dgx.Observation{
			Ref:    "dep:" + e.A + "~" + e.B,
			Kind:   "association",
			Detail: fmt.Sprintf("%s ~ %s  r=%.2f overlap=%d (undirected, NOT causal — a candidate input for authoring direction)", shortCEI(e.A), shortCEI(e.B), e.Coefficient, e.Overlap),
		})
	}
	return out
}

func causalHypothesisObservations(cs *candidate.Store) []dgx.Observation {
	if cs == nil {
		return nil
	}
	rows, err := cs.List(candidate.Filter{Kind: candidate.KindCausalHypothesis, Status: candidate.StatusCandidate})
	if err != nil {
		return nil
	}
	hrows := mapCausalHypothesisRows(rows)
	if len(hrows) > dgxToolRowCap {
		hrows = hrows[:dgxToolRowCap]
	}
	out := make([]dgx.Observation, 0, len(hrows))
	for _, h := range hrows {
		witness := "no significant lead-lag witness"
		if h.LagConsistentWithOnset != nil {
			agree := "DISAGREES with onset order"
			if *h.LagConsistentWithOnset {
				agree = "AGREES with onset order"
			}
			witness = fmt.Sprintf("lead-lag %+ds (%s)", h.LagPeakSeconds, agree)
		}
		first := h.ObservedFirst
		if first == "" {
			first = "n/a"
		}
		out = append(out, dgx.Observation{
			Ref:  "cohyp:" + h.ID,
			Kind: "causal-hypothesis",
			Detail: fmt.Sprintf("%s ~ %s  r=%.2f  onset-first=%s Δ%ds  %s — DIRECTION-FREE (operator authors the arrow)",
				shortCEI(h.A), shortCEI(h.B), h.Coefficient, first, h.DeltaSeconds, witness),
		})
	}
	return out
}

func rightsizingObservations(v *vapi.RightSizingView) []dgx.Observation {
	if v == nil || len(v.Advisories) == 0 {
		return nil
	}
	out := make([]dgx.Observation, 0, dgxToolRowCap)
	for _, a := range v.Advisories {
		if a.Action != "reclaim" && a.Action != "resize-up" {
			continue // only the actionable recommendations
		}
		if len(out) >= dgxToolRowCap {
			break
		}
		out = append(out, dgx.Observation{
			Ref:  "rightsizing:" + a.Name + "/" + a.Resource,
			Kind: "rightsizing-advisory",
			Detail: fmt.Sprintf("%s %s/%s: p95=%.0f%s vs request=%d limit=%d -> recommend %d (%s, stable=%v)",
				strings.ToUpper(a.Action), a.Name, a.Resource, a.P95, a.Unit, a.Request, a.Limit, a.Recommended, a.QoS, a.Stable),
		})
	}
	return out
}

func crossServiceObservations(chain *flow.Chain, transitive *[]flow.Chain) []dgx.Observation {
	out := make([]dgx.Observation, 0, dgxToolRowCap)
	seen := map[string]bool{}
	add := func(ref, kind, detail string) {
		if seen[ref] || len(out) >= dgxToolRowCap {
			return
		}
		seen[ref] = true
		out = append(out, dgx.Observation{Ref: ref, Kind: kind, Detail: detail})
	}
	if chain != nil && chain.MostUpstreamDegradedNode != "" {
		add("chain:"+chain.MostUpstreamDegradedNode, "cross-service-root",
			"most-upstream degraded: "+chain.MostUpstreamDegradedNode+" (STRUCTURAL fan-in position, not a cause)")
		for _, h := range chain.Links {
			add("xsvc:"+h.Degraded+"~"+h.Impacted, "cross-service-edge",
				h.Degraded+" --observed-flow--> "+h.Impacted+" (impact travels against the call arrow; AUTHORED relation)")
		}
	}
	if transitive != nil {
		for _, c := range *transitive {
			for _, p := range c.Path {
				add("xsvc:"+p.Upstream+"~"+p.Downstream, "transitive-chain-edge",
					fmt.Sprintf("hop %d: %s(%s) -> %s(%s)", p.Hop, p.Upstream, p.UpstreamPhenomenon, p.Downstream, p.DownstreamPhenomenon))
			}
		}
	}
	sortObsByRef(out)
	return out
}

// buildLedger renders the agent's OWN candidate store into the prior-proposal ledger (doc 21
// §2.2c). candidate.Store.List returns rows ordered (created_at, id) — deterministic — so the
// "most recent N" of each bucket is stable. Read-only; this is memory, never a gate.
func buildLedger(cs *candidate.Store) dgx.Ledger {
	if cs == nil {
		return dgx.Ledger{}
	}
	return dgx.Ledger{
		Promoted: ledgerBucket(cs, candidate.StatusPromoted, dgxLedgerPromotedCap),
		Rejected: ledgerBucket(cs, candidate.StatusRejected, dgxLedgerRejectedCap),
		Pending:  ledgerBucket(cs, candidate.StatusCandidate, dgxLedgerPendingCap),
	}
}

func ledgerBucket(cs *candidate.Store, st candidate.Status, max int) []dgx.LedgerEntry {
	rows, err := cs.List(candidate.Filter{Status: st})
	if err != nil || len(rows) == 0 {
		return nil
	}
	if len(rows) > max {
		rows = rows[len(rows)-max:] // the most-recent max (List is chronological)
	}
	out := make([]dgx.LedgerEntry, 0, len(rows))
	for _, r := range rows {
		out = append(out, dgx.LedgerEntry{Kind: string(r.Kind), Subject: r.Subject, Reason: r.Reason, Note: r.Note})
	}
	return out
}
