package flow

import (
	"sort"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
)

// Transitive root-cause chain (doc 15 cap. B): the one-hop cross-service cascade
// (CrossServiceChain) made TRANSITIVE — `BlastRadiusFor` walked across many hops.
//
// It stitches MEASURED-degraded workloads into an ordered chain by walking the OBSERVED
// flow EdgeStore, but every charter rule of the one-hop lane still holds, plus three new
// ones the transitivity forces:
//
//   1. DIRECTION IS NEVER DECIDED BY TIMING. A flow adjacency is oriented by the OBSERVED
//      flow-edge direction (caller→callee, via NeighboursInto), GATED by the AUTHORED
//      relation's existence (the curated cross-service phenomenon_relation, surfaced
//      verbatim). Absent the authored relation there is NO chain — the lane stays off and
//      says so. (The killed shortcut — inferring direction from intra-tick onset — has no
//      signal: every fingerprint in a tick shares one evalNow. doc 15 §3.) Timing is never
//      consulted; `now` only stamps GeneratedAt.
//   2. SILENT INTERMEDIATES ARE TRAVERSED FOR CONNECTIVITY BUT NEVER ASSERTED. A flow-
//      adjacent node that carries NO measured degradation breaks the asserted chain: the
//      chain is never bridged across it (that would assert a link through an unmeasured
//      node — a weakest-input violation). The silence is surfaced as a stated Gap ON a
//      chain that has at least one asserted step; a PURELY-silent component (every path to
//      a further degraded node runs only through unmeasured nodes) currently reports as
//      no-chain (it under-claims — never a false bridge; surfacing that lone silence is a
//      tracked follow-up).
//   3. DETERMINISM, OFF-DIGEST. A pure function of (flow topology, degraded set, relation,
//      window): every collection is sorted by a total key, a visited-set terminates
//      cycles, and maxHops bounds the walk. It writes ZERO bytes into the replay digest
//      (it runs on the warm path, exactly like CrossServiceChain).
//
// JOIN, never FUSE: each node carries its OWN measured phenomenon, each edge is the
// MEASURED observed flow, the only "why" is the AUTHORED relation note. The scaffolding
// never says "X caused Y" (asserted by HasForbiddenToken in the test).

// TransitiveMaxHops bounds the transitive walk (doc 15 cap. B §3). A real cluster's
// degraded-service chain is short; this generous ceiling guards against a pathological
// deep walk while the visited-set already guarantees termination. Surfaced as a stated
// Gap when a chain would extend past it.
const TransitiveMaxHops = 8

// flowAdjacency is one degraded caller reachable in one flow hop from a degraded callee.
type flowAdjacency struct {
	to        string // the caller's role key (the impacted, downstream node)
	traversal string // "valid" | "suspect" (the edge-validity contract)
}

