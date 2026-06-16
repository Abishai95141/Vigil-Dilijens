// Package eventdetect builds MEASURED phenomenon findings from discrete Kubernetes
// events that the KG authors as a REQUIRED member of a phenomenon (graph-robustness
// #2, G1). It lives in its own package — not in detect — because it bridges two
// sibling packages (detect's Finding shape and events' EventFinding/Detection), and
// detect must stay free of an events dependency (the events package's own tests
// already import detect, which would close an import cycle).
//
// Why a separate producer (not the fingerprint matcher): events are DISCRETE objects,
// not gauge/counter fingerprint variables — detect.satisfies only reads
// fp.Thresholds/fp.Rates (the v3 T-C critic correction: an event never becomes a
// fabricated counter). So the event lane owns its producer; its findings JOIN the
// fingerprint findings only at the surface + cascade layer, OFF the deterministic
// replay digest (the cross-service warm-path precedent).
//
// Epistemic discipline (doc 01): the event is MEASURED (a fact read from pod status /
// the Events stream); the match is its MEASURED consequence (one authored required
// member observed). DEGRADED by construction — the phenomenon's gauge/probe members
// are unobservable on the discrete-event signal set and are NAMED, never fabricated
// (degrade-never-fabricate). The authored member note + the authored detection `why`
// are attached ADJACENTLY, never paraphrased into a causal sentence. The event never
// becomes a "trigger" and proves no cause — it recognizes an authored required member.
package eventdetect

import (
	"fmt"
	"sort"
	"time"

	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/detect"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/events"
	"github.com/Abishai95141/Vigil-Dilijens/obsd/internal/graph"
)

// Findings builds the degraded MEASURED phenomenon findings from this tick's event
// findings and the authored detection conditions. Pure and deterministic: same
// (events, detections, graph) ⇒ byte-identical findings, canonically sorted.
//
// No-false-upgrade guards (the safety contract the gate enforces):
//   - only an event whose reason+kind matches an AUTHORED detection produces a finding
//     (never the event firehose);
//   - a ROLE-UNRESOLVED event (the identity store never observed the involved object,
//     or a role-less kind like Node) does NOT produce a workload-anchored phenomenon
//     finding — it stays a standalone EventFinding, visible, never a guessed match;
//   - the detection's MemberSignal must be a REQUIRED member of the phenomenon (the
//     caller validates referentially at load); a detection that satisfies no required
//     member yields no finding here too (defence in depth).
//
// Unlike the fingerprint path, these findings are NOT subject to the MinCompleteness
// floor: a discrete authored event reason is a DEFINITIVE occurrence (the kubelet
// recorded it), not a noisy single threshold crossing — so a 1-of-N degraded match is
// legitimate and surfaces, with the missing members stated.
func Findings(g *graph.Graph, evs []events.EventFinding, dets []events.Detection, now time.Time) []detect.Finding {
	if g == nil || len(evs) == 0 || len(dets) == 0 {
		return nil
	}
	byKey := make(map[string]events.Detection, len(dets))
	for _, d := range dets {
		byKey[d.Reason+"\x00"+d.InvolvedKind] = d
	}
	var out []detect.Finding
	for _, e := range evs {
		d, ok := byKey[e.Reason+"\x00"+e.Kind]
		if !ok {
			continue // no authored detection for this reason+kind
		}
		if e.RoleUnresolved {
			continue // never assert a workload phenomenon on an unidentified entity
		}
		p := g.Phenomena[d.Phenomenon]
		if p == nil {
			continue // referential integrity should have caught this at load
		}
		f := detect.Finding{
			Phenomenon: p.ID, Label: p.Label, GraphVersion: g.Version,
			EntityCEI: e.EntityCEI, Namespace: e.Namespace, Name: e.Name, Kind: e.Kind,
			EvaluatedAt: now.UTC(), Span: p.Span, Quality: detect.QualityDegraded,
		}
		// Enumerate the phenomenon's required members: the one the event satisfies
		// becomes MEASURED evidence; every other required member is NAMED unobservable.
		members := append([]graph.Member(nil), p.Members...)
		sort.SliceStable(members, func(i, j int) bool { return members[i].SignalID < members[j].SignalID })
		for _, mem := range members {
			if mem.Role != "required" {
				continue
			}
			f.RequiredTotal++
			if mem.SignalID == d.MemberSignal {
				note := mem.Why
				if d.Why != "" {
					note += " [" + d.Why + "]"
				}
				f.Members = append(f.Members, detect.MemberEvidence{
					SignalID: mem.SignalID, Metric: "k8s-event:" + e.Reason, Role: "required",
					Temporal: mem.TemporalOrder, Observable: true, Met: true,
					State:    fmt.Sprintf("event %s ×%d", e.Reason, e.Count),
					SampleAt: e.LastTimestamp.UTC(), Note: note,
				})
				f.RequiredMet++
			} else {
				f.RequiredUnobserved++
				label := mem.SignalID
				if mem.Why != "" {
					label += " — " + mem.Why
				}
				label += " [discrete-event detection observes only the event member]"
				f.Unobservable = append(f.Unobservable, label)
			}
		}
		if f.RequiredMet == 0 {
			continue // the authored member_signal is not a required member — produce nothing
		}
		if f.RequiredTotal > 0 {
			f.Completeness = float64(f.RequiredMet) / float64(f.RequiredTotal)
		}
		out = append(out, f)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].EntityCEI != out[j].EntityCEI {
			return out[i].EntityCEI < out[j].EntityCEI
		}
		return out[i].Phenomenon < out[j].Phenomenon
	})
	return out
}
