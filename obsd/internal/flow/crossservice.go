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
func CrossServiceChain(edges *identity.EdgeStore, degraded []DegradedWorkload, rel Relation, window identity.TimeWindow, now time.Time, specific ...EntityCausalRelation) (Chain, bool) {
	if len(degraded) == 0 {
		return Chain{}, false
	}
	spec := entityCausalIndex(specific)
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
		l := Link{
			Impacted:      labelByKey[sl.Impacted],
			Degraded:      labelByKey[sl.Degraded],
			EdgeClass:     "MEASURED observed flow",
			EdgeTraversal: sl.Traversal,
			Why:           rel.Why,
			WhyClass:      "AUTHORED",
			Temporal:      rel.Temporal,
			Author:        rel.Author,
			Version:       rel.Version,
		}
		// docs/33 build 3 bridge: if a NAMED operator authored a SPECIFIC cause→effect for
		// this exact role-pair (cause == degraded callee, effect == impacted caller, agreeing
		// with the observed flow direction), surface THAT authored relation's note + author —
		// the same cause→effect recognized as the operator's chain on recurrence, never the
		// generic upstream→downstream why.
		if s, ok := spec[sl.Degraded+"\x00"+sl.Impacted]; ok {
			l.Why = s.Why
			l.WhyClass = "AUTHORED (operator)"
			l.Author = s.Author
			if s.Version != "" {
				l.Version = s.Version
			}
		}
		links = append(links, l)
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

// ProjectedDegradedWorkload is a PROJECTED degradation seed for the ANTICIPATORY
// cross-service cascade (doc 15 Phase E): a workload (role CEI) whose own series is
// FORECAST to cross its bar SOON (the gated early-warning lane's warned set), carrying
// the forecast band so the propagated downstream impact names a bounded window. The
// walk is identical to the MEASURED lane, but every IMPACT statement it produces is
// PROJECTED — no stronger than its weakest input, the forecast (doc 01 weakest-input).
type ProjectedDegradedWorkload struct {
	CEI                 identity.CEI // role CEI — the identity-layer join key
	Label               string       // namespace/workload
	Metric              string       // the forecast series (e.g. working_set)
	PrecursorPhenomena  []string     // AUTHORED graph precursor ids (verbatim, never paraphrased)
	Confidence          string       // tight | moderate | wide
	CrossAt             time.Time    // the point projection (never shown without the band)
	EarliestAt          time.Time    // band lower edge
	LatestAt            time.Time    // band upper edge
	LatestBeyondHorizon bool         // the band's far edge is open (crossing may exceed the horizon)
}

// ProjectedCrossServiceChain composes the ANTICIPATORY cross-service cascade (doc 15
// Phase E): for each upstream callee FORECAST to cross soon, it walks the production
// flow EdgeStore backward to the callers and joins the ONE AUTHORED relation — naming
// the callers as PROJECTED-to-be-impacted SOON, with the upstream's forecast band.
//
// JOIN, never FUSE, with the weakest-input rule (doc 01): the upstream root symptom is
// PROJECTED (a forecast), the flow edge stays MEASURED (observed conntrack), the why
// stays AUTHORED (the curated relation), and the downstream impact symptom is PROJECTED
// — never upgraded to MEASURED, never a causal sentence. The band NEVER collapses.
// Returns ok=false when no forecast-degraded callee has a caller over a valid flow edge.
func ProjectedCrossServiceChain(edges *identity.EdgeStore, projected []ProjectedDegradedWorkload, rel Relation, window identity.TimeWindow, now time.Time) (Chain, bool) {
	if len(projected) == 0 {
		return Chain{}, false
	}
	byKey := make(map[string]ProjectedDegradedWorkload, len(projected))
	keys := make([]string, 0, len(projected))
	for _, d := range projected {
		k := d.CEI.Key()
		if _, seen := byKey[k]; !seen {
			keys = append(keys, k)
		}
		byKey[k] = d
	}

	sc := StructuralCascade(edges, keys, window)
	if len(sc.Links) == 0 {
		return Chain{}, false // forecast-degraded, but no caller over a valid flow edge
	}

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
			EdgeClass:     "MEASURED observed flow", // the edge is observed, unchanged
			EdgeTraversal: sl.Traversal,
			Why:           rel.Why,
			WhyClass:      "AUTHORED", // the relation is curated, unchanged
			Temporal:      rel.Temporal,
			Author:        rel.Author,
			Version:       rel.Version,
		})
	}

	var symptoms []SymptomOut
	// Upstream root symptoms: PROJECTED — the callee is forecast to cross soon.
	for _, k := range keys {
		d, ok := byKey[k]
		if !ok {
			continue
		}
		if _, hasCaller := callerCount(sc, k); !hasCaller {
			continue
		}
		precursors := ""
		if len(d.PrecursorPhenomena) > 0 {
			precursors = "; per the graph, known " + strings.Join(d.PrecursorPhenomena, ", ") + " precursor"
		}
		symptoms = append(symptoms, SymptomOut{
			Workload:   d.Label,
			Phenomenon: PhenUpstreamDegradation,
			Class:      "PROJECTED",
			Detail:     "projected to cross its " + d.Metric + " bar " + bandPhrase(d) + precursors,
		})
	}
	// Downstream caller symptoms: PROJECTED — impacted soon, with the upstream band.
	for _, sl := range sc.Links {
		d, ok := byKey[sl.Degraded]
		if !ok {
			continue
		}
		symptoms = append(symptoms, SymptomOut{
			Workload:   labelByKey[sl.Impacted],
			Phenomenon: rel.Downstream,
			Class:      "PROJECTED",
			Detail:     fmt.Sprintf("projected-impact via observed-flow edge to %s (upstream forecast %s)", d.Label, bandPhrase(d)),
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
		NodeClass:                "PROJECTED (structural fan-in over observed-flow edges, forecast-seeded)",
		NodeBasis:                "callee FORECAST to cross its bar soon, reaching the most impacted callers, with no inbound flow-symptom edge",
		Links:                    links,
		Symptoms:                 symptoms,
		GeneratedAt:              now,
	}, true
}

// bandPhrase renders a forecast crossing as a BAND that never collapses to a line
// (doc 01 §3): the point projection is shown only WITH its [earliest, latest] window,
// and an open far edge is stated. Register: modal + bounded, never "will cross".
func bandPhrase(d ProjectedDegradedWorkload) string {
	hm := func(t time.Time) string { return t.UTC().Format("15:04Z") }
	conf := d.Confidence
	if conf == "" {
		conf = "confidence n/a"
	}
	if d.LatestBeyondHorizon {
		return fmt.Sprintf("~%s (band %s onward — far edge open, %s)", hm(d.CrossAt), hm(d.EarliestAt), conf)
	}
	return fmt.Sprintf("~%s (band %s–%s, %s)", hm(d.CrossAt), hm(d.EarliestAt), hm(d.LatestAt), conf)
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
