// Package detect is the "now" half of the runtime — see doc.go for the full
// contract. This file implements M1 (the entity-local matcher) and M2
// (first-order spans under the edge-validity traversal contract).
package detect

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/observe"
)

// Topology is the validity-aware topology view the matcher walks (doc 07 §3.2).
// Live detection passes a per-tick SNAPSHOT-rebuilt store (never the mutating
// live store — recorded must equal evaluated); replay rebuilds the same from
// the bundle. The contract is identity.EdgeStore's: every traversed edge's
// validity interval must overlap the evaluation window; suspect degrades;
// absent never contributes.
type Topology interface {
	Traverse(typ identity.EdgeType, fromKey, toKey string, w identity.TimeWindow) identity.Traversal
	Neighbours(typ identity.EdgeType, fromKey string, w identity.TimeWindow) []identity.Neighbour
	NeighboursInto(typ identity.EdgeType, toKey string, w identity.TimeWindow) []identity.Neighbour
	Sources(typ identity.EdgeType) []string
}

// MatchQuality is full or degraded (doc 07 §3.3). A lone crossing is not an alert
// (§3.5) — only a phenomenon's required-member conjunction surfaces.
type MatchQuality string

const (
	QualityFull     MatchQuality = "full"     // every required member observable and matching
	QualityDegraded MatchQuality = "degraded" // required members matched, but span/coverage incomplete
)

// MemberEvidence is one member's contribution to a finding — the temporal evidence
// row of doc 07 §3.8, with its authored note attached verbatim. A neighbour-scoped
// member satisfied across the span produces one row PER satisfying neighbour,
// each citing the entity and the edge (with its traversal verdict) it crossed.
type MemberEvidence struct {
	SignalID   string
	Metric     string
	Role       string // required | corroborating | supporting
	Temporal   string // T0- | T0 | T0+...
	Observable bool   // a check exists AND its fingerprint variable was fresh
	Met        bool
	State      string // the fingerprint state that satisfied/failed the check
	SampleAt   time.Time
	BarFlagged bool   // the bar this state was computed against is a flagged ontology default
	Note       string // AUTHORED member note (the only "why", attributed)

	// Neighbour evidence (first-order members only; empty for anchor members):
	// the CEI whose fingerprint satisfied the check, the edge type crossed, and
	// the traversal verdict for that edge over the evaluation window.
	Neighbour  string
	Via        string // edge type ("" = on the anchor itself)
	EdgeResult string // valid | suspect
}

// EdgeStep is one traversed edge in a finding's span instantiation (doc 07 §3.8:
// "the edge path with validity stamps").
type EdgeStep struct {
	Type   string
	From   string
	To     string
	Result string // valid | suspect
}

// Finding is a detection finding (doc 07 §3.8): MEASURED, with AUTHORED references
// attached — never fused.
type Finding struct {
	Phenomenon   string
	Label        string
	GraphVersion string
	EntityCEI    string
	Namespace    string
	Name         string
	Kind         string
	EvaluatedAt  time.Time

	Quality      MatchQuality
	Completeness float64 // observable-required-met / total-required (doc 07 §8)

	RequiredTotal       int
	RequiredMet         int
	RequiredUnobserved  int
	SupportingMet       int
	SupportingObservble int

	Members      []MemberEvidence // the evidence trail (required + observed supporting)
	Unobservable []string         // required members that could not be checked here

	// Span instantiation (doc 07 §3.8) — first-order findings only.
	Span         string     // entity-local | first-order
	SpanPath     []EdgeStep // every edge evidence actually crossed, with verdicts
	SuspectEdges []string   // edges whose staleness degrades this match (§3.2)
}

// Matcher evaluates phenomena against fingerprints: entity-local everywhere a
// fingerprint exists (M1), first-order at authored anchors with one-hop walks
// under the validity contract (M2). Stateless and deterministic: same
// fingerprints + same graph version + same topology snapshot => same findings.
type Matcher struct {
	g          *graph.Graph
	checkBySig map[string]map[string]*graph.MemberCheck // phen -> signal -> check
	entityLoc  []*graph.Phenomenon                      // entity-local phenomena, sorted by id
	firstOrder []*graph.Phenomenon                      // first-order phenomena WITH checks, sorted by id
	secondSkip int                                      // second-order phenomena with checks — skipped, stated (M3)
}

