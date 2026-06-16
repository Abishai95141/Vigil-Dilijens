package flow

import (
	"fmt"
	"sort"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
)

// Multi-hop projected cascade (doc 15 cap. D): the one-hop anticipatory cascade
// (ProjectedCrossServiceChain) made TRANSITIVE — forecast the chain's ripple. From a
// FORECAST root (a callee whose own series is forecast to cross its OWN declared bar
// soon), it walks the observed-flow topology to the transitive callers, each carrying the
// root's forecast band INHERITED and WIDENED per hop.
//
// The charter discipline of doc 15 §4.D, enforced here:
//
//  1. ONE FORECAST ROOT PER CHAIN. Exactly one node (the chain root) carries a real
//     forecast; every downstream node is a caller reached over MEASURED flow edges,
//     carrying the INHERITED band as a PROJECTED-impact hypothesis. The CLOCK IS RUN
//     ONCE, at the root — never re-run per hop (that stacks forecast error and
//     manufactures a causal chain). Each warned callee is its own chain root.
//  2. THE BAND WIDENS, NEVER NARROWS (the PRODUCER INVARIANT). Each hop away from the
//     root widens the inherited band on BOTH sides, so earliest_child ≤ earliest_parent
//     and latest_child ≥ latest_parent — the arrival-of-impact window grows with distance
//     (more uncertainty further from the root) and NEVER collapses (doc 01). A downstream
//     node tighter than its parent would be PROJECTED-dressed-as-stronger — an absolute-
//     zero failure; the construction guarantees it cannot happen, and the gate asserts it.
//  3. DETERMINISM, OFF-DIGEST. A pure function of (flow topology, forecast roots, relation,
//     window, now); sorted collections, visited-set, maxHops. The forecast itself is
//     PROJECTED (not digest-bearing) by class; the cascade JOINS it with the MEASURED flow
//     edge and the AUTHORED why, never fused.
//
// The widening is DERIVED from the root's own band (not an invented constant): each hop
// adds half the root band's width to each side, so the widening scales with the forecast's
// own uncertainty. A future timestamp earlier than `now` is clamped to `now` (the impact
// cannot have arrived before now), which preserves the monotonicity invariant.

// ProjectedTransitiveChains derives the multi-hop PROJECTED cascades from the forecast
// roots (the warned callees) over the observed-flow topology. It returns ONE Chain per
// forecast root that has at least one transitive caller. Returns nil when there is no
// authored relation (rel.Why == "") or no root has a caller over a flow edge.
func ProjectedTransitiveChains(edges *identity.EdgeStore, roots []ProjectedDegradedWorkload, rel Relation, window identity.TimeWindow, now time.Time, maxHops int) []Chain {
	if len(roots) == 0 || rel.Why == "" {
		return nil
	}
	byKey := make(map[string]ProjectedDegradedWorkload, len(roots))
	rootKeys := make([]string, 0, len(roots))
	for _, r := range roots {
		k := r.CEI.Key()
		if _, seen := byKey[k]; !seen {
			rootKeys = append(rootKeys, k)
		}
		byKey[k] = r
	}
	sort.Strings(rootKeys)

	var chains []Chain
	for _, rk := range rootKeys {
		if c, ok := buildProjectedChain(byKey[rk], edges, rel, window, now, maxHops); ok {
			chains = append(chains, c)
		}
	}
	sort.SliceStable(chains, func(i, j int) bool {
		return chains[i].MostUpstreamDegradedNode < chains[j].MostUpstreamDegradedNode
	})
	return chains
}

