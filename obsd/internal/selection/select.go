package selection

import (
	"sort"
	"strings"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/binding"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
)

// Tier is the monitoring intensity (doc 06 §3.3).
type Tier string

const (
	TierA    Tier = "A-detection"
	TierB    Tier = "B-forecasting" // structurally present; EMPTY until Phase 2 (09 + budget)
	TierNone Tier = "none"
)

// Reason is the selection reason code (doc 06 §3.5). Exhaustive for M1; the
// M4 codes (critical-role, operator-pinned, out-of-scope) join with their
// milestone.
type Reason string

const (
	// ReasonPhenomenonMember: the entity's bound variables back member signals
	// of at least one phenomenon — attention earned from the graph.
	ReasonPhenomenonMember Reason = "phenomenon-member"
	// ReasonNoPhenomenonMember: evaluable variables exist, but none backs any
	// phenomenon — nothing in the graph gives them diagnostic meaning yet.
	ReasonNoPhenomenonMember Reason = "no-phenomenon-member"
	// ReasonNoEvaluableVariable: variables were instantiated for the entity but
	// none is evaluable (unbounded / out-of-scope / unresolved / QA-failed) —
	// the coverage gate's visible exclusion.
	ReasonNoEvaluableVariable Reason = "no-evaluable-variable"
	// ReasonStructuralEntity: the entity's kind carries no instrumented
	// variables at all (Services, roles, policies — structural topology).
	ReasonStructuralEntity Reason = "structural-entity"
	// ReasonBindingsStale: the bound graph predates this entity (a re-bind is
	// pending or failed) — a statement about BINDING freshness, never about the
	// entity's kind. Selection records are MEASURED facts; without this code a
	// just-created Pod would be branded structural-entity, which is false.
	ReasonBindingsStale Reason = "bindings-stale"
)

// Topology is the validity-aware topology view neighbourhood closure walks
// (doc 06 §3.2): selection expands each chosen entity out to the spans of the
// phenomena it participates in, honouring edge validity (03 §3.5). The live
// EdgeStore satisfies it; closure is an attention/audit surface, never part of
// the deterministic digest path (TierASet stays pure of topology).
type Topology interface {
	Neighbours(typ identity.EdgeType, fromKey string, w identity.TimeWindow) []identity.Neighbour
	NeighboursInto(typ identity.EdgeType, toKey string, w identity.TimeWindow) []identity.Neighbour
	Sources(typ identity.EdgeType) []string
}

// NeighbourRef is one entity pulled into the watch set by span expansion, with
// the edge path that earned it (doc 06 §3.5 "Neighbourhood CEIs ... with the
// edge paths used").
type NeighbourRef struct {
	CEIKey       string
	Via          string // edge type crossed
	Result       string // valid | suspect (absent edges never pull anyone in)
	Hop          int    // 1 for first-order spans, up to 2 for second-order
	ForPhenomena []string
}

// ClosureGap records a spanned phenomenon whose required neighbourhood is NOT
// reachable from a selected entity — the doc 06 M2 exit demands this absence be
// a recorded coverage fact, never a silent drop.
type ClosureGap struct {
	EntityCEI  string
	Phenomenon string
	Reason     string
}

// Record is the monitoring selection record (doc 06 §3.5): one entity's
// attention verdict, with the reason attached. MEASURED-class.
type Record struct {
	EntityCEI      string
	RoleCEI        string
	Kind           string
	Namespace      string
	Name           string
	PassesCoverage bool
	Phenomena      []string // sorted phenomenon IDs that earned the attention
	Tier           Tier
	Reason         Reason
	Neighbourhood  []NeighbourRef // span expansion (06 M2); empty without topology
	SelectedAt     time.Time
	Trigger        string
}

// Result is one selection pass over the inventory. Every inventory entity has
// exactly one record — hidden gaps are impossible by construction.
type Result struct {
	Records      []Record // sorted by EntityCEI
	TierACount   int
	NoneByReason map[Reason]int
	ClosureGaps  []ClosureGap // spanned phenomena whose neighbourhood is unreachable
	GraphVersion string
	SelectedAt   time.Time
	Trigger      string
}