// NewMatcher indexes the graph's phenomena and their authored checks.
func NewMatcher(g *graph.Graph) *Matcher {
	m := &Matcher{g: g, checkBySig: map[string]map[string]*graph.MemberCheck{}}
	for id, p := range g.Phenomena {
		bySig := map[string]*graph.MemberCheck{}
		for _, c := range g.ChecksFor(id) {
			bySig[c.Signal] = c
		}
		switch p.Span {
		case graph.SpanEntityLocal:
			m.entityLoc = append(m.entityLoc, p)
			m.checkBySig[id] = bySig
		case graph.SpanFirstOrder:
			if len(bySig) > 0 { // a spanned phenomenon without checks has nothing to evaluate
				m.firstOrder = append(m.firstOrder, p)
				m.checkBySig[id] = bySig
			}
		case graph.SpanSecondOrder:
			if len(bySig) > 0 {
				m.secondSkip++ // M3 — counted so the gap is stated, never silent
			}
		}
	}
	sort.Slice(m.entityLoc, func(i, j int) bool { return m.entityLoc[i].ID < m.entityLoc[j].ID })
	sort.Slice(m.firstOrder, func(i, j int) bool { return m.firstOrder[i].ID < m.firstOrder[j].ID })
	return m
}

// EntityLocalCount is how many entity-local phenomena the matcher evaluates.
func (m *Matcher) EntityLocalCount() int { return len(m.entityLoc) }

// FirstOrderCount is how many first-order phenomena carry evaluable checks.
func (m *Matcher) FirstOrderCount() int { return len(m.firstOrder) }

// SecondOrderSkipped is how many second-order phenomena have checks the matcher
// cannot yet evaluate (doc 07 M3) — surfaced, never silently dropped.
func (m *Matcher) SecondOrderSkipped() int { return m.secondSkip }

// Match is the full per-tick evaluation (doc 07 M1+M2): entity-local phenomena
// on every selected fingerprint, first-order phenomena at their authored
// anchors with one-hop walks over topo. selected filters ANCHOR entities only
// (nil = no filter — selection's absence widens attention, never narrows it);
// neighbour fingerprints contribute evidence regardless of their own tier (the
// 06 M2 closure puts them in the watch set). topo == nil states a topology-less
// evaluation: first-order phenomena are then skipped entirely (the pre-M2
// regime, kept for replaying bundles captured before topology was recorded).
func (m *Matcher) Match(fps []observe.Fingerprint, selected map[string][]string, topo Topology, w identity.TimeWindow) []Finding {
	index := make(map[string]*observe.Fingerprint, len(fps))
	for i := range fps {
		index[fps[i].CEIKey] = &fps[i]
	}
	// Container → pod containment (the spans overlay's addressing principle:
	// containment is identity, not a topology hop). Pod keys are enumerated from
	// the snapshot's runs-on sources; a container's pod UID is its own UID prefix.
	podKeyByUID := map[string]string{}
	if topo != nil {
		for _, src := range topo.Sources(identity.EdgeRunsOn) {
			cei, err := identity.ParseKey(src)
			if err == nil && cei.Kind == "Pod" {
				podKeyByUID[cei.UID] = src
			}
		}
	}

	var out []Finding
	for i := range fps {
		fp := fps[i]
		if selected != nil && len(selected[fp.CEIKey]) == 0 {
			continue
		}
		for _, p := range m.entityLoc {
			if f, ok := m.evalPhenomenon(p, fp); ok {
				out = append(out, f)
			}
		}
		if topo == nil {
			continue
		}
		for _, p := range m.firstOrder {
			if fp.Kind != p.Anchor {
				continue // first-order phenomena are evaluated at their authored anchor only
			}
			if f, ok := m.evalFirstOrder(p, fp, index, topo, w, podKeyByUID); ok {
				out = append(out, f)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].EntityCEI != out[j].EntityCEI {
			return out[i].EntityCEI < out[j].EntityCEI
		}
		if out[i].Completeness != out[j].Completeness {
			return out[i].Completeness > out[j].Completeness
		}
		return out[i].Phenomenon < out[j].Phenomenon
	})
	return out
}

