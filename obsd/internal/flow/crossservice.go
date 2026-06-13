package flow

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
)

// DegradedWorkload is a MEASURED degradation seed for the cross-service cascade: a
// workload (role CEI) that currently carries an obsd finding (its own measured
// problem), tagged with the phenomenon that fired. The cascade walks the flow edges
// BACKWARD from it to its callers.
type DegradedWorkload struct {
	CEI        identity.CEI // role CEI — must be the identity-layer role CEI (the join key)
	Label      string       // namespace/workload, for surfacing
	Phenomenon string       // the obsd finding's phenomenon (the measured degradation)
	Detail     string       // e.g. "OOM_KILL_CGROUP finding (degraded)"
}

// CrossServiceChain composes the obsd cross-service cascade from live findings: for
// each MEASURED degraded callee, it walks the production flow EdgeStore backward to
// the callers (the validity contract) and joins the AUTHORED relation. It reuses
// StructuralCascade (the digest-bearing core) for the structure, so the surfaced
// chain matches exactly what a replay of the captured flow topology would produce.
//
// JOIN, never FUSE: MEASURED degradation (the finding) + MEASURED observed-flow edge
// + AUTHORED why + structural position — each labelled, never a causal sentence.
// Returns ok=false when no degraded callee has any caller over a valid flow edge.
func CrossServiceChain(edges *identity.EdgeStore, degraded []DegradedWorkload, rel Relation, window identity.TimeWindow, now time.Time) (Chain, bool) {
	if len(degraded) == 0 {
		return Chain{}, false
	}
	byKey := make(map[string]DegradedWorkload, len(degraded))
	keys := make([]string, 0, len(degraded))
	for _, d := range degraded {
		k := d.CEI.Key()
		if _, seen := byKey[k]; !seen {
			keys = append(keys, k)
		}
		byKey[k] = d
	}

	sc := StructuralCascade(edges, keys, window)
	if len(sc.Links) == 0 {
		return Chain{}, false // degraded, but no callers over a valid flow edge
	}

	// Label map: degraded callees (from the seeds) + impacted callers (from the walk).
	labelByKey := make(map[string]string)
	for k, d := range byKey {
		labelByKey[k] = d.Label
	}
	for _, k := range keys {
		for _, n := range edges.NeighboursInto(EdgeTypeFlow, k, window) {
			labelByKey[n.To.Key()] = RoleLabel(n.To)
		}
	}

	var links []Link
	for _, sl := range sc.Links {
		links = append(links, Link{
			Impacted:      labelByKey[sl.Impacted],
			Degraded:      labelByKey[sl.Degraded],
			EdgeClass:     "MEASURED observed flow",
			EdgeTraversal: sl.Traversal,
			Why:           rel.Why,
			WhyClass:      "AUTHORED",
			Temporal:      rel.Temporal,
			Author:        rel.Author,
			Version:       rel.Version,
		})
	}

	var symptoms []SymptomOut
	for _, k := range keys {
		if d, ok := byKey[k]; ok {
			if _, hasCaller := callerCount(sc, k); hasCaller {
				symptoms = append(symptoms, SymptomOut{Workload: d.Label, Phenomenon: d.Phenomenon, Class: "MEASURED", Detail: d.Detail})
			}
		}
	}
	for _, l := range links {
		symptoms = append(symptoms, SymptomOut{
			Workload: l.Impacted, Phenomenon: rel.Downstream, Class: "MEASURED",
			Detail: fmt.Sprintf("observed-flow edge to degraded %s (captured topology, %s)", l.Degraded, l.EdgeTraversal),
		})
	}
	sort.Slice(symptoms, func(i, j int) bool {
		if symptoms[i].Phenomenon != symptoms[j].Phenomenon {
			return symptoms[i].Phenomenon < symptoms[j].Phenomenon
		}
		return symptoms[i].Workload < symptoms[j].Workload
	})

	return Chain{
		MostUpstreamDegradedNode: labelByKey[sc.Root],
		NodeClass:                "MEASURED (structural fan-in over observed-flow edges)",
		NodeBasis:                "degraded callee reaching the most impacted callers, with no inbound flow-symptom edge",
		Links:                    links,
		Symptoms:                 symptoms,
		GeneratedAt:              now,
	}, true
}

func callerCount(sc StructuralResult, calleeKey string) (int, bool) {
	n := 0
	for _, l := range sc.Links {
		if l.Degraded == calleeKey {
			n++
		}
	}
	return n, n > 0
}

// RoleLabel renders a role CEI as namespace/workload (RoleKey is "Kind/name").
func RoleLabel(c identity.CEI) string {
	name := c.RoleKey
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if c.Namespace == "" {
		return name
	}
	return c.Namespace + "/" + name
}