// TierASet is the deterministic core of the funnel: gates 1–2 computed purely
// from (bindings, graph) — the exact inputs a replay bundle carries — returning
// entity CEI key -> sorted participating phenomenon IDs. Detection consumes
// this; replay reproduces it.
func TierASet(res *binding.Result, g *graph.Graph) map[string][]string {
	if res == nil || g == nil {
		return map[string][]string{}
	}
	// signal -> phenomena that declare it a member (participates_in edges), and
	// metric -> phenomena whose authored detection CHECKS consult that metric.
	// The union matters: the matcher fires on check METRICS, so for the funnel
	// to provably cover everything detection could fire on, any entity whose
	// bound metric backs a check must participate — even if its binding rides a
	// rule whose signal happens not to be a member of that phenomenon.
	sigPhen := map[string][]string{}
	metricPhen := map[string][]string{}
	phenIDs := make([]string, 0, len(g.Phenomena))
	for id := range g.Phenomena {
		phenIDs = append(phenIDs, id)
	}
	sort.Strings(phenIDs) // deterministic accumulation order
	for _, id := range phenIDs {
		for _, m := range g.Phenomena[id].Members {
			sigPhen[m.SignalID] = append(sigPhen[m.SignalID], id)
		}
		for _, c := range g.ChecksFor(id) {
			metricPhen[c.Metric] = append(metricPhen[c.Metric], id)
		}
	}
	// rule -> signal (the authored bridge each bound variable rides on).
	ruleSig := make(map[string]string, len(g.Rules))
	for _, r := range g.Rules {
		ruleSig[r.ID] = r.Signal
	}

	out := map[string][]string{}
	seen := map[string]map[string]bool{}
	add := func(ceiKey string, phens []string) {
		for _, phen := range phens {
			if seen[ceiKey] == nil {
				seen[ceiKey] = map[string]bool{}
			}
			if !seen[ceiKey][phen] {
				seen[ceiKey][phen] = true
				out[ceiKey] = append(out[ceiKey], phen)
			}
		}
	}
	for i := range res.Bindings {
		b := &res.Bindings[i]
		// Gate 1: evaluable bound variable (the fingerprint-eligible filter,
		// mirrored from observe.Materialize).
		if b.State != binding.StateBound || b.Bar == nil || b.Validation == binding.ValidationFailed {
			continue
		}
		// Gate 2: the variable's signal is a member of >=1 phenomenon, OR its
		// metric backs a phenomenon's authored check (what the matcher fires on).
		add(b.CEIKey, sigPhen[ruleSig[b.RuleID]])
		add(b.CEIKey, metricPhen[b.Metric])
	}
	for k := range out {
		sort.Strings(out[k])
	}
	return out
}

// Select runs the funnel over the live inventory, producing one record per
// entity (the watch list AND the none-list, each with its reason). The Tier-A
// verdicts come from TierASet — the same deterministic core detection uses.
// With a topology view, Tier-A records additionally carry their neighbourhood
// closure (06 M2): the entities each spanned phenomenon needs, or a recorded
// gap where none is reachable. topo == nil skips closure (stated by the empty
// neighbourhoods, never fabricated).
//
// staleBindings says res was compiled for an EARLIER inventory (a re-bind is
// pending or failed): entities the bound graph has never seen then get the
// truthful bindings-stale reason instead of a false structural-entity verdict.
func Select(inventory []identity.InstanceRecord, res *binding.Result, g *graph.Graph, staleBindings bool, now time.Time, trigger string, topo Topology, w identity.TimeWindow) *Result {
	tierA := TierASet(res, g)

	// Coverage and instantiation facts per entity, from the binding states.
	covered := map[string]bool{}      // >=1 evaluable bound variable
	instantiated := map[string]bool{} // >=1 binding in ANY state (instrumentable kind)
	if res != nil {
		for i := range res.Bindings {
			b := &res.Bindings[i]
			instantiated[b.CEIKey] = true
			if b.State == binding.StateBound && b.Bar != nil && b.Validation != binding.ValidationFailed {
				covered[b.CEIKey] = true
			}
		}
	}

	r := &Result{
		NoneByReason: map[Reason]int{},
		SelectedAt:   now,
		Trigger:      trigger,
	}
	if g != nil {
		r.GraphVersion = g.Version
	}
	for _, inst := range inventory {
		key := inst.CEI.Key()
		rec := Record{
			EntityCEI: key,
			Kind:      inst.Kind, Namespace: inst.Namespace, Name: inst.Name,
			SelectedAt: now, Trigger: trigger,
		}
		if inst.RoleCEI.Kind != "" { // kinds with no role layer (e.g. Node) have a zero RoleCEI
			rec.RoleCEI = inst.RoleCEI.Key()
		}
		switch {
		case len(tierA[key]) > 0:
			rec.PassesCoverage = true
			rec.Phenomena = tierA[key]
			rec.Tier = TierA
			rec.Reason = ReasonPhenomenonMember
			r.TierACount++
			if topo != nil && g != nil {
				var gaps []ClosureGap
				rec.Neighbourhood, gaps = expandNeighbourhood(key, inst.Kind, rec.Phenomena, g, topo, w)
				r.ClosureGaps = append(r.ClosureGaps, gaps...)
			}
		case covered[key]:
			rec.PassesCoverage = true
			rec.Tier = TierNone
			rec.Reason = ReasonNoPhenomenonMember
		case instantiated[key]:
			rec.Tier = TierNone
			rec.Reason = ReasonNoEvaluableVariable
		case staleBindings:
			// The bound graph has no row for this entity AND predates the current
			// inventory: the only truthful statement is that binding is stale —
			// never a claim about the entity's kind.
			rec.Tier = TierNone
			rec.Reason = ReasonBindingsStale
		default:
			rec.Tier = TierNone
			rec.Reason = ReasonStructuralEntity
		}
		if rec.Tier == TierNone {
			r.NoneByReason[rec.Reason]++
		}
		r.Records = append(r.Records, rec)
	}
	sort.Slice(r.Records, func(i, j int) bool { return r.Records[i].EntityCEI < r.Records[j].EntityCEI })
	sort.Slice(r.ClosureGaps, func(i, j int) bool {
		a, b := r.ClosureGaps[i], r.ClosureGaps[j]
		if a.EntityCEI != b.EntityCEI {
			return a.EntityCEI < b.EntityCEI
		}
		return a.Phenomenon < b.Phenomenon
	})
	return r
}

