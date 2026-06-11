package selection

import (
	"sort"
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
)

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
	SelectedAt     time.Time
	Trigger        string
}

// Result is one selection pass over the inventory. Every inventory entity has
// exactly one record — hidden gaps are impossible by construction.
type Result struct {
	Records      []Record // sorted by EntityCEI
	TierACount   int
	NoneByReason map[Reason]int
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
	// signal -> phenomena that declare it a member (participates_in edges).
	sigPhen := map[string][]string{}
	phenIDs := make([]string, 0, len(g.Phenomena))
	for id := range g.Phenomena {
		phenIDs = append(phenIDs, id)
	}
	sort.Strings(phenIDs) // deterministic accumulation order
	for _, id := range phenIDs {
		for _, m := range g.Phenomena[id].Members {
			sigPhen[m.SignalID] = append(sigPhen[m.SignalID], id)
		}
	}
	// rule -> signal (the authored bridge each bound variable rides on).
	ruleSig := make(map[string]string, len(g.Rules))
	for _, r := range g.Rules {
		ruleSig[r.ID] = r.Signal
	}

	out := map[string][]string{}
	seen := map[string]map[string]bool{}
	for i := range res.Bindings {
		b := &res.Bindings[i]
		// Gate 1: evaluable bound variable (the fingerprint-eligible filter,
		// mirrored from observe.Materialize).
		if b.State != binding.StateBound || b.Bar == nil || b.Validation == binding.ValidationFailed {
			continue
		}
		// Gate 2: the variable's signal is a member of >=1 phenomenon.
		for _, phen := range sigPhen[ruleSig[b.RuleID]] {
			if seen[b.CEIKey] == nil {
				seen[b.CEIKey] = map[string]bool{}
			}
			if !seen[b.CEIKey][phen] {
				seen[b.CEIKey][phen] = true
				out[b.CEIKey] = append(out[b.CEIKey], phen)
			}
		}
	}
	for k := range out {
		sort.Strings(out[k])
	}
	return out
}

// Select runs the M1 funnel over the live inventory, producing one record per
// entity (the watch list AND the none-list, each with its reason). The Tier-A
// verdicts come from TierASet — the same deterministic core detection uses.
func Select(inventory []identity.InstanceRecord, res *binding.Result, g *graph.Graph, now time.Time, trigger string) *Result {
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
		case covered[key]:
			rec.PassesCoverage = true
			rec.Tier = TierNone
			rec.Reason = ReasonNoPhenomenonMember
		case instantiated[key]:
			rec.Tier = TierNone
			rec.Reason = ReasonNoEvaluableVariable
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
	return r
}