// MatchFingerprint evaluates every entity-local phenomenon against one entity's
// fingerprint, returning the findings that lit up (full or degraded). A phenomenon
// with no observable matching required member produces no finding (a single
// fingerprint state is not a phenomenon, doc 07 §3.5).
func (m *Matcher) MatchFingerprint(fp observe.Fingerprint) []Finding {
	var out []Finding
	for _, p := range m.entityLoc {
		if f, ok := m.evalPhenomenon(p, fp); ok {
			out = append(out, f)
		}
	}
	// Strongest (most complete) first, then by phenomenon id for determinism.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Completeness != out[j].Completeness {
			return out[i].Completeness > out[j].Completeness
		}
		return out[i].Phenomenon < out[j].Phenomenon
	})
	return out
}

func (m *Matcher) evalPhenomenon(p *graph.Phenomenon, fp observe.Fingerprint) (Finding, bool) {
	bySig := m.checkBySig[p.ID]
	f := Finding{
		Phenomenon: p.ID, Label: p.Label, GraphVersion: m.g.Version,
		EntityCEI: fp.CEIKey, Namespace: fp.Namespace, Name: fp.Name, Kind: fp.Kind,
		EvaluatedAt: fp.EvaluatedAt, Span: p.Span,
	}

	// Members come from the resolved participates_in edges (signal -> phenomenon),
	// already on the graph. Stable-sort by a TOTAL key (signal, temporal, role) so
	// ties never reorder non-deterministically (doc 07 §3.7).
	members := append([]graph.Member(nil), p.Members...)
	sort.SliceStable(members, func(i, j int) bool {
		a, b := members[i], members[j]
		if a.SignalID != b.SignalID {
			return a.SignalID < b.SignalID
		}
		if a.TemporalOrder != b.TemporalOrder {
			return a.TemporalOrder < b.TemporalOrder
		}
		return a.Role < b.Role
	})

	for _, mem := range members {
		required := mem.Role == "required"
		check := bySig[mem.SignalID]
		ev := MemberEvidence{
			SignalID: mem.SignalID, Role: mem.Role, Temporal: mem.TemporalOrder, Note: mem.Why,
		}
		if check != nil {
			ev.Metric = check.Metric
			met, state, fresh, at := satisfies(check, fp)
			ev.Observable = fresh
			ev.Met = met && fresh
			ev.State = state
			ev.SampleAt = at
			ev.BarFlagged = barFlagged(check, fp)
			if check.Note != "" {
				ev.Note = mem.Why + " [" + check.Note + "]"
			}
		}

		if required {
			f.RequiredTotal++
			switch {
			case ev.Observable && ev.Met:
				f.RequiredMet++
				f.Members = append(f.Members, ev)
			case ev.Observable && !ev.Met:
				// A required member is observable and NOT satisfied: the conjunction
				// fails — this phenomenon is not happening here.
				return Finding{}, false
			default:
				// No check, or its variable absent/stale: unobservable here. Carry the
				// AUTHORED note (the only "why") so the curator's explanation of the
				// MISSING member is surfaced, not dropped (doc 07 §4).
				f.RequiredUnobserved++
				label := mem.SignalID
				if ev.Metric != "" {
					label += " (" + ev.Metric + ", stale/absent)"
				}
				if ev.Note != "" {
					label += " — " + ev.Note
				}
				f.Unobservable = append(f.Unobservable, label)
			}
		} else {
			// Supporting member: strengthens, never required.
			if check != nil {
				f.SupportingObservble++
				if ev.Met {
					f.SupportingMet++
					f.Members = append(f.Members, ev)
				}
			}
		}
	}

	// No positive required evidence ⇒ not a match (a phenomenon needs at least one
	// of its required members observed and matching).
	if f.RequiredMet == 0 {
		return Finding{}, false
	}
	if f.RequiredTotal > 0 {
		f.Completeness = float64(f.RequiredMet) / float64(f.RequiredTotal)
	}
	if f.RequiredUnobserved == 0 {
		f.Quality = QualityFull
	} else {
		f.Quality = QualityDegraded
	}
	return f, true
}

// neighbourHit is one neighbour fingerprint that satisfied a member check.
type neighbourHit struct {
	key     string
	via     string
	result  identity.Traversal
	state   string
	at      time.Time
	flagged bool
}

