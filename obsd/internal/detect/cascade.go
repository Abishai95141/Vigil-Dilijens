// Cascades and blast radius (doc 07 §3.4, M5). `phenomenon_relation` edges are
// AUTHORED trigger→downstream knowledge; detection uses them two ways, both
// strictly descriptive:
//
//   - Cascade recognition: when a trigger phenomenon and its declared
//     downstream BOTH light up on topologically related entities within the
//     relation's window, they surface as ONE correlated story — "the authored
//     relationship is currently manifest", never a causal proof.
//   - Blast radius: given a finding, walking the downstream relations across
//     valid topology yields the entities the graph says are AT RISK — an
//     authored relationship made concrete on this cluster's topology, never a
//     prediction about the neighbours.
//
// Determinism (doc 07 §3.7): cascade recognition is windowed, so it depends on
// the SEQUENCE of prior ticks — the tracker accumulates finding references
// tick by tick exactly the same way live and in replay (the engine replays
// frames in recorded order; a process restart resets the tracker on both
// sides, the run-start frame marking the boundary). Within one tick everything
// is canonically ordered.
package detect

import (
	"sort"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/identity"
)

// AtRisk is one blast-radius entry: an entity the authored downstream relation
// puts at risk, topologically related to the finding's anchor right now.
type AtRisk struct {
	CEIKey     string
	Phenomenon string // the downstream phenomenon the entity participates in
	Why        string // the AUTHORED relation note, verbatim
	Temporal   string // the authored temporal tag (e.g. T0+terminal)
	Related    string // same-entity | <edge-type> (how the topology relates them)
}

// FindingRef names one finding occurrence — the cascade linkage unit
// (doc 07 §3.8).
type FindingRef struct {
	Phenomenon  string
	EntityCEI   string
	EvaluatedAt time.Time
	Quality     MatchQuality
}

// Cascade is one recognized trigger→downstream story (doc 07 §3.4): both
// phenomena lit on topologically related entities within the window. The Why
// text is the graph's authored note, attributed — the only "reason" shown.
type Cascade struct {
	Trigger    FindingRef
	Downstream FindingRef
	Why        string // AUTHORED relation note
	Temporal   string // authored temporal tag on the relation
	Related    string // same-entity | <edge-type>
}

// downstreamRef is one normalized trigger→downstream declaration.
type downstreamRef struct {
	id       string // the downstream phenomenon
	why      string
	temporal string
}

// resolveDownstream normalizes the graph's phenomenon_relation edges into a
// trigger → downstreams map. Two authored shapes express the same direction:
// a relation with role "downstream" on P points AT P's downstream; a relation
// with role "trigger" on Q points at what TRIGGERS Q (so Q is the target's
// downstream). "corroborating" relations are associations, not direction —
// they recognize no cascade.
func resolveDownstream(g *graph.Graph) map[string][]downstreamRef {
	out := map[string][]downstreamRef{}
	ids := make([]string, 0, len(g.Phenomena))
	for id := range g.Phenomena {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		for _, r := range g.Phenomena[id].Relations {
			switch r.Role {
			case "downstream":
				out[id] = append(out[id], downstreamRef{id: r.TargetID, why: r.Why, temporal: r.TemporalOrder})
			case "trigger":
				out[r.TargetID] = append(out[r.TargetID], downstreamRef{id: id, why: r.Why, temporal: r.TemporalOrder})
			}
		}
	}
	for k := range out {
		refs := out[k]
		sort.Slice(refs, func(i, j int) bool { return refs[i].id < refs[j].id })
		// One declaration per (trigger, downstream) pair: keep the first in
		// canonical order if both authored shapes name the same pair.
		dedup := refs[:0]
		for i, r := range refs {
			if i == 0 || refs[i-1].id != r.id {
				dedup = append(dedup, r)
			}
		}
		out[k] = dedup
	}
	return out
}

// CascadeTracker holds the recent finding references cascade recognition pairs
// against — the LATEST occurrence per (phenomenon, entity), pruned to the
// window. Live it persists across evaluation ticks in-process; in replay the
// engine resets it at every run-start frame, mirroring the process restart
// that emptied it live. Not safe for concurrent use (one evaluation goroutine).
type CascadeTracker struct {
	window time.Duration
	recent map[string]FindingRef // phenomenon \x1f entity -> latest ref
}

// NewCascadeTracker builds a tracker with the cascade window (v1: the default
// co-occurrence window from the pinned parameter set; per-relation widths are
// an M6 sensitivity matter).
func NewCascadeTracker(window time.Duration) *CascadeTracker {
	return &CascadeTracker{window: window, recent: map[string]FindingRef{}}
}