// buildProjectedChain BFS-walks the transitive callers of one forecast root, assigning
// each the root's band widened by its hop distance. Returns ok=false when the root has no
// caller over a flow edge (no projected impact to surface).
func buildProjectedChain(root ProjectedDegradedWorkload, edges *identity.EdgeStore, rel Relation, window identity.TimeWindow, now time.Time, maxHops int) (Chain, bool) {
	rootKey := root.CEI.Key()
	labelByKey := map[string]string{rootKey: root.Label}

	type qItem struct {
		key string
		hop int
	}
	queue := []qItem{{rootKey, 0}}
	visited := map[string]bool{rootKey: true}

	var steps []PathStep
	var gaps []ChainGap
	bandByHop := map[int]*ProjectedBand{0: widenedBand(root, 0, now)}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		if maxHops > 0 && cur.hop >= maxHops {
			// Ceiling: state it if there are further callers we are not walking.
			callers := edges.NeighboursInto(EdgeTypeFlow, cur.key, window)
			if len(callers) > 0 {
				gaps = append(gaps, ChainGap{
					From:   labelByKey[cur.key],
					Reason: "hop ceiling reached: further callers exist but the projected walk is bounded (maxHops)",
				})
			}
			continue
		}

		callers := edges.NeighboursInto(EdgeTypeFlow, cur.key, window)
		sort.Slice(callers, func(i, j int) bool { return callers[i].To.Key() < callers[j].To.Key() })
		for _, n := range callers {
			caller := n.To.Key()
			if caller == rootKey {
				continue // a flow cycle back to the root: never re-impact the forecast root
			}
			if _, ok := labelByKey[caller]; !ok {
				labelByKey[caller] = RoleLabel(n.To)
			}
			childHop := cur.hop + 1
			parentBand := bandByHop[cur.hop]
			childBand := widenedBand(root, childHop, now)
			// PRODUCER INVARIANT (doc 15 §4.D): the child band must CONTAIN the parent's
			// (widen, never narrow). Guaranteed by construction; asserted defensively so a
			// future change can never silently ship a narrowing band.
			if !bandContains(parentBand, childBand) {
				gaps = append(gaps, ChainGap{
					From: labelByKey[cur.key], To: labelByKey[caller],
					Reason: fmt.Sprintf("projected band would narrow at hop %d — dropped (band must never tighten downstream)", childHop),
				})
				continue
			}
			bandByHop[childHop] = childBand
			steps = append(steps, PathStep{
				Hop:                  childHop,
				Upstream:             labelByKey[cur.key],
				UpstreamPhenomenon:   upstreamPhen(root, cur.hop),
				Downstream:           labelByKey[caller],
				DownstreamPhenomenon: rel.Downstream,
				EdgeClass:            "MEASURED observed flow",
				EdgeTraversal:        n.Result.String(),
				Why:                  rel.Why,
				WhyClass:             "AUTHORED",
				Temporal:             rel.Temporal,
				Author:               rel.Author,
				Version:              rel.Version,
				Band:                 childBand,
			})
			if !visited[caller] {
				visited[caller] = true
				queue = append(queue, qItem{caller, childHop})
			}
		}
	}

	if len(steps) == 0 {
		return Chain{}, false // a lone forecast root with no caller is a warning, not a cascade
	}

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

	// Symptoms: the root (PROJECTED — forecast to cross) + each impacted caller (PROJECTED
	// — impacted soon, with the widened band). Built from the path so it stays in sync.
	var symptoms []SymptomOut
	symptoms = append(symptoms, SymptomOut{
		Workload: root.Label, Phenomenon: PhenUpstreamDegradation, Class: "PROJECTED",
		Detail: "forecast to cross its " + root.Metric + " bar " + bandPhrase(root) + precursorPhrase(root),
	})
	seen := map[string]bool{root.Label: true}
	impactedKeys := make([]string, 0, len(steps))
	for _, s := range steps {
		if !seen[s.Downstream] {
			seen[s.Downstream] = true
			impactedKeys = append(impactedKeys, s.Downstream)
		}
	}
	bandLabel := map[string]*ProjectedBand{}
	for _, s := range steps {
		if _, ok := bandLabel[s.Downstream]; !ok {
			bandLabel[s.Downstream] = s.Band
		}
	}
	sort.Strings(impactedKeys)
	for _, lbl := range impactedKeys {
		b := bandLabel[lbl]
		symptoms = append(symptoms, SymptomOut{
			Workload: lbl, Phenomenon: rel.Downstream, Class: "PROJECTED",
			Detail: fmt.Sprintf("projected-impact %d hop(s) from the forecast root (band %s, %s)", b.HopsFromRoot, bandWindow(b), b.WidenNote),
		})
	}
	sort.SliceStable(symptoms, func(i, j int) bool {
		if symptoms[i].Phenomenon != symptoms[j].Phenomenon {
			return symptoms[i].Phenomenon < symptoms[j].Phenomenon
		}
		return symptoms[i].Workload < symptoms[j].Workload
	})

	return Chain{
		MostUpstreamDegradedNode: root.Label,
		NodeClass:                "PROJECTED (forecast-seeded transitive impact over observed-flow edges)",
		NodeBasis:                "the single forecast root (its own series forecast to cross its own declared bar); every downstream node inherits the root's band, WIDENED per hop — the clock is run once, never per hop",
		Path:                     steps,
		Gaps:                     gaps,
		Symptoms:                 symptoms,
		RootBand:                 bandByHop[0],
		GeneratedAt:              now,
	}, true
}