// TransitiveChains derives the transitive root-cause chains from a MEASURED-degraded
// set over the observed-flow topology. It returns ONE Chain per connected degraded
// component that has at least one impact edge — independent, flow-DISCONNECTED faults
// therefore produce SEPARATE chains, never one merged chain (the cardinal anti-false-
// chain rule). Returns nil when there is no authored relation (rel.Why == "") or no
// degraded node has a flow-adjacent degraded caller. maxHops <= 0 means unbounded
// (the visited-set still terminates every walk).
func TransitiveChains(edges *identity.EdgeStore, degraded []DegradedWorkload, rel Relation, window identity.TimeWindow, now time.Time, maxHops int) []Chain {
	// No authored relation ⇒ no direction ⇒ no chain (doc 15 cap. B §1). Stated by the caller.
	if len(degraded) == 0 || rel.Why == "" {
		return nil
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
	sort.Strings(keys)
	degradedSet := make(map[string]bool, len(byKey))
	for k := range byKey {
		degradedSet[k] = true
	}

	labelByKey := make(map[string]string, len(byKey))
	for k, d := range byKey {
		labelByKey[k] = d.Label
	}

	// Impact adjacency among degraded nodes: from a degraded node U (a callee), each of
	// its degraded callers V (NeighboursInto a flow edge) is impacted DOWNSTREAM — the
	// authored relation's direction. A non-degraded caller is a SILENT intermediate.
	impact := make(map[string][]flowAdjacency, len(keys))
	silent := make(map[string][]string, len(keys))
	for _, u := range keys {
		var degAdj []flowAdjacency
		var sil []string
		for _, n := range edges.NeighboursInto(EdgeTypeFlow, u, window) {
			caller := n.To.Key()
			if caller == u {
				continue
			}
			if degradedSet[caller] {
				degAdj = append(degAdj, flowAdjacency{to: caller, traversal: n.Result.String()})
				if _, ok := labelByKey[caller]; !ok {
					labelByKey[caller] = RoleLabel(n.To)
				}
			} else {
				sil = append(sil, RoleLabel(n.To))
			}
		}
		sort.Slice(degAdj, func(i, j int) bool { return degAdj[i].to < degAdj[j].to })
		impact[u] = degAdj
		if len(sil) > 0 {
			sort.Strings(sil)
			silent[u] = dedupStrings(sil)
		}
	}

	// Roots: a degraded node that does NOT itself call another degraded node (inbound-
	// clean — the deepest dependency, the most-upstream degraded node). Mirrors
	// StructuralCascade's root rule, generalized to start the transitive walk.
	callsAnotherDegraded := make(map[string]bool, len(keys))
	for _, u := range keys {
		for _, n := range edges.Neighbours(EdgeTypeFlow, u, window) { // the callees U calls
			if degradedSet[n.To.Key()] {
				callsAnotherDegraded[u] = true
				break
			}
		}
	}
	var roots []string
	for _, u := range keys {
		if !callsAnotherDegraded[u] {
			roots = append(roots, u)
		}
	}
	if len(roots) == 0 {
		// A pure cycle has no inbound-clean node: every node is a candidate root, in sorted
		// order; the visited-set makes the walk terminate and the global-visited dedup keeps
		// the first component owner. (keys is already sorted.)
		roots = append(roots, keys...)
	}

	globalVisited := make(map[string]bool, len(keys))
	var chains []Chain
	for _, root := range roots {
		if globalVisited[root] {
			continue
		}
		c, ok := buildChainFromRoot(root, impact, silent, byKey, labelByKey, rel, now, maxHops, globalVisited)
		if ok {
			chains = append(chains, c)
		}
	}
	sort.SliceStable(chains, func(i, j int) bool {
		return chains[i].MostUpstreamDegradedNode < chains[j].MostUpstreamDegradedNode
	})
	return chains
}

// buildChainFromRoot does the deterministic BFS from one root over the degraded impact
// edges, emitting the ordered Path (by hop, then upstream, then downstream) and the
// stated Gaps (silent intermediates, hop ceiling). Returns ok=false when the component
// has no impact edge (a lone degraded node is a finding, not a chain).
func buildChainFromRoot(root string, impact map[string][]flowAdjacency, silent map[string][]string, byKey map[string]DegradedWorkload, labelByKey map[string]string, rel Relation, now time.Time, maxHops int, globalVisited map[string]bool) (Chain, bool) {
	type qItem struct {
		key string
		hop int
	}
	queue := []qItem{{root, 0}}
	localVisited := map[string]bool{root: true}
	globalVisited[root] = true
	onChain := map[string]bool{root: true}

	var steps []PathStep
	var gaps []ChainGap
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		// Silent flow-adjacent callers: traversed for connectivity, NEVER asserted (the
		// chain is not bridged across them). One stated gap each.
		for _, s := range silent[cur.key] {
			gaps = append(gaps, ChainGap{
				From: labelByKey[cur.key], To: s,
				Reason: "silent intermediate: flow-adjacent but carries no measured degradation — chain not bridged (weakest-input)",
			})
		}

		if maxHops > 0 && cur.hop >= maxHops {
			if len(impact[cur.key]) > 0 {
				gaps = append(gaps, ChainGap{
					From:   labelByKey[cur.key],
					Reason: "hop ceiling reached: further degraded callers exist but the walk is bounded (maxHops)",
				})
			}
			continue
		}

		for _, a := range impact[cur.key] {
			steps = append(steps, PathStep{
				Hop:                  cur.hop + 1,
				Upstream:             labelByKey[cur.key],
				UpstreamPhenomenon:   byKey[cur.key].Phenomenon,
				Downstream:           labelByKey[a.to],
				DownstreamPhenomenon: byKey[a.to].Phenomenon,
				EdgeClass:            "MEASURED observed flow",
				EdgeTraversal:        a.traversal,
				Why:                  rel.Why,
				WhyClass:             "AUTHORED",
				Temporal:             rel.Temporal,
				Author:               rel.Author,
				Version:              rel.Version,
			})
			onChain[a.to] = true
			if !localVisited[a.to] {
				localVisited[a.to] = true
				globalVisited[a.to] = true
				queue = append(queue, qItem{a.to, cur.hop + 1})
			}
		}
	}

	if len(steps) == 0 {
		return Chain{}, false // a lone degraded node is a finding, not a root-cause chain
	}

	// SliceStable, not Slice: the sort keys (display labels) are not guaranteed injective
	// with the CEI key, so two distinct entities could share a key. The pre-sort order is
	// already deterministic (BFS over key-sorted adjacency), so a STABLE sort keeps the
	// output deterministic without relying on the (contractually unstable) Slice behaviour
	// over equal-key runs.
	sort.SliceStable(steps, func(i, j int) bool {
		if steps[i].Hop != steps[j].Hop {
			return steps[i].Hop < steps[j].Hop
		}
		if steps[i].Upstream != steps[j].Upstream {
			return steps[i].Upstream < steps[j].Upstream
		}
		return steps[i].Downstream < steps[j].Downstream
	})
	sort.SliceStable(gaps, func(i, j int) bool {
		if gaps[i].From != gaps[j].From {
			return gaps[i].From < gaps[j].From
		}
		return gaps[i].To < gaps[j].To
	})

	// Symptoms: every node ON this chain, with its OWN measured phenomenon (MEASURED).
	symptomKeys := make([]string, 0, len(onChain))
	for k := range onChain {
		symptomKeys = append(symptomKeys, k)
	}
	sort.Strings(symptomKeys)
	var symptoms []SymptomOut
	for _, k := range symptomKeys {
		d := byKey[k]
		symptoms = append(symptoms, SymptomOut{
			Workload: d.Label, Phenomenon: d.Phenomenon, Class: "MEASURED", Detail: d.Detail,
		})
	}
	sort.Slice(symptoms, func(i, j int) bool {
		if symptoms[i].Phenomenon != symptoms[j].Phenomenon {
			return symptoms[i].Phenomenon < symptoms[j].Phenomenon
		}
		return symptoms[i].Workload < symptoms[j].Workload
	})

	return Chain{
		MostUpstreamDegradedNode: labelByKey[root],
		NodeClass:                "MEASURED (transitive structural fan-in over observed-flow edges, authored-relation-oriented)",
		NodeBasis:                "deepest degraded node calling no other degraded node; the chain walks its degraded callers transitively over observed-flow edges",
		Path:                     steps,
		Gaps:                     gaps,
		Symptoms:                 symptoms,
		GeneratedAt:              now,
	}, true
}

func dedupStrings(sorted []string) []string {
	if len(sorted) < 2 {
		return sorted
	}
	out := sorted[:1]
	for _, s := range sorted[1:] {
		if s != out[len(out)-1] {
			out = append(out, s)
		}
	}
	return out
}