// evalFirstOrder evaluates one first-order phenomenon at its anchor fingerprint
// (doc 07 §3.2). Anchor members evaluate on fp itself; neighbour members on the
// fingerprints of entities one hop away along the phenomenon's DECLARED edge
// types, under the validity contract:
//
//   - absent edge (or validity outside the window): the neighbour's evidence is
//     NOT a co-occurrence — it never contributes (degrade-never-fabricate);
//   - suspect edge: evidence counts but the match is DEGRADED, the edge named;
//   - valid edge: supports a full match.
//
// Container anchors walk from their containing pod (containment is identity,
// not a hop — the spans overlay's addressing principle).
func (m *Matcher) evalFirstOrder(p *graph.Phenomenon, fp observe.Fingerprint, index map[string]*observe.Fingerprint, topo Topology, w identity.TimeWindow, podKeyByUID map[string]string) (Finding, bool) {
	bySig := m.checkBySig[p.ID]
	f := Finding{
		Phenomenon: p.ID, Label: p.Label, GraphVersion: m.g.Version,
		EntityCEI: fp.CEIKey, Namespace: fp.Namespace, Name: fp.Name, Kind: fp.Kind,
		EvaluatedAt: fp.EvaluatedAt, Span: p.Span,
	}

	// Where topology walks start: the anchor itself, or — for container anchors —
	// the containing pod. Container-scope BINDINGS carry the pod's CEI key (the
	// compiler fans out per container but anchors on the pod instance, doc 04),
	// so a container fingerprint usually IS keyed by its pod and walks directly.
	// A true container-instance key (uid = podUID/name, the stream-side form)
	// resolves its pod through the topology's runs-on sources; a container whose
	// pod is unknown to the topology cannot reach any neighbour — its neighbour
	// members are unobservable, stated.
	walkFrom := fp.CEIKey
	walkNote := ""
	if fp.Kind == "Container" {
		if uid := containerPodUID(fp.CEIKey); uid != "" {
			if pk, ok := podKeyByUID[uid]; ok {
				walkFrom = pk
			} else {
				walkFrom, walkNote = "", "containing pod absent from topology"
			}
		}
	}
	// Lazy neighbour resolution per declared edge type, both directions, sorted.
	nbrCache := map[string][]identity.Neighbour{}
	neighboursFor := func(typ string) []identity.Neighbour {
		if walkFrom == "" {
			return nil
		}
		if got, ok := nbrCache[typ]; ok {
			return got
		}
		et := identity.EdgeType(typ)
		merged := append(topo.Neighbours(et, walkFrom, w), topo.NeighboursInto(et, walkFrom, w)...)
		sort.Slice(merged, func(i, j int) bool { return merged[i].To.Key() < merged[j].To.Key() })
		nbrCache[typ] = merged
		return merged
	}

	members := append([]graph.Member(nil), p.Members...)
	sort.SliceStable(members, func(i, j int) bool {
		a, b := members[i], members[j]
		if a.SignalID != b.SignalID {
			return a.SignalID < b.SignalID
		}
		if a.TemporalOrder != b.TemporalOrder {
			return a.TemporalOrder < b.TemporalOrder
		}
		return a.Role < b.Role
	})

	seenSteps := map[string]bool{}
	anchorMet := 0 // required members satisfied ON THE ANCHOR itself
	for _, mem := range members {
		required := mem.Role == "required"
		check := bySig[mem.SignalID]

		if check == nil {
			if required {
				f.RequiredTotal++
				f.RequiredUnobserved++
				f.Unobservable = append(f.Unobservable, mem.SignalID+" — "+mem.Why)
			}
			continue
		}

		note := mem.Why
		if check.Note != "" {
			note += " [" + check.Note + "]"
		}

		if check.OnAnchor() {
			// Same semantics as the entity-local path, on the anchor's fingerprint.
			ev := MemberEvidence{
				SignalID: mem.SignalID, Metric: check.Metric, Role: mem.Role,
				Temporal: mem.TemporalOrder, Note: note,
			}
			met, state, fresh, at := satisfies(check, fp)
			ev.Observable, ev.Met, ev.State, ev.SampleAt = fresh, met && fresh, state, at
			ev.BarFlagged = barFlagged(check, fp)
			if required {
				f.RequiredTotal++
				switch {
				case ev.Observable && ev.Met:
					f.RequiredMet++
					anchorMet++
					f.Members = append(f.Members, ev)
				case ev.Observable && !ev.Met:
					return Finding{}, false // observable and not happening — conjunction fails
				default:
					f.RequiredUnobserved++
					f.Unobservable = append(f.Unobservable, unobservableLabel(mem, ev.Metric, note))
				}
			} else if ev.Observable {
				f.SupportingObservble++
				if ev.Met {
					f.SupportingMet++
					f.Members = append(f.Members, ev)
				}
			}
			continue
		}

		// Neighbour-scoped member: evaluate on every traversable neighbour's
		// fingerprint along the declared edge types. Absent edges contributed
		// nothing already (Neighbours omits them).
		var hits []neighbourHit
		observable := false
		for _, typ := range p.TraversalEdgeTypes {
			for _, n := range neighboursFor(typ) {
				nfp, ok := index[n.To.Key()]
				if !ok {
					continue // neighbour has no fingerprint — coverage gap, handled below
				}
				met, state, fresh, at := satisfies(check, *nfp)
				if !fresh {
					continue
				}
				observable = true
				if met {
					hits = append(hits, neighbourHit{
						key: n.To.Key(), via: typ, result: n.Result,
						state: state, at: at, flagged: barFlagged(check, *nfp),
					})
				}
			}
		}

		if required {
			f.RequiredTotal++
		}
		switch {
		case len(hits) > 0:
			anyValid := false
			for _, h := range hits {
				if h.result == identity.TraversalValid {
					anyValid = true
				}
			}
			for _, h := range hits {
				f.Members = append(f.Members, MemberEvidence{
					SignalID: mem.SignalID, Metric: check.Metric, Role: mem.Role,
					Temporal: mem.TemporalOrder, Observable: true, Met: true,
					State: h.state, SampleAt: h.at, BarFlagged: h.flagged, Note: note,
					Neighbour: h.key, Via: h.via, EdgeResult: h.result.String(),
				})
				step := EdgeStep{Type: h.via, From: walkFrom, To: h.key, Result: h.result.String()}
				sk := step.Type + "\x1f" + step.From + "\x1f" + step.To
				if !seenSteps[sk] {
					seenSteps[sk] = true
					f.SpanPath = append(f.SpanPath, step)
				}
				if h.result == identity.TraversalSuspect && !anyValid {
					f.SuspectEdges = append(f.SuspectEdges, fmt.Sprintf("%s %s -> %s (confirmation stale)", h.via, walkFrom, h.key))
				}
			}
			if required {
				f.RequiredMet++
			} else {
				f.SupportingObservble++
				f.SupportingMet++
			}
		case observable:
			// At least one reachable neighbour evaluates this member and it is NOT
			// happening there: for a required member the conjunction fails.
			if required {
				return Finding{}, false
			}
			f.SupportingObservble++
		default:
			// No traversable neighbour carries a fresh variable for this member —
			// unobservable ACROSS THE SPAN, with the topology reason stated.
			if required {
				f.RequiredUnobserved++
				reason := "no traversable neighbour with this variable in window"
				if walkNote != "" {
					reason = walkNote
				}
				f.Unobservable = append(f.Unobservable, unobservableLabel(mem, check.Metric, note)+" ["+reason+"]")
			}
		}
	}

	// The anchor must carry positive required evidence of its own: a first-order
	// phenomenon is ABOUT its anchor ("the entity plus direct neighbours", doc 07
	// §3.2), so neighbour members corroborate — they never alone constitute the
	// match. Without this rule, EVERY container on a pressured node lights up
	// "throttling cascade (degraded)" on the node's PSI alone — observed live
	// (cpu-hog and coredns containers, whose unthrottled/limit-less anchors had
	// no ratio evidence at all): alert-fatigue noise and a claim about an anchor
	// with zero anchor evidence.
	if f.RequiredMet == 0 || anchorMet == 0 {
		return Finding{}, false
	}
	if f.RequiredTotal > 0 {
		f.Completeness = float64(f.RequiredMet) / float64(f.RequiredTotal)
	}
	sort.Strings(f.SuspectEdges)
	// Quality (doc 07 §3.2/§3.3): incomplete coverage degrades; so does ANY
	// member whose only support crossed a suspect edge.
	if f.RequiredUnobserved == 0 && len(f.SuspectEdges) == 0 {
		f.Quality = QualityFull
	} else {
		f.Quality = QualityDegraded
	}
	return f, true
}

