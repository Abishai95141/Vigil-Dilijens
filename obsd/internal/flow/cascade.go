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

// Walk runs the reverse traversal and produces the surfaced chain. Its STRUCTURE
// (root + impacted pairs) comes from StructuralCascade — the digest-bearing core,
// a pure function of the captured flow topology — so the operator-facing chain can
// never disagree with what a replay would reproduce. Walk only DECORATES that core
// with surfacing metadata (labels, ports, conn-depth) that stays off the digest.
//
// The output is a JOIN — MEASURED edge + AUTHORED why + structural position — never
// a fused causal sentence. Cause is not asserted; it is the authored note.
func Walk(g *Graph, symptoms []Symptom, rel Relation, at time.Time) Chain {
	window := identity.TimeWindow{Start: at, End: at}

	degradedSym := make(map[string]Symptom) // callee role key -> its degradation symptom
	var degradedKeys []string
	for _, s := range symptoms {
		if s.Phenomenon == rel.Trigger {
			degradedSym[s.Workload.Key()] = s
			degradedKeys = append(degradedKeys, s.Workload.Key())
		}
	}

	// The digest-bearing structural cascade (store-only, replay-identical).
	sc := StructuralCascade(g.Store(), degradedKeys, window)

	// Metadata lookups for decoration (off-digest).
	edgeByPair := make(map[[2]string]*FlowEdge)
	labelByKey := make(map[string]string)
	for _, e := range g.Edges() {
		edgeByPair[[2]string{e.From.Key(), e.To.Key()}] = e
		labelByKey[e.From.Key()] = e.FromLabel
		labelByKey[e.To.Key()] = e.ToLabel
	}

	var links []Link
	for _, sl := range sc.Links {
		e := edgeByPair[[2]string{sl.Impacted, sl.Degraded}]
		if e == nil {
			continue
		}
		links = append(links, Link{
			Impacted:      e.FromLabel,
			Degraded:      e.ToLabel,
			EdgeClass:     "MEASURED observed flow",
			ServicePorts:  sortedPorts(e.ServicePorts),
			ConnDepth:     e.ConnCount,
			EdgeTraversal: sl.Traversal,
			Why:           rel.Why,
			WhyClass:      "AUTHORED",
			Temporal:      rel.Temporal,
			Author:        rel.Author,
			Version:       rel.Version,
		})
	}

	// Symptoms out: the MEASURED degraded seeds, then the DERIVED downstream-impact
	// callers (each backed by a MEASURED observed-flow edge).
	var symOut []SymptomOut
	dkeys := append([]string(nil), degradedKeys...)
	sort.Strings(dkeys)
	for _, k := range dkeys {
		s := degradedSym[k]
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
		MostUpstreamDegradedNode: labelByKey[sc.Root],
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