// Reset empties the tracker (a process-run boundary).
func (t *CascadeTracker) Reset() {
	t.recent = map[string]FindingRef{}
}

// Observe records this tick's findings and prunes anything older than the
// window. Call AFTER recognizing cascades for the tick, so a downstream firing
// this tick pairs with triggers from EARLIER ticks (or this one — same-tick
// pairs are handled by Cascades directly).
func (t *CascadeTracker) Observe(now time.Time, findings []Finding) {
	for _, f := range findings {
		t.recent[f.Phenomenon+"\x1f"+f.EntityCEI] = FindingRef{
			Phenomenon: f.Phenomenon, EntityCEI: f.EntityCEI,
			EvaluatedAt: f.EvaluatedAt, Quality: f.Quality,
		}
	}
	for k, ref := range t.recent {
		if now.Sub(ref.EvaluatedAt) > t.window {
			delete(t.recent, k)
		}
	}
}

// Cascades recognizes the authored relations currently manifest (doc 07 §3.4):
// for every finding D this tick whose phenomenon has a declared trigger T, a T
// finding on a topologically related entity — this tick or within the tracker
// window — pairs into a Cascade. The trigger must precede or coincide with the
// downstream, never follow it. Results are canonically sorted. Call BEFORE
// tracker.Observe for this tick.
func (m *Matcher) Cascades(now time.Time, findings []Finding, tracker *CascadeTracker, topo Topology, w identity.TimeWindow) []Cascade {
	if len(findings) == 0 {
		return nil
	}
	podKeyByUID := podKeyIndex(topo)
	// Triggers visible to this tick: the tracker's window plus this tick's own
	// findings (same-tick recognition; latest occurrence wins per key).
	triggers := map[string]FindingRef{}
	if tracker != nil {
		for k, ref := range tracker.recent {
			if now.Sub(ref.EvaluatedAt) <= tracker.window {
				triggers[k] = ref
			}
		}
	}
	for _, f := range findings {
		triggers[f.Phenomenon+"\x1f"+f.EntityCEI] = FindingRef{
			Phenomenon: f.Phenomenon, EntityCEI: f.EntityCEI,
			EvaluatedAt: f.EvaluatedAt, Quality: f.Quality,
		}
	}
	triggerKeys := make([]string, 0, len(triggers))
	for k := range triggers {
		triggerKeys = append(triggerKeys, k)
	}
	sort.Strings(triggerKeys)

	var out []Cascade
	for _, d := range findings {
		// Which phenomena does the graph declare as TRIGGERS of d's phenomenon?
		// (Invert the trigger→downstream map for this downstream.)
		for trigPhen, refs := range m.downstream {
			for _, ref := range refs {
				if ref.id != d.Phenomenon {
					continue
				}
				for _, tk := range triggerKeys {
					tr := triggers[tk]
					if tr.Phenomenon != trigPhen {
						continue
					}
					if tr.EvaluatedAt.After(d.EvaluatedAt) {
						continue // a trigger never follows its downstream
					}
					if tr.Phenomenon == d.Phenomenon && tr.EntityCEI == d.EntityCEI {
						continue // a finding is not its own cascade
					}
					rel, ok := related(tr.EntityCEI, d.EntityCEI, topo, w, podKeyByUID)
					if !ok {
						continue // not topologically related — no story, however suggestive
					}
					out = append(out, Cascade{
						Trigger: tr,
						Downstream: FindingRef{Phenomenon: d.Phenomenon, EntityCEI: d.EntityCEI,
							EvaluatedAt: d.EvaluatedAt, Quality: d.Quality},
						Why: ref.why, Temporal: ref.temporal, Related: rel,
					})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Downstream.EntityCEI != b.Downstream.EntityCEI {
			return a.Downstream.EntityCEI < b.Downstream.EntityCEI
		}
		if a.Downstream.Phenomenon != b.Downstream.Phenomenon {
			return a.Downstream.Phenomenon < b.Downstream.Phenomenon
		}
		if a.Trigger.EntityCEI != b.Trigger.EntityCEI {
			return a.Trigger.EntityCEI < b.Trigger.EntityCEI
		}
		return a.Trigger.Phenomenon < b.Trigger.Phenomenon
	})
	return out
}

// podKeyIndex maps pod UID -> pod CEI key from the snapshot's runs-on sources
// (the container→pod anchor resolution both cascades and blast radii need).
func podKeyIndex(topo Topology) map[string]string {
	out := map[string]string{}
	if topo == nil {
		return out
	}
	for _, src := range topo.Sources(identity.EdgeRunsOn) {
		cei, err := identity.ParseKey(src)
		if err == nil && cei.Kind == "Pod" {
			out[cei.UID] = src
		}
	}
	return out
}

// BlastRadiusFor derives the at-risk set for phenomenonID manifesting (or
// PROJECTED to manifest, doc 09 M4) at anchorCEI: the same authored downstream
// walk detection findings use — ONE implementation, so a warning's blast
// radius can never disagree with a finding's. The result is AUTHORED
// relationship made concrete on current topology, never a prediction about
// the neighbours.
func (m *Matcher) BlastRadiusFor(phenomenonID, anchorCEI string, selected map[string][]string, topo Topology, w identity.TimeWindow) []AtRisk {
	f := Finding{Phenomenon: phenomenonID, EntityCEI: anchorCEI}
	return m.blastRadius(&f, selected, topo, w, podKeyIndex(topo))
}

// blastRadius walks the finding's downstream relations across valid topology
// (doc 07 §3.4): the at-risk set is every entity that PARTICIPATES in a
// declared downstream phenomenon (per the selection funnel — the graph's own
// participation, never invented) and is topologically related to the finding's
// anchor right now. v1 relatedness policy (stated): the entity itself, or one
// valid/suspect hop over any known edge type; wider radii await harness
// evidence (doc 07 §8).
func (m *Matcher) blastRadius(f *Finding, selected map[string][]string, topo Topology, w identity.TimeWindow, podKeyByUID map[string]string) []AtRisk {
	refs := m.downstream[f.Phenomenon]
	if len(refs) == 0 {
		return nil
	}
	keys := make([]string, 0, len(selected))
	for k := range selected {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var out []AtRisk
	for _, ref := range refs {
		for _, k := range keys {
			participates := false
			for _, phen := range selected[k] {
				if phen == ref.id {
					participates = true
					break
				}
			}
			if !participates {
				continue
			}
			rel, ok := related(f.EntityCEI, k, topo, w, podKeyByUID)
			if !ok {
				continue
			}
			out = append(out, AtRisk{
				CEIKey: k, Phenomenon: ref.id, Why: ref.why, Temporal: ref.temporal, Related: rel,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CEIKey != out[j].CEIKey {
			return out[i].CEIKey < out[j].CEIKey
		}
		return out[i].Phenomenon < out[j].Phenomenon
	})
	return out
}

// related reports how two entities are topologically related right now:
// "same-entity" (equal keys, equal topology anchors, or containers of the same
// pod — containment is identity, not a hop, so it holds even when the pod's
// runs-on edge is absent from the snapshot), or the edge type of a valid edge
// one hop between their topology anchors. A SUSPECT hop still relates them —
// the edge-validity contract (doc 07 §3.2) makes suspect usable-but-NAMED — so
// the relation carries the suspicion marker rather than passing as clean.
// Absent topology relates nothing beyond identity (degrade-never-fabricate
// applies to stories too).
func related(aKey, bKey string, topo Topology, w identity.TimeWindow, podKeyByUID map[string]string) (string, bool) {
	if aKey == bKey {
		return "same-entity", true
	}
	if ua, ub := containerPodUID(aKey), containerPodUID(bKey); ua != "" && ua == ub {
		return "same-entity", true
	}
	ta, tb := topoAnchor(aKey, podKeyByUID), topoAnchor(bKey, podKeyByUID)
	if ta == tb {
		return "same-entity", true
	}
	if topo == nil {
		return "", false
	}
	for _, typ := range []identity.EdgeType{identity.EdgeRunsOn, identity.EdgeMounts, identity.EdgeSelects} {
		r1 := topo.Traverse(typ, ta, tb, w)
		r2 := topo.Traverse(typ, tb, ta, w)
		if r1 == identity.TraversalValid || r2 == identity.TraversalValid {
			return string(typ), true
		}
		if r1 == identity.TraversalSuspect || r2 == identity.TraversalSuspect {
			return string(typ) + " (suspect)", true
		}
	}
	return "", false
}

// topoAnchor maps an entity key to its topology-walk anchor: containers
// resolve to their pod (containment is identity, not a hop); everything else
// anchors on itself.
func topoAnchor(key string, podKeyByUID map[string]string) string {
	if uid := containerPodUID(key); uid != "" {
		if pk, ok := podKeyByUID[uid]; ok {
			return pk
		}
	}
	return key
}