// expandNeighbourhood walks one Tier-A entity out to the spans of its
// participating phenomena (doc 06 §3.2): along each phenomenon's DECLARED
// traversal edge types, to its declared depth (1 for first-order, 2 for
// second-order), both directions, honouring edge validity over the window.
// Containers walk from their containing pod (containment is identity, not a
// hop — the spans overlay's addressing principle). Every spanned phenomenon
// with no reachable neighbourhood yields a recorded gap.
func expandNeighbourhood(key, kind string, phenomena []string, g *graph.Graph, topo Topology, w identity.TimeWindow) ([]NeighbourRef, []ClosureGap) {
	var refs []NeighbourRef
	var gaps []ClosureGap

	// Container-scope bindings carry the POD's CEI key (doc 04 compiler), so a
	// Container record usually walks from its own key. A true container-instance
	// key (uid = podUID/name) resolves its pod through runs-on sources.
	walkFrom := key
	if kind == "Container" {
		if uid := containerPodUID(key); uid != "" {
			walkFrom = ""
			for _, src := range topo.Sources(identity.EdgeRunsOn) {
				cei, err := identity.ParseKey(src)
				if err == nil && cei.Kind == "Pod" && cei.UID == uid {
					walkFrom = src
					break
				}
			}
		}
	}

	byRef := map[string]*NeighbourRef{} // (cei, via, hop) -> ref, phenomena merged
	for _, phen := range phenomena {
		p, ok := g.Phenomena[phen]
		if !ok || !p.HasSpan() || p.Span == graph.SpanEntityLocal {
			continue
		}
		if walkFrom == "" {
			gaps = append(gaps, ClosureGap{EntityCEI: key, Phenomenon: phen,
				Reason: "containing pod absent from topology"})
			continue
		}
		depth := 1
		if p.Span == graph.SpanSecondOrder {
			depth = 2
		}
		visited := map[string]bool{walkFrom: true, key: true}
		frontier := []string{walkFrom}
		reached := false
		for hop := 1; hop <= depth; hop++ {
			var next []string
			for _, from := range frontier {
				for _, typ := range p.TraversalEdgeTypes {
					et := identity.EdgeType(typ)
					nbrs := append(topo.Neighbours(et, from, w), topo.NeighboursInto(et, from, w)...)
					sort.Slice(nbrs, func(i, j int) bool { return nbrs[i].To.Key() < nbrs[j].To.Key() })
					for _, n := range nbrs {
						nk := n.To.Key()
						if hop == 1 {
							reached = true
						}
						if visited[nk] {
							continue
						}
						visited[nk] = true
						next = append(next, nk)
						rk := nk + "\x1f" + typ + "\x1f" + string(rune('0'+hop))
						ref, seen := byRef[rk]
						if !seen {
							ref = &NeighbourRef{CEIKey: nk, Via: typ, Result: n.Result.String(), Hop: hop}
							byRef[rk] = ref
						}
						ref.ForPhenomena = append(ref.ForPhenomena, phen)
					}
				}
			}
			frontier = next
		}
		if !reached {
			gaps = append(gaps, ClosureGap{EntityCEI: key, Phenomenon: phen,
				Reason: "no traversable neighbour along " + strings.Join(p.TraversalEdgeTypes, "|") + " in window"})
		}
	}

	for _, ref := range byRef {
		sort.Strings(ref.ForPhenomena)
		refs = append(refs, *ref)
	}
	sort.Slice(refs, func(i, j int) bool {
		a, b := refs[i], refs[j]
		if a.CEIKey != b.CEIKey {
			return a.CEIKey < b.CEIKey
		}
		if a.Via != b.Via {
			return a.Via < b.Via
		}
		return a.Hop < b.Hop
	})
	return refs, gaps
}

// containerPodUID extracts the pod UID a container CEI key embeds
// (uid = <podUID>/<containerName>), or "" when not container-form.
func containerPodUID(ceiKey string) string {
	parts := strings.Split(ceiKey, "|")
	if len(parts) != 6 {
		return ""
	}
	if i := strings.IndexByte(parts[5], '/'); i > 0 {
		return parts[5][:i]
	}
	return ""
}