// containerPodUID extracts the pod UID a container CEI key embeds
// (uid = <podUID>/<containerName>), or "" when the key is not container-form.
func containerPodUID(ceiKey string) string {
	parts := strings.Split(ceiKey, "|")
	if len(parts) != 6 {
		return ""
	}
	uid := parts[5]
	if i := strings.IndexByte(uid, '/'); i > 0 {
		return uid[:i]
	}
	return ""
}

func unobservableLabel(mem graph.Member, metric, note string) string {
	label := mem.SignalID
	if metric != "" {
		label += " (" + metric + ", stale/absent)"
	}
	if note != "" {
		label += " — " + note
	}
	return label
}

// satisfies evaluates one member check against the fingerprint, returning
// (conditionMet, observedState, fresh, sampleTime). fresh is false when the
// fingerprint variable is absent or stale (treated as missing, doc 05 §6).
func satisfies(c *graph.MemberCheck, fp observe.Fingerprint) (met bool, state string, fresh bool, at time.Time) {
	switch c.Facet {
	case "slope":
		vt, ok := findThreshold(fp, c.Metric)
		if !ok || vt.Stale || vt.SlopeInconclusive || vt.SlopeSamples < 2 {
			return false, "no-slope", false, time.Time{}
		}
		state = fmt.Sprintf("%s, slope %+.3g/s", vt.State, vt.Slope)
		// Materiality guard (doc 07 §3.6): a rise only counts if the level is also at
		// least the authored min_state — a trivial slope on an idle entity does not fire.
		if !meetsMinState(vt.State, c.MinState) {
			return false, state, true, vt.Deriv.SampleAt
		}
		switch c.Expect {
		case "rising":
			return vt.Slope > 0, state, true, vt.Deriv.SampleAt
		case "falling":
			return vt.Slope < 0, state, true, vt.Deriv.SampleAt
		}
		return false, state, true, vt.Deriv.SampleAt
	case "level", "ratio":
		vt, ok := findThreshold(fp, c.Metric)
		if !ok || vt.Stale {
			return false, "no-sample", false, time.Time{}
		}
		state = vt.State.String()
		switch c.Expect {
		case "crossed", "at-or-above":
			// Both require the bar actually CROSSED. StateAtThreshold is the healthy
			// approach band (strictly below the bar) and must not satisfy at-or-above.
			return vt.State.Crossed(), state, true, vt.Deriv.SampleAt
		}
		return false, state, true, vt.Deriv.SampleAt
	case "rate-guard":
		vr, ok := findRate(fp, c.Metric)
		if !ok || vr.Stale || vr.Inconclusive {
			return false, "inconclusive", false, time.Time{}
		}
		state = fmt.Sprintf("Δ%.0f/window", vr.WindowDelta)
		if c.Expect == "breached" {
			return vr.Breached, state, true, vr.Deriv.SampleAt
		}
		return false, state, true, vr.Deriv.SampleAt
	}
	return false, "unknown-facet", false, time.Time{}
}

