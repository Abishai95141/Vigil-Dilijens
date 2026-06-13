package flow

import (
	"fmt"
	"sort"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
)

// Symptom is one MEASURED finding handed to the walk. The degraded end
// (Phenomenon == relation.Trigger) is the seed; impacted callers are DERIVED from
// the observed-flow graph, never asserted independently of a measured edge.
type Symptom struct {
	Workload   identity.CEI
	Label      string // namespace/workload
	Phenomenon string // PHEN_UPSTREAM_DEGRADATION (degraded callee)
	Class      string // "MEASURED" | "SYNTHETIC"
	Detail     string // the measured basis
}

// Walk runs the reverse traversal: for each MEASURED degraded callee, it follows
// the observed-flow edges BACKWARD (callee → its callers) under the validity
// contract, pairing each caller as a downstream-impacted node per the AUTHORED
// relation. It then names the most-upstream degraded node as a STRUCTURAL position.
//
// The walk emits a JOIN — MEASURED edge + AUTHORED why + structural position —
// never a fused causal sentence. Cause is not asserted; it is the authored note.
func Walk(g *Graph, symptoms []Symptom, rel Relation, at time.Time) Chain {
	window := identity.TimeWindow{Start: at, End: at}

	degraded := make(map[string]Symptom) // callee role key -> its degradation symptom
	for _, s := range symptoms {
		if s.Phenomenon == rel.Trigger {
			degraded[s.Workload.Key()] = s
		}
	}

	edges := g.Edges() // canonically sorted
	// inbound-clean: a degraded callee T is "clean" iff T does not itself call
	// another degraded callee (no flow edge T->T2 with T2 degraded). The cleanest
	// degraded node is the most upstream in the observed chain.
	callsAnotherDegraded := make(map[string]bool)
	for _, e := range edges {
		if _, fromDeg := degraded[e.From.Key()]; fromDeg {
			if _, toDeg := degraded[e.To.Key()]; toDeg {
				callsAnotherDegraded[e.From.Key()] = true
			}
		}
	}

	var links []Link
	impactedCallers := make(map[string]map[string]bool) // calleeKey -> set of caller keys
	calleeLabel := make(map[string]string)
	for _, e := range edges {
		ds, ok := degraded[e.To.Key()]
		if !ok {
			continue
		}
		tr := g.Store().Traverse(EdgeTypeFlow, e.From.Key(), e.To.Key(), window)
		if tr == identity.TraversalAbsent {
			continue // the validity contract: an absent/expired edge is not a co-occurrence
		}
		_ = ds
		calleeLabel[e.To.Key()] = e.ToLabel
		if impactedCallers[e.To.Key()] == nil {
			impactedCallers[e.To.Key()] = make(map[string]bool)
		}
		impactedCallers[e.To.Key()][e.From.Key()] = true
		links = append(links, Link{
			Impacted:      e.FromLabel,
			Degraded:      e.ToLabel,
			EdgeClass:     "MEASURED observed flow",
			ServicePorts:  sortedPorts(e.ServicePorts),
			ConnDepth:     e.ConnCount,
			EdgeTraversal: tr.String(),
			Why:           rel.Why,
			WhyClass:      "AUTHORED",
			Temporal:      rel.Temporal,
			Author:        rel.Author,
			Version:       rel.Version,
		})
	}
	sort.Slice(links, func(i, j int) bool {
		if links[i].Degraded != links[j].Degraded {
			return links[i].Degraded < links[j].Degraded
		}
		return links[i].Impacted < links[j].Impacted
	})

	// Root = inbound-clean degraded callee reaching the most impacted callers.
	// Deterministic tie-break by callee key.
	rootKey, rootLabel, best := "", "", -1
	keys := make([]string, 0, len(impactedCallers))
	for k := range impactedCallers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if callsAnotherDegraded[k] {
			continue // not the most-upstream degraded node
		}
		if n := len(impactedCallers[k]); n > best {
			best, rootKey, rootLabel = n, k, calleeLabel[k]
		}
	}
	if rootKey == "" { // no inbound-clean node (cyclic); fall back to max impacted
		for _, k := range keys {
			if n := len(impactedCallers[k]); n > best {
				best, rootKey, rootLabel = n, k, calleeLabel[k]
			}
		}
	}

	// Symptoms out: the MEASURED degraded seeds, plus the DERIVED downstream-impact
	// callers (each backed by a MEASURED observed-flow edge).
	var symOut []SymptomOut
	dkeys := make([]string, 0, len(degraded))
	for k := range degraded {
		dkeys = append(dkeys, k)
	}
	sort.Strings(dkeys)
	for _, k := range dkeys {
		s := degraded[k]
		symOut = append(symOut, SymptomOut{Workload: s.Label, Phenomenon: s.Phenomenon, Class: s.Class, Detail: s.Detail})
	}
	for _, l := range links {
		symOut = append(symOut, SymptomOut{
			Workload:   l.Impacted,
			Phenomenon: rel.Downstream,
			Class:      "MEASURED",
			Detail:     fmt.Sprintf("observed-flow edge to degraded %s (conn-depth %d, ports %v)", l.Degraded, l.ConnDepth, l.ServicePorts),
		})
	}

	cov := g.Coverage()
	return Chain{
		MostUpstreamDegradedNode: rootLabel,
		NodeClass:                "MEASURED (structural fan-in over observed-flow edges)",
		NodeBasis:                "degraded callee reaching the most impacted callers, with no inbound flow-symptom edge",
		Links:                    links,
		Symptoms:                 symOut,
		CoverageGaps: CoverageOut{
			ResolvableFlows: cov.ResolvableFlows, SnatMaskedFlows: cov.SnatMaskedFlows,
			UnresolvedFlows: cov.UnresolvedFlows, InfraFlows: cov.InfraFlows,
			UnrepliedFlows: cov.UnrepliedFlows, Snapshots: g.Ticks(),
		},
		GeneratedAt: at,
	}
}

func sortedPorts(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for p := range m {
		out = append(out, p)
	}
	sort.Ints(out)
	return out
}