// widenedBand returns the root's forecast band widened by `hop` flow hops. The widening
// per hop is HALF the root band's width on each side (derived from the forecast's own
// uncertainty, not an invented constant), so the band strictly widens with distance and
// never collapses. earliest is clamped to `now` (the impact cannot precede now). The
// Earliest/Latest fields are RFC3339 (date-bearing) so a band that straddles midnight UTC
// compares CHRONOLOGICALLY as a string — never misordered (the surface shortens them for
// display). A zero-width root band (step ≤ 0) would collapse to a line; doc 01 forbids a
// band that collapses, so such a band is surfaced with its far edge OPEN instead.
func widenedBand(root ProjectedDegradedWorkload, hop int, now time.Time) *ProjectedBand {
	step := root.LatestAt.Sub(root.EarliestAt) / 2 // half the root band width
	off := time.Duration(hop) * step
	earliest := root.EarliestAt.Add(-off)
	if earliest.Before(now) {
		earliest = now
	}
	b := &ProjectedBand{
		Class: "PROJECTED", HopsFromRoot: hop,
		Earliest:    rfc(earliest),
		RootMetric:  root.Metric,
		RootCrossAt: hm(root.CrossAt),
		Confidence:  confOrNA(root.Confidence),
	}
	switch {
	case root.LatestBeyondHorizon || step <= 0:
		// Open far edge: the root crossing may exceed the horizon, OR the root band has no
		// width (a non-credible point forecast) — either way the downstream window cannot
		// be bounded on the far side, and a band must NEVER collapse to a line (doc 01).
		b.Open = true
	default:
		b.Latest = rfc(root.LatestAt.Add(off))
	}
	switch {
	case hop == 0:
		b.WidenNote = "the forecast root's own band"
	case step <= 0:
		b.WidenNote = "inherited from the forecast root (zero-width root → far edge open, never a line)"
	default:
		b.WidenNote = fmt.Sprintf("inherited from the forecast root, widened ±%s/hop (×%d)", step.Round(time.Second), hop)
	}
	return b
}

// bandContains reports whether child's window contains parent's (the producer invariant:
// earliest_child ≤ earliest_parent AND latest_child ≥ latest_parent). An open (beyond-
// horizon) far edge contains any finite latest. The Earliest/Latest fields are RFC3339, so
// a plain string comparison is CHRONOLOGICALLY correct even across the UTC midnight boundary
// (the bug an HH:MMZ render would have hidden). The same comparison runs in the gate scorer.
func bandContains(parent, child *ProjectedBand) bool {
	if parent == nil || child == nil {
		return false
	}
	if child.Earliest > parent.Earliest {
		return false // child's earliest is LATER than parent's — narrowing on the near side
	}
	if parent.Open {
		return child.Open // an open parent can only be contained by an open child
	}
	if child.Open {
		return true // a finite parent is contained by an open child
	}
	return child.Latest >= parent.Latest
}

func upstreamPhen(root ProjectedDegradedWorkload, hop int) string {
	if hop == 0 {
		return PhenUpstreamDegradation
	}
	return PhenDownstreamImpact
}

func bandWindow(b *ProjectedBand) string {
	if b.Open {
		return shortTime(b.Earliest) + " onward (far edge open)"
	}
	return shortTime(b.Earliest) + "–" + shortTime(b.Latest)
}

// rfc renders a band edge as a date-bearing RFC3339 UTC timestamp. RFC3339-UTC strings
// sort lexically in chronological order, so bandContains (and the gate scorer) can compare
// band edges as plain strings WITHOUT the midnight-wrap bug an HH:MMZ render would carry.
func rfc(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// shortTime renders an RFC3339 band edge as HH:MMZ for human-facing prose (the comparable
// field stays RFC3339; only the display is shortened). Unparseable input passes through.
func shortTime(s string) string {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	return t.UTC().Format("15:04Z")
}

func precursorPhrase(root ProjectedDegradedWorkload) string {
	if len(root.PrecursorPhenomena) == 0 {
		return ""
	}
	out := "; per the graph, known "
	for i, p := range root.PrecursorPhenomena {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out + " precursor"
}

func confOrNA(c string) string {
	if c == "" {
		return "confidence n/a"
	}
	return c
}

func hm(t time.Time) string { return t.UTC().Format("15:04Z") }