// meetsMinState reports whether a ladder state is at least the required minimum.
func meetsMinState(s observe.ThresholdState, min string) bool {
	rank := map[string]observe.ThresholdState{
		"": observe.StateUnknown, "at-threshold": observe.StateAtThreshold,
		"above": observe.StateAbove, "well-above": observe.StateWellAbove,
	}
	return s >= rank[min]
}

// findThreshold returns the entity's threshold variable for a metric only if it is
// UNAMBIGUOUS. Two variables on one entity sharing a metric is an ambiguity (mirrors
// binding QA): rather than silently take the first (fp.Thresholds is sorted by
// RuleID, not metric), treat it as no usable evidence so the member is unobserved,
// never matched against an arbitrary bar.
func findThreshold(fp observe.Fingerprint, metric string) (observe.VariableThreshold, bool) {
	var found observe.VariableThreshold
	n := 0
	for _, t := range fp.Thresholds {
		if t.Metric == metric {
			found, n = t, n+1
		}
	}
	return found, n == 1
}

func findRate(fp observe.Fingerprint, metric string) (observe.VariableRate, bool) {
	var found observe.VariableRate
	n := 0
	for _, r := range fp.Rates {
		if r.Metric == metric {
			found, n = r, n+1
		}
	}
	return found, n == 1
}

// barFlagged reports whether the fingerprint variable a check consults was computed
// against a flagged ontology-default bar (lower trust, doc 04 §3.4) — surfaced on the
// finding so a default bar never masquerades as config-sourced.
func barFlagged(c *graph.MemberCheck, fp observe.Fingerprint) bool {
	if c.Facet == "rate-guard" {
		if vr, ok := findRate(fp, c.Metric); ok {
			return vr.Flagged
		}
		return false
	}
	if vt, ok := findThreshold(fp, c.Metric); ok {
		return vt.Flagged
	}
	return false
}
