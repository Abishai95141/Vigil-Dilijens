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
